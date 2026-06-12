package router

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"guise/internal/chrome"
	"guise/internal/config"
)

// routeHarness makes Route hermetic: it points config and Chrome discovery at
// temp dirs, supplies a fake chrome.exe via chrome_path (so ResolvePath is
// deterministic on any machine), and captures the would-be launch instead of
// spawning Chrome or popping a modal dialog.
type routeHarness struct {
	chromePath string
	launched   bool
	gotPath    string
	gotArgs    []string
	launchErr  error // returned by the stubbed launcher when set.
	notified   bool
}

func newRouteHarness(t *testing.T) *routeHarness {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())

	chromePath := filepath.Join(t.TempDir(), "chrome.exe")
	if err := os.WriteFile(chromePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &routeHarness{chromePath: chromePath}

	origStart, origNotify := startProcess, notifyError
	startProcess = func(path string, args ...string) error {
		h.launched = true
		h.gotPath = path
		h.gotArgs = args
		return h.launchErr
	}
	notifyError = func(string, string) { h.notified = true }
	t.Cleanup(func() { startProcess, notifyError = origStart, origNotify })
	return h
}

// writeConfig writes config.json with the harness's fake chrome_path and the
// given rules array (raw JSON, e.g. `[{"id":"1",...}]`).
func (h *routeHarness) writeConfig(t *testing.T, rulesJSON string) {
	t.Helper()
	h.writeConfigRaw(t, rulesJSON, "[]")
}

// writeConfigRaw writes config.json with raw rules and rewrites arrays, so a
// test can exercise the rewrite pipeline alongside routing.
func (h *routeHarness) writeConfigRaw(t *testing.T, rulesJSON, rewritesJSON string) {
	t.Helper()
	body := fmt.Sprintf(`{"version":1,"chrome_path":%q,"rules":%s,"rewrites":%s}`, h.chromePath, rulesJSON, rewritesJSON)
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *routeHarness) writeLocalState(t *testing.T, body string) {
	t.Helper()
	p := chrome.LocalStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouteMatchedExistingProfileLaunchesWithFlag(t *testing.T) {
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"github\\.com","profile_directory":"Profile 1"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Default":{"name":"P"},"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://github.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "https://github.com/x"}
	if h.gotPath != h.chromePath || !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("launched %q %v, want %q %v", h.gotPath, h.gotArgs, h.chromePath, want)
	}
}

func TestRouteNoMatchOmitsFlag(t *testing.T) {
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"github\\.com","profile_directory":"Profile 1"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://example.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if want := []string{"https://example.com/x"}; !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteVanishedProfileFallsBackToDefault(t *testing.T) {
	// §10: a configured profile that is no longer in Local State must drop the
	// flag rather than launch (which would make Chrome create a phantom profile).
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"github\\.com","profile_directory":"Profile 7"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Default":{"name":"P"}}}}`)

	if err := Route("https://github.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if want := []string{"https://github.com/x"}; !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v (flag should be dropped)", h.gotArgs, want)
	}
}

func TestRouteInvalidProfileSyntaxFallsBackToDefault(t *testing.T) {
	// A tampered config with an injection-y profile name must never reach the
	// chrome.exe command line; the flag is dropped.
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"github\\.com","profile_directory":"Bad\" --user-data-dir=C:\\evil"}]`)

	if err := Route("https://github.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if want := []string{"https://github.com/x"}; !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v (invalid profile flag should be dropped)", h.gotArgs, want)
	}
}

func TestRouteLaunchFailureReturnsErrorAndNotifies(t *testing.T) {
	h := newRouteHarness(t)
	h.launchErr = errors.New("boom")
	h.writeConfig(t, `[]`)

	err := Route("https://example.com")
	if err == nil {
		t.Fatal("expected an error when the launcher fails")
	}
	if !h.notified {
		t.Error("a launch failure should surface a notification")
	}
}

func TestRouteEmptyURLLaunchesBareChrome(t *testing.T) {
	h := newRouteHarness(t)
	h.writeConfig(t, `[]`)

	if err := Route(""); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if h.gotArgs != nil {
		t.Errorf("args = %v, want no args for empty URL", h.gotArgs)
	}
}

func TestRoutePreRewriteAffectsProfileAndLaunch(t *testing.T) {
	// A pre-rewrite (delayed=false) runs before matching, so the rule keyed on the
	// rewritten host wins and Chrome launches the rewritten URL.
	h := newRouteHarness(t)
	h.writeConfigRaw(t,
		`[{"id":"r","enabled":true,"pattern":"xcancel\\.com","profile_directory":"Profile 1"}]`,
		`[{"id":"swap","enabled":true,"find":"x.com","replace":"xcancel.com"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://x.com/foo"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "https://xcancel.com/foo"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteDelayedRewriteKeepsOriginalMatch(t *testing.T) {
	// A delayed rewrite (delayed=true) runs after matching, so the profile is
	// chosen from the original host while Chrome launches the rewritten URL.
	h := newRouteHarness(t)
	h.writeConfigRaw(t,
		`[{"id":"r","enabled":true,"pattern":"x\\.com","profile_directory":"Profile 1"}]`,
		`[{"id":"swap","enabled":true,"find":"x.com","replace":"xcancel.com","delayed":true}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://x.com/foo"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	// Profile 1 (matched on x.com) but the launched URL is rewritten.
	want := []string{"--profile-directory=Profile 1", "https://xcancel.com/foo"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteDelayedRewriteWouldHaveBrokenMatch(t *testing.T) {
	// Same rewrite as above but delayed: had it run before matching, the x.com
	// rule would no longer match and the URL would fall through to Chrome default.
	// Delaying it preserves the match — this is the reason the option exists.
	h := newRouteHarness(t)
	h.writeConfigRaw(t,
		`[{"id":"r","enabled":true,"pattern":"x\\.com","profile_directory":"Profile 1"}]`,
		`[{"id":"swap","enabled":true,"find":"x.com","replace":"xcancel.com","delayed":true}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://x.com/foo"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if h.gotArgs[0] != "--profile-directory=Profile 1" {
		t.Errorf("delayed rewrite should preserve the match; args = %v", h.gotArgs)
	}
}

func TestResolveDropsMissingProfileSoPreviewMatchesRoute(t *testing.T) {
	// Resolve is what the editor preview calls; it must apply the same
	// invalid/missing-profile fallback as Route so a vanished profile previews as
	// Chrome default instead of a phantom hit.
	h := newRouteHarness(t)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Default":{"name":"P"}}}}`)

	cfg := &config.Config{Version: 1, Rules: []config.Rule{
		{ID: "r", Enabled: true, Pattern: `x\.com`, ProfileDirectory: "Profile 7"}, // not in Local State
	}}
	got := Resolve(cfg, "https://x.com/foo")
	if got.Rule == nil || got.Rule.ID != "r" {
		t.Fatalf("expected rule r to match, got %+v", got.Rule)
	}
	if !got.ProfileDropped || got.ProfileDirectory != "" {
		t.Errorf("missing profile should drop to Chrome default: dropped=%v dir=%q", got.ProfileDropped, got.ProfileDirectory)
	}
}

func TestResolveKeepsExistingProfile(t *testing.T) {
	h := newRouteHarness(t)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	cfg := &config.Config{Version: 1, Rules: []config.Rule{
		{ID: "r", Enabled: true, Pattern: `x\.com`, ProfileDirectory: "Profile 1"},
	}}
	got := Resolve(cfg, "https://x.com/foo")
	if got.ProfileDropped || got.ProfileDirectory != "Profile 1" {
		t.Errorf("existing profile should be kept: dropped=%v dir=%q", got.ProfileDropped, got.ProfileDirectory)
	}
}

func TestResolveAppliesRewritesAroundMatch(t *testing.T) {
	h := newRouteHarness(t)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	cfg := &config.Config{
		Version: 1,
		Rules:   []config.Rule{{ID: "r", Enabled: true, Pattern: `x\.com`, ProfileDirectory: "Profile 1"}},
		Rewrites: []config.Rewrite{
			{ID: "late", Enabled: true, Find: "x.com", Replace: "xcancel.com", Delayed: true},
		},
	}
	got := Resolve(cfg, "https://x.com/foo")
	// Matched on the original host (delayed rewrite runs after the match)...
	if got.Rule == nil || got.ProfileDirectory != "Profile 1" {
		t.Fatalf("delayed rewrite should not affect the match: %+v", got)
	}
	// ...but the final URL is rewritten, and the applied list records it.
	if got.URL != "https://xcancel.com/foo" {
		t.Errorf("final URL = %q, want rewritten", got.URL)
	}
	if len(got.Applied) != 1 || got.Applied[0] != "late" {
		t.Errorf("applied = %v, want [late]", got.Applied)
	}
}

func TestLaunchArgs(t *testing.T) {
	tests := []struct {
		name       string
		profileDir string
		url        string
		want       []string
	}{
		{"profile and url", "Profile 3", "https://github.com/foo", []string{"--profile-directory=Profile 3", "https://github.com/foo"}},
		{"no match keeps no flag", "", "https://github.com/bar", []string{"https://github.com/bar"}},
		{"no url no profile", "", "", nil},
		{"profile only no url", "Profile 1", "", []string{"--profile-directory=Profile 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := launchArgs(tt.profileDir, tt.url)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("launchArgs(%q, %q) = %v, want %v", tt.profileDir, tt.url, got, tt.want)
			}
		})
	}
}

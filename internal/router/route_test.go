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
	source     string // injected originating app (§5.4); "" = undeterminable.
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

	origStart, origNotify, origSource := startProcess, notifyError, resolveSource
	startProcess = func(path string, args ...string) error {
		h.launched = true
		h.gotPath = path
		h.gotArgs = args
		return h.launchErr
	}
	notifyError = func(string, string) { h.notified = true }
	// Stub the source lookup so Route is hermetic and never depends on the real
	// process tree; tests that exercise source matching set h.source.
	resolveSource = func() string { return h.source }
	t.Cleanup(func() { startProcess, notifyError, resolveSource = origStart, origNotify, origSource })
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

func TestRouteIncognitoRuleAddsFlag(t *testing.T) {
	// A matched rule with "incognito":true launches --incognito alongside the
	// profile flag (ordered profile → incognito → url).
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"x\\.com","profile_directory":"Profile 1","incognito":true}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://x.com/foo"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "--incognito", "https://x.com/foo"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteIncognitoNoProfile(t *testing.T) {
	// Incognito with no profile is just --incognito <url>.
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"x\\.com","incognito":true}]`)

	if err := Route("https://x.com/foo"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--incognito", "https://x.com/foo"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteSourceRuleMatchesWhenAppMatches(t *testing.T) {
	// End-to-end: the injected source (§5.4) satisfies a source-only rule, so the
	// click routes to that profile regardless of the URL.
	h := newRouteHarness(t)
	h.source = "Slack.exe"
	h.writeConfig(t, `[{"id":"1","enabled":true,"source":"slack","profile_directory":"Profile 1"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://anything.example/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "https://anything.example/x"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteSourceRuleFailsOpenWhenUndeterminable(t *testing.T) {
	// When the source can't be resolved (""), the source rule's predicate is
	// unsatisfied and the click still routes — to Chrome default here (§5.4).
	h := newRouteHarness(t)
	h.source = "" // undeterminable
	h.writeConfig(t, `[{"id":"1","enabled":true,"source":"slack","profile_directory":"Profile 1"}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work"}}}}`)

	if err := Route("https://example.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if want := []string{"https://example.com/x"}; !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v (undeterminable source must fail open to Chrome default)", h.gotArgs, want)
	}
}

func TestResolveIncognitoSurvivesDroppedProfile(t *testing.T) {
	// Incognito is independent of the profile fallback: a vanished profile drops to
	// Chrome default but the rule still requests a private window.
	h := newRouteHarness(t)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Default":{"name":"P"}}}}`)

	cfg := &config.Config{Version: 1, Rules: []config.Rule{
		{ID: "r", Enabled: true, Pattern: `x\.com`, ProfileDirectory: "Profile 7", Incognito: true}, // profile not in Local State
	}}
	got := Resolve(cfg, "https://x.com/foo", "")
	if !got.ProfileDropped || got.ProfileDirectory != "" {
		t.Errorf("missing profile should drop: dropped=%v dir=%q", got.ProfileDropped, got.ProfileDirectory)
	}
	if !got.Incognito {
		t.Error("incognito should survive a dropped profile")
	}
}

func TestRouteAccountMatchResolvesToProfile(t *testing.T) {
	// A rule bound by account email (#22) resolves to the current profile
	// directory at route time and launches with that flag.
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":"acme\\.com","profile_match":{"email":"joe@acme.com"}}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Default":{"name":"P"},"Profile 1":{"name":"Work","user_name":"joe@acme.com","hosted_domain":"acme.com"}}}}`)

	if err := Route("https://acme.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "https://acme.com/x"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteAccountMatchByHostedDomain(t *testing.T) {
	// Binding by Workspace hosted domain resolves to the profile with that domain.
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":".","profile_match":{"hosted_domain":"acme.com"}}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 3":{"name":"Work","user_name":"joe@acme.com","hosted_domain":"acme.com"}}}}`)

	if err := Route("https://example.com/x"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 3", "https://example.com/x"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v", h.gotArgs, want)
	}
}

func TestRouteAccountMatchOverridesDirectory(t *testing.T) {
	// When a rule sets both, the account binding wins over profile_directory (#22).
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":".","profile_directory":"Profile 9","profile_match":{"email":"joe@acme.com"}}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work","user_name":"joe@acme.com"}}}}`)

	if err := Route("https://x.test/"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{"--profile-directory=Profile 1", "https://x.test/"}
	if !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v (account should override directory)", h.gotArgs, want)
	}
}

func TestRouteUnresolvableAccountFallsBackToDefault(t *testing.T) {
	// §10/#22: an account that resolves to no current profile must fail closed to
	// Chrome default, just like a vanished profile_directory.
	h := newRouteHarness(t)
	h.writeConfig(t, `[{"id":"1","enabled":true,"pattern":".","profile_match":{"email":"gone@acme.com"}}]`)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work","user_name":"joe@acme.com"}}}}`)

	if err := Route("https://x.test/"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if want := []string{"https://x.test/"}; !reflect.DeepEqual(h.gotArgs, want) {
		t.Errorf("args = %v, want %v (unresolvable account should drop the flag)", h.gotArgs, want)
	}
}

func TestResolveUnresolvableAccountMarksDropped(t *testing.T) {
	// The editor preview (Resolve) must mark the matched-but-unresolvable account
	// as dropped, so it previews as Chrome default exactly as a real click routes.
	h := newRouteHarness(t)
	h.writeLocalState(t, `{"profile":{"info_cache":{"Profile 1":{"name":"Work","user_name":"joe@acme.com"}}}}`)

	cfg := &config.Config{Version: 1, Rules: []config.Rule{
		{ID: "r", Enabled: true, Pattern: `x\.com`, ProfileMatch: &config.ProfileMatch{Email: "gone@acme.com"}},
	}}
	got := Resolve(cfg, "https://x.com/foo", "")
	if got.Rule == nil || got.Rule.ID != "r" {
		t.Fatalf("expected rule r to match, got %+v", got.Rule)
	}
	if !got.ProfileDropped || got.ProfileDirectory != "" {
		t.Errorf("unresolvable account should drop to Chrome default: dropped=%v dir=%q", got.ProfileDropped, got.ProfileDirectory)
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
	got := Resolve(cfg, "https://x.com/foo", "")
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
	got := Resolve(cfg, "https://x.com/foo", "")
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
	got := Resolve(cfg, "https://x.com/foo", "")
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
		incognito  bool
		url        string
		want       []string
	}{
		{"profile and url", "Profile 3", false, "https://github.com/foo", []string{"--profile-directory=Profile 3", "https://github.com/foo"}},
		{"no match keeps no flag", "", false, "https://github.com/bar", []string{"https://github.com/bar"}},
		{"no url no profile", "", false, "", nil},
		{"profile only no url", "Profile 1", false, "", []string{"--profile-directory=Profile 1"}},
		// §10: --incognito is a distinct argv entry, ordered profile → incognito → url.
		{"incognito with profile", "Profile 3", true, "https://x.com/foo", []string{"--profile-directory=Profile 3", "--incognito", "https://x.com/foo"}},
		{"incognito no profile", "", true, "https://x.com/foo", []string{"--incognito", "https://x.com/foo"}},
		{"incognito no url", "", true, "", []string{"--incognito"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := launchArgs(tt.profileDir, tt.incognito, tt.url)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("launchArgs(%q, %v, %q) = %v, want %v", tt.profileDir, tt.incognito, tt.url, got, tt.want)
			}
		})
	}
}

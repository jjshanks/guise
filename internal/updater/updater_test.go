package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false}, // same
		{"v1.2.3", "v1.2.2", false}, // older
		{"v1.2.3", "v1.1.9", false},
		{"v1.10.0", "v1.9.0", false}, // numeric, not lexical, compare
		{"v1.9.0", "v1.10.0", true},
		// Development builds opt out: never reported as upgradable, even to a
		// higher tag, because they have no clean release version to compare.
		{"dev", "v9.9.9", false},
		{"v0.0.0-dev+abc1234", "v9.9.9", false},
		{"v1.2.3-5-gabc1234", "v9.9.9", false},
		// A non-release "latest" (shouldn't happen via /releases/latest) is ignored.
		{"v1.2.3", "v1.2.4-rc1", false},
		{"v1.2.3", "garbage", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestIsReleaseBuild(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v0.1.0", "v10.20.30"} {
		if !IsReleaseBuild(v) {
			t.Errorf("IsReleaseBuild(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"dev", "", "1.2.3", "v1.2", "v1.2.3-rc1", "v0.0.0-dev+abc"} {
		if IsReleaseBuild(v) {
			t.Errorf("IsReleaseBuild(%q) = true, want false", v)
		}
	}
}

func TestParseSHA(t *testing.T) {
	good := "abc123" + "0000000000000000000000000000000000000000000000000000000000"[:58]
	if len(good) != 64 {
		t.Fatalf("test digest is %d chars, want 64", len(good))
	}
	if got, err := parseSHA(good + "  guise.exe\n"); err != nil || got != good {
		t.Errorf("parseSHA(valid) = %q, %v; want %q, nil", got, err, good)
	}
	for _, bad := range []string{"", "   ", "xyz  guise.exe", "GGGG" + good[4:] + "  guise.exe"} {
		if _, err := parseSHA(bad); err == nil {
			t.Errorf("parseSHA(%q) = nil error, want error", bad)
		}
	}
	// Uppercase hex is normalized to lowercase.
	up := "ABCDEF" + good[6:]
	if got, _ := parseSHA(up); got == up {
		t.Errorf("parseSHA did not lowercase: got %q", got)
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent header")
		}
		w.Write([]byte(`{
			"tag_name": "v2.0.0",
			"html_url": "https://example.test/releases/v2.0.0",
			"prerelease": false,
			"assets": [
				{"name": "guise.exe", "browser_download_url": "https://example.test/guise.exe", "size": 123}
			]
		}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Owner: "owner", Repo: "repo"}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.TagName != "v2.0.0" {
		t.Errorf("TagName = %q, want v2.0.0", rel.TagName)
	}
	if a, ok := rel.asset("guise.exe"); !ok || a.Size != 123 {
		t.Errorf("asset guise.exe = %+v, ok=%v", a, ok)
	}
}

func TestLatestNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Owner: "o", Repo: "r"}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Error("expected error on 404, got nil")
	}
}

func TestDownloadVerifies(t *testing.T) {
	payload := []byte("pretend this is guise.exe")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/guise.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/guise.exe.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hexSum + "  guise.exe\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		TagName: "v2.0.0",
		Assets: []Asset{
			{Name: "guise.exe", URL: srv.URL + "/guise.exe"},
			{Name: "guise.exe.sha256", URL: srv.URL + "/guise.exe.sha256"},
		},
	}
	c := &Client{HTTP: srv.Client()}
	dir := t.TempDir()

	path, err := c.Download(context.Background(), rel, dir)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if want := filepath.Join(dir, "guise.exe.new"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded content mismatch")
	}
}

func TestDownloadRejectsCorruptAsset(t *testing.T) {
	payload := []byte("the real bytes")
	wrong := sha256.Sum256([]byte("different bytes"))
	wrongHex := hex.EncodeToString(wrong[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/guise.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/guise.exe.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(wrongHex + "  guise.exe\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		TagName: "v2.0.0",
		Assets: []Asset{
			{Name: "guise.exe", URL: srv.URL + "/guise.exe"},
			{Name: "guise.exe.sha256", URL: srv.URL + "/guise.exe.sha256"},
		},
	}
	c := &Client{HTTP: srv.Client()}
	dir := t.TempDir()

	if _, err := c.Download(context.Background(), rel, dir); err == nil {
		t.Fatal("expected sha256 mismatch error, got nil")
	}
	// A rejected download must leave nothing behind to install.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("download dir not clean after rejection: %v", entries)
	}
}

func TestDownloadRejectsOversizedAsset(t *testing.T) {
	// The server streams more bytes than the release metadata declares for the
	// asset; the download must stop at the cap and fail, leaving nothing behind.
	payload := bytes.Repeat([]byte("A"), 64)
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/guise.exe", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/guise.exe.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hexSum + "  guise.exe\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		TagName: "v2.0.0",
		Assets: []Asset{
			{Name: "guise.exe", URL: srv.URL + "/guise.exe", Size: 8},
			{Name: "guise.exe.sha256", URL: srv.URL + "/guise.exe.sha256"},
		},
	}
	c := &Client{HTTP: srv.Client()}
	dir := t.TempDir()

	if _, err := c.Download(context.Background(), rel, dir); err == nil {
		t.Fatal("expected error for asset larger than its declared size, got nil")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("download dir not clean after rejection: %v", entries)
	}
}

func TestDownloadRejectsDisallowedHost(t *testing.T) {
	// NewClient installs the GitHub host allowlist; an asset URL pointing
	// elsewhere must be rejected before any request is made.
	rel := &Release{
		TagName: "v2.0.0",
		Assets: []Asset{
			{Name: "guise.exe", URL: "https://evil.example/guise.exe"},
			{Name: "guise.exe.sha256", URL: "https://evil.example/guise.exe.sha256"},
		},
	}
	if _, err := NewClient().Download(context.Background(), rel, t.TempDir()); err == nil {
		t.Error("expected error for a non-GitHub asset host, got nil")
	}
}

func TestCheckGitHubHost(t *testing.T) {
	for _, ok := range []string{"github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"} {
		if err := checkGitHubHost(ok); err != nil {
			t.Errorf("checkGitHubHost(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"evil.example", "github.com.evil.example", "xgithubusercontent.com", "githubusercontent.com", "notgithub.com", ""} {
		if err := checkGitHubHost(bad); err == nil {
			t.Errorf("checkGitHubHost(%q) = nil, want error", bad)
		}
	}
}

func TestDownloadMissingAsset(t *testing.T) {
	rel := &Release{TagName: "v2.0.0"} // no assets
	c := NewClient()
	if _, err := c.Download(context.Background(), rel, t.TempDir()); err == nil {
		t.Error("expected error for release with no assets, got nil")
	}
}

func TestOldPath(t *testing.T) {
	if got := oldPath("/x/guise.exe"); got != "/x/guise.exe.old" {
		t.Errorf("oldPath = %q", got)
	}
	if got := newPath("/x/guise.exe"); got != "/x/guise.exe.new" {
		t.Errorf("newPath = %q", got)
	}
}

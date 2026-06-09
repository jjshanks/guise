// Package updater checks GitHub for newer guise releases and downloads them
// (§14). It lives only in TRAY mode: ROUTE is the stateless hot path and must
// never touch the network, so the update check runs in the one long-lived
// process instead.
//
// The flow is check → download → verify → (on the user's click) apply:
// Latest queries the GitHub Releases API, IsNewer compares the running build's
// version against the published tag, and Download fetches the guise.exe asset
// and verifies it against the release's published SHA256 before it is ever a
// candidate to replace the running binary. The Windows-only Apply step (see
// apply_windows.go) does the rename-in-place swap.
//
// This file is deliberately pure (no build tag, no Win32) so the comparison,
// HTTP, and verification logic stays testable on every platform, per the
// platform-split convention in CLAUDE.md.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repo coordinates and asset names. The release workflow publishes exactly
// these two assets per tag (see .github/workflows/release.yml), so the updater
// looks for them by name.
const (
	DefaultOwner = "jjshanks"
	DefaultRepo  = "guise"

	exeAssetName = "guise.exe"
	shaAssetName = "guise.exe.sha256"

	apiBaseDefault = "https://api.github.com"
	// userAgent is required: GitHub rejects API requests without one.
	userAgent = "guise-updater"

	// maxExeBytes is a hard ceiling on the binary download, regardless of what
	// the release metadata claims, so a malformed or malicious release can't
	// fill the disk before the SHA check rejects it. guise.exe is a few MB;
	// 64 MB is generous headroom.
	maxExeBytes = 64 << 20
)

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is the slice of the GitHub Releases API response we use.
type Release struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// asset returns the named asset attached to the release.
func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Client talks to the GitHub Releases API. The zero value is not usable; call
// NewClient. BaseURL and the HTTP client are fields so tests can point at an
// httptest server.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Owner   string
	Repo    string
	// CheckHost, when non-nil, vets the host of every asset URL before it is
	// fetched. Asset URLs come verbatim from the release JSON, so this is what
	// keeps a tampered release from pointing the updater at an arbitrary
	// server. NewClient installs the GitHub allowlist; tests that point at an
	// httptest server leave it nil.
	CheckHost func(host string) error
}

// NewClient returns a Client pointed at the public GitHub API for this repo,
// with timeouts so a hung network can never wedge the tray, and with asset
// downloads (including every redirect hop — github.com 302s assets to a
// storage host) restricted to GitHub-operated hosts.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				return checkGitHubHost(req.URL.Hostname())
			},
		},
		BaseURL:   apiBaseDefault,
		Owner:     DefaultOwner,
		Repo:      DefaultRepo,
		CheckHost: checkGitHubHost,
	}
}

// checkGitHubHost rejects hosts GitHub release traffic never uses. Combined
// with TLS and the SHA256 check, the download can only deliver bytes that
// GitHub-operated hosts served for this release.
func checkGitHubHost(host string) error {
	if host == "github.com" || host == "api.github.com" ||
		strings.HasSuffix(host, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("unexpected download host %q", host)
}

// checkHost applies the client's host allowlist, if any, to a URL about to be
// fetched.
func (c *Client) checkHost(u *url.URL) error {
	if c.CheckHost == nil {
		return nil
	}
	return c.CheckHost(u.Hostname())
}

// Latest returns the newest *stable* release. It uses the /releases/latest
// endpoint, which GitHub defines to exclude pre-releases and drafts — so a
// hyphenated tag like v1.2.3-rc1 never surfaces here (§14, "stable only").
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.BaseURL, c.Owner, c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s for latest release", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	return &rel, nil
}

// Download fetches the release's guise.exe asset into destDir, verifies it
// against the release's published SHA256 asset, and returns the path to the
// verified file (named guise.exe.new so it can never clobber a running
// guise.exe). destDir must be the directory holding the running exe, so the
// later Apply rename is within one filesystem. A SHA mismatch is a hard error:
// the partial download is removed and nothing is left to install.
func (c *Client) Download(ctx context.Context, rel *Release, destDir string) (string, error) {
	exeAsset, ok := rel.asset(exeAssetName)
	if !ok {
		return "", fmt.Errorf("release %s has no %s asset", rel.TagName, exeAssetName)
	}
	shaAsset, ok := rel.asset(shaAssetName)
	if !ok {
		return "", fmt.Errorf("release %s has no %s asset", rel.TagName, shaAssetName)
	}

	want, err := c.fetchSHA(ctx, shaAsset)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(destDir, "guise-download-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating download temp: %w", err)
	}
	tmpName := tmp.Name()
	// Remove the temp on any error path; on success it is renamed away first so
	// the remove becomes a no-op. closed tracks the explicit Close on the
	// success path so the file is never closed twice.
	closed := false
	cleanup := true
	defer func() {
		if !closed {
			tmp.Close()
		}
		if cleanup {
			os.Remove(tmpName)
		}
	}()

	// Cap the download at the size the release metadata declares (an asset
	// that serves more than it claims is itself anomalous), under the hard
	// maxExeBytes ceiling.
	limit := int64(maxExeBytes)
	if exeAsset.Size > 0 && exeAsset.Size < limit {
		limit = exeAsset.Size
	}
	got, err := c.downloadTo(ctx, exeAsset.URL, tmp, limit)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("sha256 mismatch for %s: got %s, want %s", exeAssetName, got, want)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing download: %w", err)
	}
	closed = true

	final := newPath(filepath.Join(destDir, exeAssetName))
	if err := os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("finalizing download: %w", err)
	}
	cleanup = false
	return final, nil
}

// downloadTo streams the asset at url into w while hashing it, returning the
// lowercase hex SHA256 of what was written. A response longer than maxBytes is
// a hard error: the copy stops there rather than streaming an unbounded body
// to disk.
func (c *Client) downloadTo(ctx context.Context, assetURL string, w io.Writer, maxBytes int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}
	if err := c.checkHost(req.URL); err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", exeAssetName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github returned %s downloading %s", resp.Status, exeAssetName)
	}

	h := sha256.New()
	// Read one byte past the cap so an over-long body is distinguishable from
	// one that is exactly maxBytes.
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("writing download: %w", err)
	}
	if n > maxBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", exeAssetName, maxBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchSHA downloads and parses the .sha256 asset, returning the expected
// lowercase hex digest. The file format (from the release workflow) is
// "<hex>  guise.exe" — we take the first field.
func (c *Client) fetchSHA(ctx context.Context, a Asset) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	if err := c.checkHost(req.URL); err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github returned %s for checksum", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("reading checksum: %w", err)
	}
	return parseSHA(string(body))
}

// parseSHA extracts and validates the leading 64-char hex digest from a
// checksum file's contents.
func parseSHA(s string) (string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	sum := strings.ToLower(fields[0])
	if len(sum) != 64 {
		return "", fmt.Errorf("malformed checksum %q", fields[0])
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", fmt.Errorf("non-hex checksum %q", fields[0])
	}
	return sum, nil
}

var versionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// parseVersion accepts only a clean release tag, vMAJOR.MINOR.PATCH.
func parseVersion(v string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// IsReleaseBuild reports whether v is a clean release tag (vMAJOR.MINOR.PATCH).
// Anything else — the "dev" default, a `git describe` "ahead of tag" string
// like v1.2.3-5-gabc1234, or a build with +metadata — is a development build
// that opts out of auto-update (§14), so the tray neither nags it nor calls the
// API on its behalf.
func IsReleaseBuild(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// IsNewer reports whether latest is a strictly higher release than current.
// Both must be clean release tags; if current is a development build (see
// IsReleaseBuild) the answer is always false, so a dev build is never told to
// "update" to an older published tag.
func IsNewer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// oldPath is the name the running exe is moved aside to during Apply. It lives
// here (not in apply_windows.go) so it is covered by the cross-platform tests.
func oldPath(exe string) string { return exe + ".old" }

// newPath is the name a verified download is staged under next to exe, both by
// Download and by CleanupOld when it sweeps an orphaned one.
func newPath(exe string) string { return exe + ".new" }

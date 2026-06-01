package version

import (
	"strings"
	"testing"
)

// withStamp sets the link-time vars for one test and restores them after, so
// each case sees a known state regardless of how the test binary was built.
func withStamp(t *testing.T, ver, commit, date string) {
	t.Helper()
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })
	Version, Commit, Date = ver, commit, date
}

func TestShortReturnsBareVersion(t *testing.T) {
	withStamp(t, "v1.2.3", "abc1234def", "2026-05-31T12:00:00Z")
	if got := Short(); got != "v1.2.3" {
		t.Errorf("Short() = %q, want %q", got, "v1.2.3")
	}
}

func TestStringFullyStamped(t *testing.T) {
	withStamp(t, "v1.2.3", "abc1234def5678", "2026-05-31T12:00:00Z")
	got := String()
	want := "guise v1.2.3 (abc1234, 2026-05-31T12:00:00Z)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringTruncatesCommit(t *testing.T) {
	// A full 40-char SHA must be abbreviated to 7 chars in the output.
	withStamp(t, "v0.1.0", "0123456789abcdef0123456789abcdef01234567", "")
	got := String()
	if !strings.Contains(got, "(0123456)") {
		t.Errorf("String() = %q, want it to contain the 7-char commit %q", got, "0123456")
	}
}

func TestStringShortCommitUntouched(t *testing.T) {
	// A commit already <=7 chars is passed through verbatim.
	withStamp(t, "v0.1.0", "abc12", "")
	got := String()
	if !strings.Contains(got, "(abc12)") {
		t.Errorf("String() = %q, want it to contain %q", got, "abc12")
	}
}

func TestStringDateOnly(t *testing.T) {
	withStamp(t, "dev", "", "2026-05-31T12:00:00Z")
	got := String()
	want := "guise dev (2026-05-31T12:00:00Z)"
	// Commit may be backfilled from build info in some environments; only assert
	// the date is rendered when present.
	if !strings.Contains(got, "2026-05-31T12:00:00Z") {
		t.Errorf("String() = %q, want it to contain the date; want like %q", got, want)
	}
}

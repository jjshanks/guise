//go:build windows

package editor

import (
	"testing"

	"guise/internal/chrome"
	"guise/internal/config"
)

func TestProfileOptionDirsUnion(t *testing.T) {
	profiles := []chrome.Profile{{Directory: "Default"}, {Directory: "Profile 1"}}
	rules := []config.Rule{
		{ProfileDirectory: "Profile 1"}, // already discovered → not duplicated
		{ProfileDirectory: "Profile 5"}, // missing from Local State → still offered
		{ProfileDirectory: ""},          // Chrome default → not an option
	}
	got := profileOptionDirs(profiles, rules)
	want := []string{"Default", "Profile 1", "Profile 5"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("option %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestProfileComboRoundTripMissingProfile guards the data-loss regression: a
// rule whose profile is absent from Local State must keep a stable combo index
// so editing another field on its row does not reset the profile to "Chrome
// default". profileForComboIndex/comboIndexForProfile read only profileOptions,
// so this needs no walk widgets.
func TestProfileComboRoundTripMissingProfile(t *testing.T) {
	w := &window{
		profiles:       nil, // Local State unreadable.
		profileOptions: profileOptionDirs(nil, []config.Rule{{ProfileDirectory: "Profile 3"}}),
	}
	idx := w.comboIndexForProfile("Profile 3")
	if idx == 0 {
		t.Fatal("missing profile mapped to the Chrome-default sentinel; an edit would wipe it")
	}
	if got := w.profileForComboIndex(idx); got != "Profile 3" {
		t.Errorf("round trip: got %q, want Profile 3", got)
	}
	// The empty value still maps to the sentinel at index 0.
	if got := w.comboIndexForProfile(""); got != 0 {
		t.Errorf("empty profile should map to sentinel index 0, got %d", got)
	}
}

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

// TestAccountOptionsUnion proves the by-account dropdown (#22) lists signed-in
// discovered profiles and seeds any account a rule already binds to that
// discovery did not return, so an existing profile_match round-trips.
func TestAccountOptionsUnion(t *testing.T) {
	profiles := []chrome.Profile{
		{Directory: "Default", Email: "joe@acme.com", Name: "Work", HostedDomain: "acme.com"},
		{Directory: "Profile 1"}, // signed out → no account option.
	}
	rules := []config.Rule{
		{ProfileMatch: &config.ProfileMatch{Email: "joe@acme.com"}},  // already discovered → not duplicated.
		{ProfileMatch: &config.ProfileMatch{Email: "gone@acme.com"}}, // removed account → still offered.
		{ProfileDirectory: "Profile 1"},                              // directory-bound → contributes nothing.
	}
	got := accountOptions(profiles, rules)
	if len(got) != 2 {
		t.Fatalf("got %d account options, want 2: %+v", len(got), got)
	}
	if got[0].match.Email != "joe@acme.com" {
		t.Errorf("option 0 = %+v, want joe@acme.com", got[0].match)
	}
	if got[1].match.Email != "gone@acme.com" {
		t.Errorf("option 1 = %+v, want the removed account seeded from the rule", got[1].match)
	}
}

// TestAccountComboRoundTrip proves a rule's ProfileMatch maps to a stable combo
// index and back, and that an absent match selects the sentinel at index 0.
func TestAccountComboRoundTrip(t *testing.T) {
	w := &window{accountOpts: accountOptions(
		[]chrome.Profile{{Directory: "Default", Email: "joe@acme.com"}},
		[]config.Rule{{ProfileMatch: &config.ProfileMatch{HostedDomain: "corp.example"}}},
	)}

	idx := w.accountComboIndex(&config.ProfileMatch{Email: "JOE@ACME.COM"}) // case-insensitive.
	if idx == 0 {
		t.Fatal("known account mapped to the sentinel; an edit would drop the binding")
	}
	if m := w.matchForAccountIndex(idx); m.IsZero() || m.Email != "joe@acme.com" {
		t.Errorf("round trip: got %+v, want joe@acme.com", m)
	}
	// A rule-seeded hosted-domain match also round-trips.
	if i := w.accountComboIndex(&config.ProfileMatch{HostedDomain: "corp.example"}); i == 0 {
		t.Error("seeded hosted-domain match should have a stable index")
	}
	// No match → sentinel; sentinel → nil.
	if i := w.accountComboIndex(nil); i != 0 {
		t.Errorf("absent match should map to sentinel index 0, got %d", i)
	}
	if m := w.matchForAccountIndex(0); m != nil {
		t.Errorf("sentinel index 0 should map to nil match, got %+v", m)
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

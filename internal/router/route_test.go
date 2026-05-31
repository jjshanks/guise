package router

import (
	"reflect"
	"testing"
)

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

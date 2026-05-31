//go:build !windows

package chrome

// appPathsChrome has no meaning off Windows; this stub lets the pure profile
// and fallback logic compile and be tested on any platform.
func appPathsChrome() string { return "" }

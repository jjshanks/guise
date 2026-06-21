//go:build !windows

package source

// snapshot has no meaning off Windows; this stub lets the pure tree-walk and
// broker-skipping logic compile and be tested on any platform. A nil table makes
// Current return "", i.e. an undeterminable source (fail open, §5.4).
func snapshot() map[uint32]procInfo { return nil }

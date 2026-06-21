// Package source resolves the application that originated a click so a routing
// rule can match on it (§5.4, #16). ROUTE is a fresh, short-lived process per
// click (§2): when Windows launches guise.exe <url>, the parent process is the
// app that opened the link (or an OS shell broker), so the source must be
// derived from the live process tree, never from any resident state.
//
// The tree-walk and broker-skipping logic lives here, cross-platform and
// table-tested; only the snapshot of the live process table is Win32 (see
// source_windows.go / source_other.go), per the platform-split convention.
package source

import (
	"os"
	"strings"
)

// procInfo is one row of the process table: a process's parent PID and its
// image base name (e.g. "slack.exe").
type procInfo struct {
	ppid uint32
	name string
}

// maxDepth bounds the walk up the process tree. Clicks arrive at most a few
// levels below the originating app even through a broker chain; the bound also
// guarantees termination if PID reuse ever produces a cycle.
const maxDepth = 8

// brokers are OS launcher/broker image names that relay a click without being
// its true origin (§5.4). When one of these is an ancestor we keep walking up
// rather than reporting it as the source. Lower-cased for case-insensitive
// comparison (Windows process names are case-insensitive).
var brokers = map[string]bool{
	"explorer.exe":             true, // shell, opens links from many places
	"applicationframehost.exe": true, // hosts UWP windows
	"runtimebroker.exe":        true, // brokers UWP capability access
	"svchost.exe":              true, // generic service host
	"dllhost.exe":              true, // COM surrogate
	"sihost.exe":               true, // shell infrastructure host
	"openwith.exe":             true, // the "open with" picker
	"guise.exe":                true, // our own launcher chain, never the origin
}

// Current returns the image name of the application that originated this click,
// or "" when it cannot be determined. It is best-effort and fails open: an
// empty result simply leaves any rule's source predicate unsatisfied so matching
// continues (§5.4), never blocking the click. The Win32 snapshot lives in
// source_windows.go; off Windows it is empty, so Current returns "".
func Current() string {
	return walk(snapshot(), uint32(os.Getpid()))
}

// walk climbs the process tree from start (this process's PID) toward the root,
// returning the first ancestor whose image name is not a known broker. It is
// pure so the broker-skipping logic is testable on any platform.
//
// PIDs can be reused and a parent may have already exited, so a missing entry or
// a self-referential PID terminates the walk with "" — best-effort by design.
func walk(procs map[uint32]procInfo, start uint32) string {
	cur := start
	for depth := 0; depth < maxDepth; depth++ {
		info, ok := procs[cur]
		if !ok || info.ppid == cur {
			return "" // unknown PID or self-parent (e.g. the idle/root process)
		}
		parent, ok := procs[info.ppid]
		if !ok {
			return "" // parent already gone — source undeterminable
		}
		if !isBroker(parent.name) {
			return parent.name
		}
		cur = info.ppid // broker relayed the click; keep climbing
	}
	return ""
}

// isBroker reports whether name is a known OS launcher/broker that relays a
// click without being its origin. Case-insensitive on the base image name.
func isBroker(name string) bool {
	return brokers[strings.ToLower(name)]
}

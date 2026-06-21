package source

import "testing"

// procTree is a tiny helper to build a process table for walk.
func procTree(rows map[uint32]procInfo) map[uint32]procInfo { return rows }

func TestWalkReturnsImmediateParent(t *testing.T) {
	// 100 (us) → 50 (slack.exe): the immediate non-broker parent is the source.
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 50, name: "guise.exe"},
		50:  {ppid: 1, name: "slack.exe"},
		1:   {ppid: 1, name: "system"},
	})
	if got := walk(procs, 100); got != "slack.exe" {
		t.Errorf("walk = %q, want slack.exe", got)
	}
}

func TestWalkSkipsBrokers(t *testing.T) {
	// 100 (us) → 80 (explorer.exe, a broker) → 50 (slack.exe). The broker is
	// relaying the click, so the source is the app above it.
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 80, name: "guise.exe"},
		80:  {ppid: 50, name: "explorer.exe"},
		50:  {ppid: 1, name: "slack.exe"},
		1:   {ppid: 1, name: "system"},
	})
	if got := walk(procs, 100); got != "slack.exe" {
		t.Errorf("walk should skip the broker: got %q, want slack.exe", got)
	}
}

func TestWalkSkipsChainedBrokers(t *testing.T) {
	// Several brokers in a row are all skipped until a real origin is found.
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 90, name: "guise.exe"},
		90:  {ppid: 80, name: "RuntimeBroker.exe"},
		80:  {ppid: 70, name: "ApplicationFrameHost.exe"},
		70:  {ppid: 50, name: "svchost.exe"},
		50:  {ppid: 1, name: "Teams.exe"},
		1:   {ppid: 1, name: "system"},
	})
	if got := walk(procs, 100); got != "Teams.exe" {
		t.Errorf("walk should skip chained brokers: got %q, want Teams.exe", got)
	}
}

func TestWalkUndeterminableWhenParentMissing(t *testing.T) {
	// The parent PID is not in the table (parent already exited): fail open with "".
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 50, name: "guise.exe"},
	})
	if got := walk(procs, 100); got != "" {
		t.Errorf("missing parent should yield \"\", got %q", got)
	}
}

func TestWalkUndeterminableWhenAllBrokers(t *testing.T) {
	// Nothing but brokers up to the root: no real origin, so "" (fail open).
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 80, name: "guise.exe"},
		80:  {ppid: 1, name: "explorer.exe"},
		1:   {ppid: 1, name: "svchost.exe"},
	})
	if got := walk(procs, 100); got != "" {
		t.Errorf("all-broker chain should yield \"\", got %q", got)
	}
}

func TestWalkSelfParentTerminates(t *testing.T) {
	// A self-referential PID (the idle/root process) must terminate the walk
	// rather than loop forever.
	procs := procTree(map[uint32]procInfo{
		100: {ppid: 100, name: "guise.exe"},
	})
	if got := walk(procs, 100); got != "" {
		t.Errorf("self-parent should yield \"\", got %q", got)
	}
}

func TestWalkUnknownStartPID(t *testing.T) {
	if got := walk(map[uint32]procInfo{}, 100); got != "" {
		t.Errorf("unknown start PID should yield \"\", got %q", got)
	}
}

func TestWalkDepthBound(t *testing.T) {
	// A chain of brokers longer than maxDepth must terminate at "" rather than
	// run unbounded, even though a real origin sits just past the bound.
	procs := map[uint32]procInfo{}
	const start = 1000
	pid := uint32(start)
	for i := 0; i < maxDepth+3; i++ {
		procs[pid] = procInfo{ppid: pid + 1, name: "explorer.exe"}
		pid++
	}
	procs[pid] = procInfo{ppid: pid, name: "slack.exe"} // origin, but beyond the bound
	if got := walk(procs, start); got != "" {
		t.Errorf("walk should stop at the depth bound: got %q, want \"\"", got)
	}
}

func TestIsBrokerCaseInsensitive(t *testing.T) {
	for _, name := range []string{"explorer.exe", "EXPLORER.EXE", "Explorer.Exe", "svchost.exe"} {
		if !isBroker(name) {
			t.Errorf("isBroker(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"slack.exe", "chrome.exe", "Teams.exe", ""} {
		if isBroker(name) {
			t.Errorf("isBroker(%q) = true, want false", name)
		}
	}
}

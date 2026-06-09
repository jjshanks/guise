// Package applog wires the standard logger to guise.log (§9). The log is
// the primary debugging surface: when a link opens in the "wrong" profile, the
// log shows exactly which rule won.
package applog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"guise/internal/config"
)

// maxLogSize keeps the log small (§9): once it exceeds this, the current file
// is rotated to guise.log.1 (overwriting any previous one) and a fresh
// file starts.
const maxLogSize = 512 * 1024

// Path returns the full path to guise.log.
func Path() string {
	return filepath.Join(config.Dir(), "guise.log")
}

// Setup points the standard logger at guise.log, creating the directory
// and rotating an oversized log first. It returns the open file so the caller
// can close it on exit. On failure it leaves logging at its default (stderr)
// and returns the error — logging problems must never be fatal.
func Setup() (*os.File, error) {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}
	path := Path()
	rotateIfLarge(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags) // Date + time; no prefix is set, so Lmsgprefix would be a no-op.
	return f, nil
}

// rotateIfLarge moves an oversized log aside to guise.log.1 (overwriting any
// previous one) so the live file stays small (§9). It is best-effort and safe
// under the occasional concurrent ROUTE process (a double-click spawns two):
// os.Rename is atomic, so at worst two near-simultaneous clicks each rotate once
// and the large content still lands in guise.log.1 — and because every log line
// is emitted as a single O_APPEND write, concurrent writers interleave whole
// lines but never corrupt one. One caveat: a process that opened the log just
// before another's rotate keeps its handle on the renamed file, so its lines
// land in guise.log.1 instead of the fresh log — they survive, just in the
// rotated file. A cross-process lock would buy nothing here.
func rotateIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	_ = os.Rename(path, path+".1") // A failed rotate must not block logging.
}

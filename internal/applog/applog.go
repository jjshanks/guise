// Package applog wires the standard logger to urlrouter.log (§9). The log is
// the primary debugging surface: when a link opens in the "wrong" profile, the
// log shows exactly which rule won.
package applog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"urlrouter/internal/config"
)

// maxLogSize keeps the log small (§9): once it exceeds this, the current file
// is rotated to urlrouter.log.1 (overwriting any previous one) and a fresh
// file starts.
const maxLogSize = 512 * 1024

// Path returns the full path to urlrouter.log.
func Path() string {
	return filepath.Join(config.Dir(), "urlrouter.log")
}

// Setup points the standard logger at urlrouter.log, creating the directory
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
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	return f, nil
}

func rotateIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	_ = os.Rename(path, path+".1") // Best effort; a failed rotate must not block logging.
}

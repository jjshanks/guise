//go:build windows

package editor

import (
	"os"
	"runtime"
	"testing"

	"guise/internal/config"
)

// TestEditorRewriteWriteBackGUI drives the real walk widgets to prove that a
// rewrite added on the (background) Rewrites tab captures the user's edits — the
// regression behind "rewrites save with empty find/replace". It is opt-in: it
// creates a real top-level window, so it needs an interactive desktop and is
// skipped unless GUISE_EDITOR_IT is set (mirrors the winreg round-trip test).
//
//	$env:GUISE_EDITOR_IT=1; go test ./internal/editor/ -run GUI -v
func TestEditorRewriteWriteBackGUI(t *testing.T) {
	if os.Getenv("GUISE_EDITOR_IT") == "" {
		t.Skip("set GUISE_EDITOR_IT=1 to run the GUI editor write-back test")
	}
	t.Setenv("APPDATA", t.TempDir()) // onSave writes config.json here.
	// walk requires all GUI work on one OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := &window{current: -1, rwCurrent: -1}
	w.cfg = &config.Config{Version: 1, Rules: []config.Rule{}, Rewrites: []config.Rewrite{}}
	w.model = &rulesModel{rules: &w.cfg.Rules, nameFor: w.friendlyName}
	w.rwModel = &rewritesModel{rewrites: &w.cfg.Rewrites}

	afterBuild = func(w *window) {
		defer w.mw.Close()
		// Deliberately do NOT switch to the Rewrites tab first: the table is on the
		// background tab, the exact condition under which SetCurrentIndex used to
		// fail silently and leave rwCurrent at -1 so write-back dropped every edit.
		w.onAddRewrite()
		w.rwFindEd.SetText("x.com") // Fill the detail fields, as the user does.
		w.rwReplaceEd.SetText("xcancel.com")
		w.onSave() // Flushes on-screen edits, then writes config.json atomically.
	}
	defer func() { afterBuild = nil }()

	if err := w.build(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Reload from disk: the saved config must carry the typed values, not the
	// empty Add-time defaults (the reported regression).
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Rewrites) != 1 {
		t.Fatalf("got %d saved rewrites, want 1", len(got.Rewrites))
	}
	if r := got.Rewrites[0]; r.Find != "x.com" || r.Replace != "xcancel.com" {
		t.Fatalf("saved rewrite lost edits: find=%q replace=%q, want x.com / xcancel.com", r.Find, r.Replace)
	}
}

// TestEditorIncognitoWriteBackGUI drives the real walk widgets to prove the
// "Open in incognito" checkbox writes back to the rule and round-trips through a
// save. Opt-in like the rewrite GUI test — it creates a real top-level window.
//
//	$env:GUISE_EDITOR_IT=1; go test ./internal/editor/ -run GUI -v
func TestEditorIncognitoWriteBackGUI(t *testing.T) {
	if os.Getenv("GUISE_EDITOR_IT") == "" {
		t.Skip("set GUISE_EDITOR_IT=1 to run the GUI editor write-back test")
	}
	t.Setenv("APPDATA", t.TempDir()) // onSave writes config.json here.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := &window{current: -1, rwCurrent: -1}
	w.cfg = &config.Config{Version: 1, Rules: []config.Rule{}, Rewrites: []config.Rewrite{}}
	w.model = &rulesModel{rules: &w.cfg.Rules, nameFor: w.friendlyName}
	w.rwModel = &rewritesModel{rewrites: &w.cfg.Rewrites}

	afterBuild = func(w *window) {
		defer w.mw.Close()
		w.onAdd() // selects the new rule in the detail pane.
		w.patternEd.SetText("x.com")
		w.incognitoCB.SetChecked(true)
		w.writeBack() // mirrors the OnCheckedChanged handler.
		w.onSave()
	}
	defer func() { afterBuild = nil }()

	if err := w.build(); err != nil {
		t.Fatalf("build: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Rules) != 1 {
		t.Fatalf("got %d saved rules, want 1", len(got.Rules))
	}
	if r := got.Rules[0]; !r.Incognito || r.Pattern != "x.com" {
		t.Fatalf("saved rule lost edits: pattern=%q incognito=%v, want x.com / true", r.Pattern, r.Incognito)
	}
}

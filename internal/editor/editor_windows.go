//go:build windows

// Package editor implements the rule editor window (§6.2): a table-driven CRUD
// view over the routing rules with reorder, a live-validated pattern field, a
// Chrome-profile dropdown sourced from Local State, a "test URL" field, and an
// atomic save.
package editor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"urlrouter/internal/chrome"
	"urlrouter/internal/config"
	"urlrouter/internal/router"
)

// rulesModel adapts the rule slice to a walk TableView.
type rulesModel struct {
	walk.TableModelBase
	rules   *[]config.Rule
	nameFor func(dir string) string
}

func (m *rulesModel) RowCount() int { return len(*m.rules) }

func (m *rulesModel) Value(row, col int) interface{} {
	r := (*m.rules)[row]
	switch col {
	case 0:
		if r.Enabled {
			return "✓" // check mark
		}
		return ""
	case 1:
		return r.Pattern
	case 2:
		return m.nameFor(r.ProfileDirectory)
	case 3:
		return r.Comment
	}
	return ""
}

type window struct {
	mw       *walk.MainWindow
	cfg      *config.Config
	profiles []chrome.Profile
	model    *rulesModel

	tv         *walk.TableView
	enabledCB  *walk.CheckBox
	patternEd  *walk.LineEdit
	patternErr *walk.Label
	profileCB  *walk.ComboBox
	commentEd  *walk.LineEdit
	chromePath *walk.LineEdit
	testEd     *walk.LineEdit
	testResult *walk.Label
	status     *walk.Label

	current int  // index of the rule shown in the detail pane, or -1.
	loading bool // true while populating widgets, to suppress write-back.
}

// Show opens the editor modally on the calling goroutine and returns when the
// window closes. The caller is responsible for running it on its own OS thread
// (the tray launches it in a fresh goroutine).
func Show() error {
	w := &window{current: -1}

	cfg, err := config.Load()
	if err != nil {
		// Start from default on bad config so the editor can still be used to
		// fix things; the save will replace the broken file.
		cfg = config.Default()
	}
	w.cfg = cfg
	w.profiles, _ = chrome.Profiles() // Best effort; dropdown may be empty.
	w.model = &rulesModel{rules: &w.cfg.Rules, nameFor: w.friendlyName}

	return w.build()
}

func (w *window) build() error {
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "URL Router — Edit Rules",
		MinSize:  d.Size{Width: 720, Height: 480},
		Size:     d.Size{Width: 820, Height: 560},
		Layout:   d.VBox{},
		Children: []d.Widget{
			// Test URL row (§6.2): catches over-broad unanchored patterns.
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.Label{Text: "Test URL:"},
					d.LineEdit{AssignTo: &w.testEd, OnTextChanged: w.onTest},
					d.Label{AssignTo: &w.testResult, Text: "type a URL to see which rule wins"},
				},
			},
			// Rules table + reorder/CRUD buttons.
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.TableView{
						AssignTo:         &w.tv,
						MinSize:          d.Size{Height: 220},
						AlternatingRowBG: true,
						Columns: []d.TableViewColumn{
							{Title: "On", Width: 36},
							{Title: "Pattern", Width: 320},
							{Title: "Profile", Width: 150},
							{Title: "Comment", Width: 200},
						},
						Model:                 w.model,
						OnCurrentIndexChanged: w.onSelect,
					},
					d.Composite{
						Layout: d.VBox{MarginsZero: true},
						Children: []d.Widget{
							d.PushButton{Text: "Add", OnClicked: w.onAdd},
							d.PushButton{Text: "Delete", OnClicked: w.onDelete},
							d.PushButton{Text: "Move ↑", OnClicked: func() { w.onMove(-1) }},
							d.PushButton{Text: "Move ↓", OnClicked: func() { w.onMove(1) }},
							d.VSpacer{},
						},
					},
				},
			},
			// Detail editor for the selected rule.
			d.GroupBox{
				Title:  "Selected rule",
				Layout: d.Grid{Columns: 2},
				Children: []d.Widget{
					d.CheckBox{AssignTo: &w.enabledCB, Text: "Enabled", OnCheckedChanged: w.writeBack, ColumnSpan: 2},
					d.Label{Text: "Pattern (RE2, unanchored, case-sensitive):"},
					d.LineEdit{AssignTo: &w.patternEd, OnTextChanged: w.onPatternChanged},
					d.Label{Text: ""},
					d.Label{AssignTo: &w.patternErr, Text: ""},
					d.Label{Text: "Profile:"},
					d.ComboBox{AssignTo: &w.profileCB, OnCurrentIndexChanged: w.writeBack},
					d.Label{Text: "Comment:"},
					d.LineEdit{AssignTo: &w.commentEd, OnTextChanged: w.writeBack},
				},
			},
			// Chrome path row.
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.Label{Text: "Chrome path (blank = auto):"},
					d.LineEdit{AssignTo: &w.chromePath, Text: w.cfg.ChromePath},
					d.PushButton{Text: "Auto-detect", OnClicked: w.onAutoDetect},
					d.PushButton{Text: "Browse…", OnClicked: w.onBrowse},
				},
			},
			// Save / close row.
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.Label{AssignTo: &w.status, Text: ""},
					d.HSpacer{},
					d.PushButton{Text: "Save", OnClicked: w.onSave},
					d.PushButton{Text: "Close", OnClicked: func() { w.mw.Close() }},
				},
			},
		},
	}.Create()
	if err != nil {
		return err
	}

	w.fillProfileCombo()
	w.populate() // Clears detail pane (nothing selected yet).

	w.mw.Run()
	return nil
}

// friendlyName maps a profile directory to its display name for the table.
func (w *window) friendlyName(dir string) string {
	return friendlyName(w.profiles, dir)
}

// friendlyName maps a profile directory to a human label given the discovered
// profiles. Shared by the editor table and the tray's test dialog.
func friendlyName(profiles []chrome.Profile, dir string) string {
	if dir == "" {
		return "(Chrome default)"
	}
	for _, p := range profiles {
		if p.Directory == dir {
			if p.Name != "" {
				return fmt.Sprintf("%s (%s)", p.Name, dir)
			}
			return dir
		}
	}
	return dir + " (missing)"
}

// fillProfileCombo loads the profile dropdown with friendly names. The model
// strings line up index-for-index with w.profiles.
func (w *window) fillProfileCombo() {
	items := make([]string, 0, len(w.profiles)+1)
	items = append(items, "(Chrome default — no profile)")
	for _, p := range w.profiles {
		items = append(items, w.friendlyName(p.Directory))
	}
	w.profileCB.SetModel(items)
}

// profileForComboIndex maps a combo index back to a profile directory. Index 0
// is the no-profile sentinel.
func (w *window) profileForComboIndex(i int) string {
	if i <= 0 || i-1 >= len(w.profiles) {
		return ""
	}
	return w.profiles[i-1].Directory
}

// comboIndexForProfile finds the combo index for a directory; unknown
// directories map to the no-profile sentinel so the user notices and re-picks.
func (w *window) comboIndexForProfile(dir string) int {
	for i, p := range w.profiles {
		if p.Directory == dir {
			return i + 1
		}
	}
	return 0
}

func (w *window) onSelect() {
	w.current = w.tv.CurrentIndex()
	w.populate()
}

// populate loads the detail widgets from the current rule (or clears them).
func (w *window) populate() {
	w.loading = true
	defer func() { w.loading = false }()

	if w.current < 0 || w.current >= len(w.cfg.Rules) {
		w.enabledCB.SetChecked(false)
		w.patternEd.SetText("")
		w.commentEd.SetText("")
		w.profileCB.SetCurrentIndex(0)
		w.patternErr.SetText("")
		return
	}
	r := w.cfg.Rules[w.current]
	w.enabledCB.SetChecked(r.Enabled)
	w.patternEd.SetText(r.Pattern)
	w.commentEd.SetText(r.Comment)
	w.profileCB.SetCurrentIndex(w.comboIndexForProfile(r.ProfileDirectory))
	w.validatePattern(r.Pattern)
}

// writeBack copies the detail widgets into the current rule and refreshes the
// table row. Suppressed while populating.
func (w *window) writeBack() {
	if w.loading || w.current < 0 || w.current >= len(w.cfg.Rules) {
		return
	}
	r := &w.cfg.Rules[w.current]
	r.Enabled = w.enabledCB.Checked()
	r.Pattern = w.patternEd.Text()
	r.Comment = w.commentEd.Text()
	r.ProfileDirectory = w.profileForComboIndex(w.profileCB.CurrentIndex())
	w.model.PublishRowChanged(w.current)
}

func (w *window) onPatternChanged() {
	w.validatePattern(w.patternEd.Text())
	w.writeBack()
}

// validatePattern flags an invalid RE2 pattern inline (§6.2 live validation).
func (w *window) validatePattern(pattern string) {
	if pattern == "" {
		w.patternErr.SetText("")
		return
	}
	if _, err := regexp.Compile(pattern); err != nil {
		w.patternErr.SetText("⚠ invalid regex: " + err.Error())
	} else {
		w.patternErr.SetText("✓ valid")
	}
}

func (w *window) onAdd() {
	w.cfg.Rules = append(w.cfg.Rules, config.Rule{ID: genID(), Enabled: true})
	w.model.PublishRowsReset()
	idx := len(w.cfg.Rules) - 1
	w.tv.SetCurrentIndex(idx)
}

func (w *window) onDelete() {
	i := w.current
	if i < 0 || i >= len(w.cfg.Rules) {
		return
	}
	w.cfg.Rules = append(w.cfg.Rules[:i], w.cfg.Rules[i+1:]...)
	w.model.PublishRowsReset()
	w.current = -1
	w.tv.SetCurrentIndex(-1)
	w.populate()
}

// onMove reorders the selected rule by delta (-1 up, +1 down). Order is
// evaluation order (§5.3), so this is a first-class operation.
func (w *window) onMove(delta int) {
	i := w.current
	j := i + delta
	if i < 0 || j < 0 || j >= len(w.cfg.Rules) {
		return
	}
	w.cfg.Rules[i], w.cfg.Rules[j] = w.cfg.Rules[j], w.cfg.Rules[i]
	w.model.PublishRowsReset()
	w.tv.SetCurrentIndex(j)
}

// onTest reports which rule would win for the typed URL, without launching,
// and selects the matching row (§6.2). Writes pending edits back first so the
// test reflects exactly what is on screen.
func (w *window) onTest() {
	w.writeBack() // Flush the detail pane so the test sees what's on screen.
	url := w.testEd.Text()
	if url == "" {
		w.testResult.SetText("type a URL to see which rule wins")
		return
	}
	res := router.Match(w.cfg, url)
	if !res.Matched {
		w.testResult.SetText("no match → Chrome default")
		return
	}
	for i := range w.cfg.Rules {
		if &w.cfg.Rules[i] == res.Rule {
			w.tv.SetCurrentIndex(i)
			break
		}
	}
	w.testResult.SetText(fmt.Sprintf("→ %s", w.friendlyName(res.ProfileDirectory)))
}

func (w *window) onAutoDetect() {
	path, err := chrome.ResolvePath("")
	if err != nil {
		w.status.SetText("auto-detect failed: " + err.Error())
		return
	}
	w.chromePath.SetText(path)
	w.status.SetText("found: " + path)
}

func (w *window) onBrowse() {
	dlg := walk.FileDialog{Title: "Locate chrome.exe", Filter: "Programs (*.exe)|*.exe"}
	if ok, _ := dlg.ShowOpen(w.mw); ok {
		w.chromePath.SetText(dlg.FilePath)
	}
}

func (w *window) onSave() {
	w.cfg.ChromePath = w.chromePath.Text()
	if err := config.Save(w.cfg); err != nil {
		walk.MsgBox(w.mw, "Save failed", err.Error(), walk.MsgBoxIconError)
		return
	}
	w.status.SetText("saved to " + config.Path())
}

// genID returns a short stable identifier for a new rule.
func genID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rule"
	}
	return "rule-" + hex.EncodeToString(b[:])
}

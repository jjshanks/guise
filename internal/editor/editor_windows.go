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
	"strings"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"guise/internal/chrome"
	"guise/internal/config"
	"guise/internal/router"
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
		// A rule bound by account (#22) shows the account, not the directory it
		// currently resolves to, since that is what the user chose and what
		// survives renumbering.
		if !r.ProfileMatch.IsZero() {
			return accountColumnLabel(*r.ProfileMatch)
		}
		return m.nameFor(r.ProfileDirectory)
	case 3:
		return r.Comment
	}
	return ""
}

// rewritesModel adapts the rewrite slice to a walk TableView (§15).
type rewritesModel struct {
	walk.TableModelBase
	rewrites *[]config.Rewrite
}

func (m *rewritesModel) RowCount() int { return len(*m.rewrites) }

func (m *rewritesModel) Value(row, col int) interface{} {
	r := (*m.rewrites)[row]
	switch col {
	case 0:
		if r.Enabled {
			return "✓"
		}
		return ""
	case 1:
		return r.Find
	case 2:
		return r.Replace
	case 3:
		// Show the timing in the same words the detail checkbox uses.
		if r.Delayed {
			return "after match"
		}
		return "before match"
	case 4:
		return r.Comment
	}
	return ""
}

type window struct {
	mw       *walk.MainWindow
	cfg      *config.Config
	profiles []chrome.Profile
	// profileOptions are the profile directories the dropdown offers, in combo
	// order (the no-profile sentinel sits at index 0, ahead of these). It is the
	// union of discovered profiles and any directory already referenced by a
	// rule, so a rule whose profile is missing from Local State still has a
	// stable index and round-trips through an edit instead of being silently
	// reset to "Chrome default".
	profileOptions []string
	// accountOpts are the by-account profile choices the "match by account"
	// dropdown offers (#22), in combo order (a sentinel sits at index 0, ahead of
	// these). It is the union of every signed-in discovered profile and any
	// account a rule already binds to that discovery did not return, so a rule
	// matched by an account no longer present still round-trips through an edit.
	accountOpts []accountOpt
	model       *rulesModel
	rwModel     *rewritesModel

	tv           *walk.TableView
	enabledCB    *walk.CheckBox
	patternEd    *walk.LineEdit
	patternErr   *walk.Label
	profileCB    *walk.ComboBox
	accountCB    *walk.ComboBox
	incognitoCB  *walk.CheckBox
	sourceEd     *walk.LineEdit
	commentEd    *walk.LineEdit
	chromePath   *walk.LineEdit
	testEd       *walk.LineEdit
	testSourceEd *walk.LineEdit
	testResult   *walk.Label
	status       *walk.Label

	// Rewrite tab widgets (§15).
	rwTV        *walk.TableView
	rwEnabledCB *walk.CheckBox
	rwFindEd    *walk.LineEdit
	rwFindErr   *walk.Label
	rwReplaceEd *walk.LineEdit
	rwDelayedCB *walk.CheckBox
	rwCommentEd *walk.LineEdit

	current   int  // index of the rule shown in the detail pane, or -1.
	rwCurrent int  // index of the rewrite shown in its detail pane, or -1.
	loading   bool // true while populating rule widgets, to suppress write-back.
	rwLoading bool // true while populating rewrite widgets, to suppress write-back.
}

// Show opens the editor modally on the calling goroutine and returns when the
// window closes. The caller is responsible for running it on its own OS thread
// (the tray launches it in a fresh goroutine).
func Show() error {
	w := &window{current: -1, rwCurrent: -1}

	cfg, err := config.Load()
	if err != nil {
		// Start from default on bad config so the editor can still be used to
		// fix things; the save will replace the broken file.
		cfg = config.Default()
	}
	w.cfg = cfg
	w.profiles, _ = chrome.Profiles() // Best effort; dropdown may be empty.
	w.profileOptions = profileOptionDirs(w.profiles, w.cfg.Rules)
	w.accountOpts = accountOptions(w.profiles, w.cfg.Rules)
	w.model = &rulesModel{rules: &w.cfg.Rules, nameFor: w.friendlyName}
	w.rwModel = &rewritesModel{rewrites: &w.cfg.Rewrites}

	return w.build()
}

// profileOptionDirs returns the profile directories to show in the dropdown:
// every discovered profile, followed by any non-empty directory referenced by a
// rule that discovery did not return (a profile deleted/renamed in Chrome, or
// any directory at all when Local State is unreadable). Including those keeps a
// configured-but-missing profile selectable, so editing another field on its
// rule no longer collapses the value to "Chrome default".
func profileOptionDirs(profiles []chrome.Profile, rules []config.Rule) []string {
	seen := make(map[string]bool, len(profiles))
	dirs := make([]string, 0, len(profiles))
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	for _, p := range profiles {
		add(p.Directory)
	}
	for i := range rules {
		add(rules[i].ProfileDirectory)
	}
	return dirs
}

// accountOpt is one entry in the "match by account" dropdown (#22): the
// profile_match value it stores plus the friendly label shown. Combo index 0 is
// a sentinel ("use the profile above") that no accountOpt represents.
type accountOpt struct {
	match config.ProfileMatch
	label string
}

// accountOptions lists the by-account binding choices for the dropdown: every
// signed-in discovered profile (an email), followed by any account a rule
// already binds to that discovery did not return (account removed, or Local
// State unreadable) so an existing profile_match round-trips through an edit
// instead of silently reverting to the directory binding.
func accountOptions(profiles []chrome.Profile, rules []config.Rule) []accountOpt {
	var opts []accountOpt
	seen := make(map[string]bool)
	key := func(m config.ProfileMatch) string {
		return strings.ToLower(m.Email) + "\x00" + strings.ToLower(m.HostedDomain)
	}
	add := func(m config.ProfileMatch, label string) {
		if m.IsZero() || seen[key(m)] {
			return
		}
		seen[key(m)] = true
		opts = append(opts, accountOpt{match: m, label: label})
	}
	for _, p := range profiles {
		if p.Email == "" {
			continue
		}
		add(config.ProfileMatch{Email: p.Email}, accountLabel(p))
	}
	for i := range rules {
		if m := rules[i].ProfileMatch; !m.IsZero() {
			add(*m, accountMatchLabel(*m))
		}
	}
	return opts
}

// accountLabel renders a discovered profile as "email — Friendly Name [domain]".
func accountLabel(p chrome.Profile) string {
	label := p.Email
	if p.Name != "" {
		label += " — " + p.Name
	}
	if p.HostedDomain != "" {
		label += " [" + p.HostedDomain + "]"
	}
	return label
}

// accountMatchLabel renders a rule-seeded match whose account discovery did not
// return, flagging that it maps to no current profile.
func accountMatchLabel(m config.ProfileMatch) string {
	if m.Email != "" {
		return m.Email + " (no current profile)"
	}
	return "@" + m.HostedDomain + " (no current profile)"
}

// accountColumnLabel is the compact form shown in the rules table's Profile
// column when a rule binds by account.
func accountColumnLabel(m config.ProfileMatch) string {
	if m.Email != "" {
		return m.Email
	}
	return "@" + m.HostedDomain
}

func (w *window) build() error {
	err := d.MainWindow{
		AssignTo: &w.mw,
		Title:    "Guise — Edit Rules",
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
					// Simulate the originating app (§5.4) so a source-matching rule
					// previews meaningfully — blank means "source undeterminable".
					d.Label{Text: "from app:"},
					d.LineEdit{AssignTo: &w.testSourceEd, ToolTipText: "e.g. slack.exe — blank = source undeterminable", OnTextChanged: w.onTest},
					d.Label{AssignTo: &w.testResult, Text: "type a URL to see how it routes"},
				},
			},
			// Rules and Rewrites live on separate tabs so neither table crowds the
			// other (§6.2, §15). The Test URL row above runs the full pipeline —
			// pre-rewrite, match, delayed-rewrite — against either tab's data.
			d.TabWidget{
				Pages: []d.TabPage{
					{
						Title:  "Rules",
						Layout: d.VBox{},
						Children: []d.Widget{
							// Rules table + reorder/CRUD buttons.
							d.Composite{
								Layout: d.HBox{MarginsZero: true},
								Children: []d.Widget{
									d.TableView{
										AssignTo:         &w.tv,
										MinSize:          d.Size{Height: 200},
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
									d.Label{Text: "Match by account:"},
									d.ComboBox{AssignTo: &w.accountCB, ToolTipText: "Bind to a Chrome profile by Google account (email/Workspace domain) instead of the on-disk directory — survives Chrome renumbering profiles. Overrides the Profile above when set.", OnCurrentIndexChanged: w.writeBack},
									d.CheckBox{AssignTo: &w.incognitoCB, Text: "Open in incognito", OnCheckedChanged: w.writeBack, ColumnSpan: 2},
									d.Label{Text: "Source app (optional):"},
									d.LineEdit{AssignTo: &w.sourceEd, ToolTipText: "Match clicks from this app — case-insensitive substring of the process image name, e.g. slack matches Slack.exe. Blank = any source.", OnTextChanged: w.writeBack},
									d.Label{Text: "Comment:"},
									d.LineEdit{AssignTo: &w.commentEd, OnTextChanged: w.writeBack},
								},
							},
						},
					},
					{
						Title:  "Rewrites",
						Layout: d.VBox{},
						Children: []d.Widget{
							d.Label{Text: "Literal find/replace on the URL, applied in order. " +
								"“Before match” rewrites run before profile selection; “after match” run after it."},
							// Rewrites table + reorder/CRUD buttons.
							d.Composite{
								Layout: d.HBox{MarginsZero: true},
								Children: []d.Widget{
									d.TableView{
										AssignTo:         &w.rwTV,
										MinSize:          d.Size{Height: 180},
										AlternatingRowBG: true,
										Columns: []d.TableViewColumn{
											{Title: "On", Width: 36},
											{Title: "Find", Width: 220},
											{Title: "Replace", Width: 220},
											{Title: "When", Width: 100},
											{Title: "Comment", Width: 160},
										},
										Model:                 w.rwModel,
										OnCurrentIndexChanged: w.onSelectRewrite,
									},
									d.Composite{
										Layout: d.VBox{MarginsZero: true},
										Children: []d.Widget{
											d.PushButton{Text: "Add", OnClicked: w.onAddRewrite},
											d.PushButton{Text: "Delete", OnClicked: w.onDeleteRewrite},
											d.PushButton{Text: "Move ↑", OnClicked: func() { w.onMoveRewrite(-1) }},
											d.PushButton{Text: "Move ↓", OnClicked: func() { w.onMoveRewrite(1) }},
											d.VSpacer{},
										},
									},
								},
							},
							// Detail editor for the selected rewrite.
							d.GroupBox{
								Title:  "Selected rewrite",
								Layout: d.Grid{Columns: 2},
								Children: []d.Widget{
									d.CheckBox{AssignTo: &w.rwEnabledCB, Text: "Enabled", OnCheckedChanged: w.writeBackRewrite, ColumnSpan: 2},
									d.Label{Text: "Find (literal text):"},
									d.LineEdit{AssignTo: &w.rwFindEd, OnTextChanged: w.onFindChanged},
									d.Label{Text: ""},
									d.Label{AssignTo: &w.rwFindErr, Text: ""},
									d.Label{Text: "Replace with:"},
									d.LineEdit{AssignTo: &w.rwReplaceEd, OnTextChanged: w.writeBackRewrite},
									d.CheckBox{AssignTo: &w.rwDelayedCB, Text: "Apply after profile match (delayed)", OnCheckedChanged: w.writeBackRewrite, ColumnSpan: 2},
									d.Label{Text: "Comment:"},
									d.LineEdit{AssignTo: &w.rwCommentEd, OnTextChanged: w.writeBackRewrite},
								},
							},
						},
					},
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

	// Title-bar icon (top-left): reuse the app icon already embedded in the exe.
	// rsrc (see the //go:generate line in main_windows.go) writes the manifest as
	// resource ID 1 and the icon group as ID 2; LoadImage resolves the group to
	// the best size for the title bar. Fail soft — a missing resource just leaves
	// the Windows default, never breaking the editor.
	if icon, err := walk.NewIconFromResourceId(2); err == nil {
		w.mw.SetIcon(icon)
	}

	w.fillProfileCombo()
	w.fillAccountCombo()
	w.populate()        // Clears the rule detail pane (nothing selected yet).
	w.populateRewrite() // Same for the rewrite detail pane.

	// Test seam: a GUI test sets afterBuild to drive the live widgets on the GUI
	// thread (via Synchronize) and then close the window. nil in normal use.
	if afterBuild != nil {
		w.mw.Synchronize(func() { afterBuild(w) })
	}

	w.mw.Run()
	return nil
}

// afterBuild, when non-nil, is invoked once on the GUI thread right after the
// editor window is built (see build). It exists only so a Windows GUI test can
// exercise the real walk widgets; production code never sets it.
var afterBuild func(*window)

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
// strings line up index-for-index with w.profileOptions (offset by the
// sentinel at index 0).
func (w *window) fillProfileCombo() {
	items := make([]string, 0, len(w.profileOptions)+1)
	items = append(items, "(Chrome default — no profile)")
	for _, dir := range w.profileOptions {
		items = append(items, w.friendlyName(dir))
	}
	w.profileCB.SetModel(items)
}

// profileForComboIndex maps a combo index back to a profile directory. Index 0
// is the no-profile sentinel.
func (w *window) profileForComboIndex(i int) string {
	if i <= 0 || i-1 >= len(w.profileOptions) {
		return ""
	}
	return w.profileOptions[i-1]
}

// comboIndexForProfile finds the combo index for a directory. A non-empty
// directory is always present in w.profileOptions (Show seeds it with every
// configured directory), so an existing rule round-trips; only the empty
// "Chrome default" value maps to the sentinel at index 0.
func (w *window) comboIndexForProfile(dir string) int {
	for i, d := range w.profileOptions {
		if d == dir {
			return i + 1
		}
	}
	return 0
}

// fillAccountCombo loads the "match by account" dropdown (#22). The sentinel at
// index 0 defers to the Profile directory above; the rest line up index-for-index
// with w.accountOpts.
func (w *window) fillAccountCombo() {
	items := make([]string, 0, len(w.accountOpts)+1)
	items = append(items, "(use the profile above)")
	for _, o := range w.accountOpts {
		items = append(items, o.label)
	}
	w.accountCB.SetModel(items)
}

// accountComboIndex maps a rule's ProfileMatch back to a combo index. A
// zero/absent match is the sentinel at index 0; otherwise the matching option
// (seeded from every rule's match, so it is always present) wins.
func (w *window) accountComboIndex(m *config.ProfileMatch) int {
	if m.IsZero() {
		return 0
	}
	for i, o := range w.accountOpts {
		if strings.EqualFold(o.match.Email, m.Email) && strings.EqualFold(o.match.HostedDomain, m.HostedDomain) {
			return i + 1
		}
	}
	return 0
}

// matchForAccountIndex maps a combo index to the ProfileMatch it stores, or nil
// for the sentinel at index 0 (bind by directory instead).
func (w *window) matchForAccountIndex(i int) *config.ProfileMatch {
	if i <= 0 || i-1 >= len(w.accountOpts) {
		return nil
	}
	m := w.accountOpts[i-1].match
	return &m
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
		w.accountCB.SetCurrentIndex(0)
		w.incognitoCB.SetChecked(false)
		w.sourceEd.SetText("")
		w.patternErr.SetText("")
		return
	}
	r := w.cfg.Rules[w.current]
	w.enabledCB.SetChecked(r.Enabled)
	w.patternEd.SetText(r.Pattern)
	w.commentEd.SetText(r.Comment)
	w.profileCB.SetCurrentIndex(w.comboIndexForProfile(r.ProfileDirectory))
	w.accountCB.SetCurrentIndex(w.accountComboIndex(r.ProfileMatch))
	w.incognitoCB.SetChecked(r.Incognito)
	w.sourceEd.SetText(r.Source)
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
	// Account binding (#22) overrides the directory when chosen; the sentinel
	// (nil) leaves the rule on its ProfileDirectory.
	r.ProfileMatch = w.matchForAccountIndex(w.accountCB.CurrentIndex())
	r.Incognito = w.incognitoCB.Checked()
	r.Source = w.sourceEd.Text()
	w.model.PublishRowChanged(w.current)
}

func (w *window) onPatternChanged() {
	w.validatePattern(w.patternEd.Text())
	w.writeBack()
}

// validatePattern flags an invalid RE2 pattern inline (§6.2 live validation).
// An empty pattern is called out specifically: routing now skips blank rules
// (see router.Match), so the editor warns rather than leaving the field silent.
func (w *window) validatePattern(pattern string) {
	if pattern == "" {
		w.patternErr.SetText("⚠ empty pattern — this rule is ignored")
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
	// Set the selection explicitly (see onAddRewrite) so detail edits write back
	// even if the table's OnCurrentIndexChanged event does not fire.
	w.current = len(w.cfg.Rules) - 1
	w.tv.SetCurrentIndex(w.current)
	w.populate()
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
	w.current = j
	w.tv.SetCurrentIndex(j)
	w.populate()
}

// onTest reports how the typed URL routes, without launching, and selects the
// winning rule row (§6.2). It delegates to router.Resolve so the preview runs
// the exact ROUTE pipeline — pre-rewrites, match, profile fallback, delayed
// rewrites (§15) — and can never drift from a real click (e.g. a vanished
// profile previews as Chrome default, just as it would route). Pending edits on
// both detail panes are flushed first so the test sees what's on screen.
func (w *window) onTest() {
	w.writeBack()
	w.writeBackRewrite()
	url := w.testEd.Text()
	if url == "" {
		w.testResult.SetText("type a URL to see how it routes")
		return
	}
	// The "from app" field simulates the originating app (§5.4) so a
	// source-matching rule previews exactly as a real click from that app would;
	// blank means the source is undeterminable.
	r := router.Resolve(w.cfg, url, w.testSourceEd.Text())

	// Clear any stale highlight first; only a real match re-selects a row below.
	w.tv.SetCurrentIndex(-1)
	if r.Rule != nil {
		for i := range w.cfg.Rules {
			if &w.cfg.Rules[i] == r.Rule {
				w.tv.SetCurrentIndex(i)
				break
			}
		}
	}

	msg := "→ " + w.friendlyName(r.ProfileDirectory)
	if r.Incognito {
		// Reflect the incognito flag so it's visible before a real click (§6.2).
		msg += " [incognito]"
	}
	if r.ProfileDropped {
		// The rule matched but its profile is gone, so the real click falls back.
		msg += " (rule matched but its profile is missing)"
	}
	if r.URL != url {
		// A rewrite changed the URL; show what Chrome would actually open.
		msg += fmt.Sprintf("  (opens %s)", r.URL)
	}
	w.testResult.SetText(msg)
}

func (w *window) onSelectRewrite() {
	w.rwCurrent = w.rwTV.CurrentIndex()
	w.populateRewrite()
}

// populateRewrite loads the rewrite detail widgets from the current rewrite (or
// clears them). Mirrors populate() for rules.
func (w *window) populateRewrite() {
	w.rwLoading = true
	defer func() { w.rwLoading = false }()

	if w.rwCurrent < 0 || w.rwCurrent >= len(w.cfg.Rewrites) {
		w.rwEnabledCB.SetChecked(false)
		w.rwFindEd.SetText("")
		w.rwReplaceEd.SetText("")
		w.rwDelayedCB.SetChecked(false)
		w.rwCommentEd.SetText("")
		w.rwFindErr.SetText("")
		return
	}
	r := w.cfg.Rewrites[w.rwCurrent]
	w.rwEnabledCB.SetChecked(r.Enabled)
	w.rwFindEd.SetText(r.Find)
	w.rwReplaceEd.SetText(r.Replace)
	w.rwDelayedCB.SetChecked(r.Delayed)
	w.rwCommentEd.SetText(r.Comment)
	w.validateFind(r.Find)
}

// writeBackRewrite copies the rewrite detail widgets into the current rewrite
// and refreshes the table row. Suppressed while populating.
func (w *window) writeBackRewrite() {
	if w.rwLoading || w.rwCurrent < 0 || w.rwCurrent >= len(w.cfg.Rewrites) {
		return
	}
	r := &w.cfg.Rewrites[w.rwCurrent]
	r.Enabled = w.rwEnabledCB.Checked()
	r.Find = w.rwFindEd.Text()
	r.Replace = w.rwReplaceEd.Text()
	r.Delayed = w.rwDelayedCB.Checked()
	r.Comment = w.rwCommentEd.Text()
	w.rwModel.PublishRowChanged(w.rwCurrent)
}

func (w *window) onFindChanged() {
	w.validateFind(w.rwFindEd.Text())
	w.writeBackRewrite()
}

// validateFind warns when Find is blank: router.ApplyRewrites skips a blank
// Find (an empty search would splice Replace between every character), so the
// rewrite is inert until the user types something.
func (w *window) validateFind(find string) {
	if find == "" {
		w.rwFindErr.SetText("⚠ empty find — this rewrite is ignored")
	} else {
		w.rwFindErr.SetText("")
	}
}

func (w *window) onAddRewrite() {
	w.cfg.Rewrites = append(w.cfg.Rewrites, config.Rewrite{ID: genID(), Enabled: true})
	w.rwModel.PublishRowsReset()
	// Track the selection ourselves rather than trusting the table's
	// OnCurrentIndexChanged event: walk's SetCurrentIndex can fail silently for a
	// table on a not-yet-realized tab, leaving rwCurrent at -1 so writeBackRewrite
	// drops every keystroke (the rewrite then saves with empty find/replace). Set
	// rwCurrent explicitly and populate, so editing works regardless of the event.
	w.rwCurrent = len(w.cfg.Rewrites) - 1
	w.rwTV.SetCurrentIndex(w.rwCurrent)
	w.populateRewrite()
}

func (w *window) onDeleteRewrite() {
	i := w.rwCurrent
	if i < 0 || i >= len(w.cfg.Rewrites) {
		return
	}
	w.cfg.Rewrites = append(w.cfg.Rewrites[:i], w.cfg.Rewrites[i+1:]...)
	w.rwModel.PublishRowsReset()
	w.rwCurrent = -1
	w.rwTV.SetCurrentIndex(-1)
	w.populateRewrite()
}

// onMoveRewrite reorders the selected rewrite by delta. Order is application
// order (rewrites chain), so reordering is meaningful (§15).
func (w *window) onMoveRewrite(delta int) {
	i := w.rwCurrent
	j := i + delta
	if i < 0 || j < 0 || j >= len(w.cfg.Rewrites) {
		return
	}
	w.cfg.Rewrites[i], w.cfg.Rewrites[j] = w.cfg.Rewrites[j], w.cfg.Rewrites[i]
	w.rwModel.PublishRowsReset()
	w.rwCurrent = j
	w.rwTV.SetCurrentIndex(j)
	w.populateRewrite()
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
	// Flush whatever is on screen for the selected rule/rewrite before saving, in
	// case a pending edit has not been written back yet (belt and suspenders).
	w.writeBack()
	w.writeBackRewrite()
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

//go:build windows

package editor

import (
	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"

	"urlrouter/internal/chrome"
	"urlrouter/internal/config"
	"urlrouter/internal/router"
)

// ShowTest opens the lightweight "Test a URL" dialog from the tray (§6.1): type
// a URL and see which rule would win and which profile would launch, without
// launching anything. Critical for debugging unanchored patterns. Runs on the
// calling goroutine and returns when the window closes.
func ShowTest() error {
	cfg, _ := config.Load() // Bad config simply yields the default (route all to Chrome).
	profiles, _ := chrome.Profiles()

	var mw *walk.MainWindow
	var ed *walk.LineEdit
	var result *walk.Label

	evaluate := func() {
		url := ed.Text()
		if url == "" {
			result.SetText("Type a URL above.")
			return
		}
		res := router.Match(cfg, url)
		if !res.Matched {
			result.SetText("No rule matches → Chrome default (no profile flag).")
			return
		}
		result.SetText("Rule " + res.Rule.ID + " wins → " + friendlyName(profiles, res.ProfileDirectory))
	}

	_, err := d.MainWindow{
		AssignTo: &mw,
		Title:    "URL Router — Test a URL",
		MinSize:  d.Size{Width: 480, Height: 140},
		Layout:   d.VBox{},
		Children: []d.Widget{
			d.Label{Text: "Enter a URL to see which rule would match (nothing is launched):"},
			d.LineEdit{AssignTo: &ed, OnTextChanged: evaluate},
			d.Label{AssignTo: &result, Text: "Type a URL above."},
			d.Composite{
				Layout: d.HBox{MarginsZero: true},
				Children: []d.Widget{
					d.HSpacer{},
					d.PushButton{Text: "Close", OnClicked: func() { mw.Close() }},
				},
			},
		},
	}.Run()
	return err
}

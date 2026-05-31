// Package assets embeds binary resources used at runtime, such as the tray
// icon (§8: icon embedded and referenced by the tray).
package assets

import _ "embed"

// Icon is the application icon as .ico bytes, used for the tray icon.
//
//go:embed icon.ico
var Icon []byte

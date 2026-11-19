// Package ui holds the dashboard this daemon serves at `/`.
//
// IT IS EMBEDDED, NOT A DIRECTORY ON DISK. The page is three files and they must match the Go binary
// exactly - a dashboard that shipped separately would be a second artifact whose version can differ
// from the API it calls, and the failure would be a blank panel rather than a version mismatch
// anybody can read. `//go:embed` makes the page part of the binary that serves it.
package ui

import _ "embed"

// Index is the dashboard itself.
//
//go:embed index.html
var Index []byte

// App is the dashboard's script.
//
//go:embed app.js
var App []byte

// EstIndex is the full-window est page, served at `/est.html`.
//
// IT IS THE PUBLISHED easystream-webui, VENDORED AT `ui/vendor/easystream-webui` AND NOT REWRITTEN,
// because it is the implementation that has been running against this protocol the longest: zoom and
// pan, touch gestures, audio, the clipboard bridge, quality presets and a virtual keyboard. The
// dashboard's own est panel is a canvas and an input path; this page is the client for someone who
// wants to USE the guest rather than glance at it, which is the same relationship `/vnc.html` has
// with the embedded noVNC frame.
//
// ONE EDIT WAS REQUIRED, and it is documented at the call site in est.js: the token moved from the
// WebSocket subprotocol list to a `0x03` Auth frame, because a browser closes a socket whose offered
// protocols the server does not echo and `/est/ws` only ever negotiates `est`.
//
//go:embed est.html
var EstIndex []byte

// EstApp is that page's script.
//
//go:embed est.js
var EstApp []byte

// ContentType makes a handler's response unambiguous.
const (
	ContentTypeHTML = "text/html; charset=utf-8"
	ContentTypeJS   = "application/javascript; charset=utf-8"
)

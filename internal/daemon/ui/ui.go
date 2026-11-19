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

// ContentType makes a handler's response unambiguous.
const (
	ContentTypeHTML = "text/html; charset=utf-8"
	ContentTypeJS   = "application/javascript; charset=utf-8"
)

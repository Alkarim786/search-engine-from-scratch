// Package web embeds the single-page search UI.
package web

import "embed"

// Files holds index.html.
//
//go:embed index.html
var Files embed.FS

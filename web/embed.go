//go:build !noui

// Package web holds the dashboard and exposes its built assets to the server.
package web

import (
	"embed"
	"io/fs"
)

// The all: prefix is required so the pattern matches when only the committed
// .gitkeep is present. Without it a fresh checkout fails to build, vet, and test.
//
//go:embed all:dist
var distFS embed.FS

// Embedded reports whether this binary carries the dashboard.
const Embedded = true

// FS returns the dashboard build output rooted at its index.html.
func FS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

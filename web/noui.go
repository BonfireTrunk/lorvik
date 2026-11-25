//go:build noui

// Package web holds the dashboard and exposes its built assets to the server.
package web

import "io/fs"

// Embedded reports whether this binary carries the dashboard.
const Embedded = false

// FS returns nil: this binary was built with -tags noui.
func FS() (fs.FS, error) {
	return nil, nil
}

// Package webui embeds the built frontend into the binary, so a deployment is
// a single file with no static assets to copy alongside it.
package webui

import (
	"embed"
	"io/fs"
)

// dist holds the output of `npm run build` in web/.
//
// The placeholder index.html is committed so the Go build never fails on a
// fresh clone; running the frontend build overwrites this directory.
//
//go:embed all:dist
var dist embed.FS

// FS returns the frontend rooted at dist/, or nil when no build is present.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// Built reports whether a real frontend build is embedded.
func Built() bool { return FS() != nil }

// Package web embeds the browser UI.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// FS returns the static UI files rooted at the site root.
func FS() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

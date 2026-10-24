// Package webui serves the compiled Pasture web console and delegates the
// session API surface to the existing handler. The production bundle is
// committed under assets/ and embedded into the binary, so a plain `go build`
// needs no Node or Vite process.
package webui

import (
	"embed"
	"io/fs"
)

// embeddedAssets holds the Vite production output committed at
// internal/webui/assets. `all:` includes every file, so a future dotfile in the
// bundle is embedded too.
//
//go:embed all:assets
var embeddedAssets embed.FS

// Embedded returns the embedded console tree rooted at the bundle directory
// (the parent of index.html).
func Embedded() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "assets")
}

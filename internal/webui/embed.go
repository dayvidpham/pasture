// Package webui serves the compiled Pasture web console and delegates the
// session API surface to the existing handler. The production bundle is
// committed under assets/ and embedded into the binary, so a plain `go build`
// needs no Node or Vite process.
package webui

import (
	"embed"
	"fmt"
	"io/fs"
)

// embeddedAssets holds the Vite production output committed at
// internal/webui/assets. `all:` includes every file, so a future dotfile in the
// bundle is embedded too.
//
//go:embed all:assets
var embeddedAssets embed.FS

// Embedded returns the embedded console tree rooted at the bundle directory
// (the parent of index.html) after checking the shell and every local asset it
// names. It fails closed rather than letting serve start with a blank console.
func Embedded() (fs.FS, error) {
	assets, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, fmt.Errorf("webui: open the embedded console bundle: %w — rebuild pasture after `cd web && pnpm install --frozen-lockfile && pnpm build`", err)
	}
	if err := validateBundle(assets, embeddedHints()); err != nil {
		return nil, err
	}
	return assets, nil
}

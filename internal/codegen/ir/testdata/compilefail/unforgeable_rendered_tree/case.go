// Package compilefail is an isolated compile-fail fixture: RenderedFile and
// RenderedTree construction is private to the ir package (see
// document.go's newRenderedFile/newRenderedTree) — Compile is the only
// producer. A caller outside the package cannot even name a constructor to
// attempt forging one.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

func invalidUse() {
	_, _ = ir.NewRenderedFile("path.md", []byte("content"))
}

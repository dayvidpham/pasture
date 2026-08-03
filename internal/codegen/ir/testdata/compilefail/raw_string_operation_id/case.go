// Package compilefail is an isolated compile-fail fixture: a raw untyped
// string literal must not satisfy the opaque SemanticOperationID boundary.
// It is isolated in its own directory/package so this one domain violation
// fails compilation on its own, independent of any other fixture.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

func takesID(_ ir.SemanticOperationID) {}

func invalidUse() {
	takesID("pasture.raw.literal/v1")
}

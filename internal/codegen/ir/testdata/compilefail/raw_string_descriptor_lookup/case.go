// Package compilefail is an isolated compile-fail fixture: a raw untyped
// string literal must not satisfy the opaque OperationDescriptor lookup
// boundary — only a genuine, constructor-produced descriptor value can.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct{}
type output struct{}

func lookupOperation(_ ir.OperationDescriptor[input, output]) {}

func invalidUse() {
	lookupOperation("pasture.raw.literal/v1")
}

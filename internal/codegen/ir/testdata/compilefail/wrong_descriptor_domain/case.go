// Package compilefail is an isolated compile-fail fixture: an EffectDescriptor
// must not satisfy an OperationDescriptor lookup, even with identical type
// parameters — the two descriptor domains (semantic operations vs effects)
// are not interchangeable.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct{}
type output struct{}

func lookupOperation(_ ir.OperationDescriptor[input, output]) {}

func invalidUse() {
	lookupOperation(ir.EffectDescriptor[input, output]{})
}

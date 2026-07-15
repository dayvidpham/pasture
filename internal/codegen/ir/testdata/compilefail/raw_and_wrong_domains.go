package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct{}
type output struct{}

func takesID(_ ir.SemanticOperationID)                        {}
func lookupOperation(_ ir.OperationDescriptor[input, output]) {}

func invalidUses() {
	takesID("pasture.raw.literal/v1")
	lookupOperation("pasture.raw.literal/v1")
	lookupOperation(ir.EffectDescriptor[input, output]{})
}

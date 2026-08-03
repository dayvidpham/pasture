// Package verbatimclosureforwarding is a capabilitylint fixture proving the
// counterpart to reassigned_parameter_via_closure: a closure that captures
// an outer CapabilityID-typed parameter and forwards it WITHOUT ever
// reassigning it produces zero findings — the verbatim-forwarding
// enforcement rejects reassignment specifically, not closure capture in
// general.
package verbatimclosureforwarding

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func Outer(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) func() (ir.Capability[input, output], error) {
	return func() (ir.Capability[input, output], error) {
		return ir.DefineCapability(
			id,
			ir.CapabilityContractVersion("1.0.0"),
			ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
			effects,
			inputCodec,
			outputCodec,
		)
	}
}

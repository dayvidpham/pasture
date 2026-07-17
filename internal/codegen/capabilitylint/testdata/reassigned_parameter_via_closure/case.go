// Package reassignedparameterviaclosure is a capabilitylint fixture proving
// the verbatim-forwarding check follows a captured parameter into a nested
// function literal: Outer's id parameter is reassigned to a raw literal
// *inside* the closure it returns, not in Outer's own body, and the
// forwarding call is also inside that closure. go/parser resolves the
// captured identifier to the SAME *ast.Object as the outer parameter, so
// this must be rejected exactly like a direct, non-closure reassignment.
package reassignedparameterviaclosure

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func Outer(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) func() (ir.Capability[input, output], error) {
	return func() (ir.Capability[input, output], error) {
		id = ir.CapabilityID("acme.diagram.closure.rebound")
		return ir.MustDefineCapability(
			id,
			ir.CapabilityContractVersion("1.0.0"),
			ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
			effects,
			inputCodec,
			outputCodec,
		), nil
	}
}

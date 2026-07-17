// Package rangedefinedloopvariable is a capabilitylint fixture proving a
// `:=` range clause does not falsely disqualify a same-named parameter: the
// loop variable `id` here (declared via `:=`) is a NEW *ast.Object confined
// to the loop body, distinct from the outer id parameter's own *ast.Object
// — go/parser's identifier resolution scopes it that way — so forwarding
// the outer id after the loop must still produce zero findings.
package rangedefinedloopvariable

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineAfterRangeDefinedShadow(id ir.CapabilityID, ids []ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	for _, id := range ids {
		_ = id // the loop-local shadow; scoped to this block only
	}
	return ir.DefineCapability(
		id, // the outer, never-disqualified parameter
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

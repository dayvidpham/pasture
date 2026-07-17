// Package reassignedparameterviarange is a capabilitylint fixture proving
// the verbatim-forwarding check follows range-clause reassignment: `for _,
// id = range ids` (using `=`, not `:=`) rebinds the CapabilityID-typed
// parameter's own *ast.Object on every iteration — an *ast.RangeStmt, a
// distinct AST shape from the *ast.AssignStmt the direct-reassignment
// fixture (reassigned_parameter) exercises — and must be rejected
// identically.
package reassignedparameterviarange

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithRangeReassignedParameter(id ir.CapabilityID, ids []ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	for _, id = range ids {
		return ir.DefineCapability(
			id, // want "does not resolve to a package-level"
			ir.CapabilityContractVersion("1.0.0"),
			ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
			effects,
			inputCodec,
			outputCodec,
		)
	}
	return ir.Capability[input, output]{}, nil
}

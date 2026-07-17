// Package convertedliteralid is a capabilitylint fixture: it passes an
// inline ir.CapabilityID(...) conversion of a literal as the identity
// argument to DefineCapability, using the inferred (non-bracketed) generic
// call shape to prove the analyzer handles that form too. This compiles
// successfully — a named-type conversion of an untyped string constant is
// exactly as valid as the bare literal — and must still be rejected.
package convertedliteralid

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithConversion(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	return ir.DefineCapability(
		ir.CapabilityID("acme.diagram.render"),
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

// Package concatenatedliteral is a capabilitylint fixture: it builds the
// capability identity via string concatenation instead of a declared
// constant. This compiles successfully (both operands are untyped string
// constants, and their concatenation is still assignable to CapabilityID),
// and must still be rejected.
package concatenatedliteral

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithConcatenation(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return ir.MustDefineCapability[input, output](
		"acme."+"diagram.render", // want "binary expression \\(e\\.g\\. string concatenation\\)"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

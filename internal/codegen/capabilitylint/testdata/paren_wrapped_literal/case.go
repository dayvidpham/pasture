// Package parenwrappedliteral is a capabilitylint fixture: it wraps a raw
// string literal identity in redundant parentheses. gofmt does not strip
// these parens, so this exact shape can appear in real, gofmt-clean source
// (e.g. after a line-wrap edit); it must be rejected identically to the bare
// literal.
package parenwrappedliteral

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithParenLiteral(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return ir.MustDefineCapability[input, output](
		("acme.diagram.render"),
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

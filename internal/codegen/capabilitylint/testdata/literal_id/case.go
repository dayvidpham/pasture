// Package literalid is a capabilitylint fixture: it intentionally passes a
// raw string literal as the capability identity argument to
// MustDefineCapability. Go's own literal-assignability rule for named
// string types lets this package compile successfully (see
// capabilitylint_test.go's
// TestLiteralIdentityFixtureCompilesDespiteBeingLinted) — capabilitylint.Check
// is the rule that must still reject it. This fixture also uses the
// explicit generic-instantiation call shape (MustDefineCapability[In, Out])
// to prove the analyzer unwraps it.
package literalid

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithLiteral(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return ir.MustDefineCapability[input, output](
		"acme.diagram.render", // want "raw string literal capability identity"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

// Package structfieldselector is a capabilitylint fixture: it passes a
// struct-field selector (cfg.ID) as the capability identity. cfg is a
// function-local value, not an imported package, so this SelectorExpr must
// not be confused with the conservative pkg.SomeCapabilityID cross-package
// allowance — it compiles successfully (cfg.ID's type is exactly
// ir.CapabilityID) and must still be rejected, since the field's value
// cannot be statically verified as a stable, canonical identity.
package structfieldselector

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

type capabilityConfig struct {
	ID ir.CapabilityID
}

func defineWithStructFieldSelector(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	cfg := capabilityConfig{ID: "acme.diagram.render"}
	return ir.MustDefineCapability[input, output](
		cfg.ID,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

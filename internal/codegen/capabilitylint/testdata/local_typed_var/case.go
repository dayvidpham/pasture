// Package localtypedvar is a capabilitylint fixture: it declares a
// correctly *typed* local variable (`var localID ir.CapabilityID = ...`)
// inside the function body and passes it to DefineCapability. This compiles
// successfully — localID's type matches CapabilityID exactly, no conversion
// needed — but a function-local variable is not a canonical, package-scope
// declaration site, so the analyzer must still reject it.
package localtypedvar

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithLocalTypedVar(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	var localID ir.CapabilityID = "acme.diagram.render"
	return ir.DefineCapability(
		localID, // want "does not resolve to a package-level"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

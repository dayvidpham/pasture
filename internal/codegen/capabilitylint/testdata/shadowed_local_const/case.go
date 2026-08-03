// Package shadowedlocalconst is a capabilitylint fixture: it declares a
// legitimate package-level typed const identity (CapabilityRenderDiagram),
// then in a different function shadows that exact name with a
// function-local variable holding an arbitrary, user-supplied value, and
// passes the *shadowed local* to MustDefineCapability. Go allows this
// silent shadowing via `:=` with no compiler warning, so this compiles
// successfully; the analyzer must still reject it, because
// go/parser's identifier resolution gives the shadowing local a different
// *ast.Object than the package-level constant it merely shares a name with.
package shadowedlocalconst

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

// CapabilityRenderDiagram is the legitimate, canonical package-level const.
const CapabilityRenderDiagram ir.CapabilityID = "acme.legit.canonical"

// defineWithShadowedLocal shadows CapabilityRenderDiagram's name with a
// function-local variable built from an arbitrary parameter, then passes
// the shadowing local — not the canonical constant — to
// MustDefineCapability.
func defineWithShadowedLocal(userSuppliedID string, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	CapabilityRenderDiagram := ir.CapabilityID(userSuppliedID)
	return ir.MustDefineCapability[input, output](
		CapabilityRenderDiagram, // want "does not resolve to a package-level"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

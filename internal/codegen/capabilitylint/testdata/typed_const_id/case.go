// Package typedconstid is a capabilitylint fixture proving the canonical,
// accepted usage pattern produces zero findings: a package-level
// `const ... ir.CapabilityID = "..."`, an opaque descriptor stored in a var
// through MustDefineCapability, and InvokeTool invoked with matching typed
// input — exactly the accepted contract's own required-API example.
package typedconstid

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

// CapabilityRenderDiagram is the canonical declaration-site typed const the
// accepted contract requires.
const CapabilityRenderDiagram ir.CapabilityID = "acme.diagram.render"

func mustInputCodec() ir.Codec[input] {
	codec, err := ir.NewJSONCodec[input]("acme.diagram.render-input/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

func mustOutputCodec() ir.Codec[output] {
	codec, err := ir.NewJSONCodec[output]("acme.diagram.render-output/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

func mustEffects() ir.EffectSet {
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	return effects
}

// RenderDiagram stores the opaque descriptor in a var through
// MustDefineCapability, matching the accepted contract's required shape.
var RenderDiagram = ir.MustDefineCapability(
	CapabilityRenderDiagram,
	ir.CapabilityContractVersion("1.0.0"),
	ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
	mustEffects(),
	mustInputCodec(),
	mustOutputCodec(),
)

func invoke() ir.SemanticOperation {
	return ir.InvokeTool(RenderDiagram, input{Name: "example"})
}

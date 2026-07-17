// Package functioncallresult is a capabilitylint fixture: it passes the
// result of an arbitrary function call as the capability identity instead of
// a declared constant. computeID's return type is exactly ir.CapabilityID,
// so this compiles successfully — the analyzer, not the type system, must
// still reject it, since a call result cannot be statically verified as a
// stable, canonical identity (it could return something different on every
// invocation).
package functioncallresult

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func computeID() ir.CapabilityID {
	return "acme.diagram.render"
}

func defineWithFunctionCallResult(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return ir.MustDefineCapability[input, output](
		computeID(), // want "result of a function/method call"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

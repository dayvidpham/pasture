// Package typedparameterforwarding is a capabilitylint fixture proving the
// third recognized-safe shape: an ir.CapabilityID-typed parameter of the
// enclosing function, forwarded verbatim to DefineCapability/
// MustDefineCapability, produces zero findings. Wrap mirrors the exact shape
// ir.MustDefineCapability's own required implementation uses internally
// (forwarding its id parameter to ir.DefineCapability) — the pattern this
// allowance exists to permit.
package typedparameterforwarding

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

// Wrap forwards an already-typed capability identity, supplied by its own
// caller, to DefineCapability — exactly the "dynamic or user-supplied
// inputs must use the error-returning constructor" pattern the accepted
// contract sanctions.
func Wrap(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	return ir.DefineCapability(
		id,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

// WrapLiteral forwards a MustDefineCapability function-literal parameter,
// proving the allowance also applies inside an *ast.FuncLit, not only
// *ast.FuncDecl.
var WrapLiteral = func(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return ir.MustDefineCapability[input, output](
		id,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

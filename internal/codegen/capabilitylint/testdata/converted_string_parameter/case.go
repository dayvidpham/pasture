// Package convertedstringparameter is a capabilitylint fixture proving the
// typed-parameter-forwarding allowance (see testdata/typed_parameter_forwarding)
// is not a loophole: a plain string parameter converted inline to
// CapabilityID at the DefineCapability call site is still rejected — the
// allowance only covers a parameter that is *already* declared with type
// CapabilityID, never one converted on the way in.
package convertedstringparameter

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func WrapRawString(raw string, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	return ir.DefineCapability(
		ir.CapabilityID(raw), // want "inline ir\\.CapabilityID\\(\\.\\.\\.\\) conversion"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

// Package unrelatedaddressof is a capabilitylint fixture proving address-of
// disqualification is scoped to the exact identifier whose address is
// taken: taking the address of a completely unrelated local variable must
// not disqualify the CapabilityID-typed id parameter forwarded elsewhere in
// the same function.
package unrelatedaddressof

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithUnrelatedAddressOf(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	other := "unrelated"
	p := &other
	*p = "still unrelated"
	return ir.DefineCapability(
		id,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

// Package reassignedparameterviapointer is a capabilitylint fixture proving
// the verbatim-forwarding check disqualifies a parameter the moment its
// address is taken at all: `p := &id; *p = ...` mutates id through the
// pointer, but even without a subsequent mutation, taking &id anywhere is
// itself conservatively treated as forfeiting the verbatim-forwarding
// claim, since this single-file syntactic checker cannot track what a
// resulting pointer's writes ultimately target.
package reassignedparameterviapointer

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithPointerReassignedParameter(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	p := &id
	*p = ir.CapabilityID("acme.diagram.pointer.rebound")
	return ir.MustDefineCapability(
		id, // want "does not resolve to a package-level"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	), nil
}

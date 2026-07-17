// Package reassignedparameter is a capabilitylint fixture proving the
// parameter-forwarding allowance requires *verbatim* forwarding: a
// CapabilityID-typed parameter that is reassigned to a raw literal before
// being forwarded to DefineCapability/MustDefineCapability is rejected, even
// though the identifier still resolves (by *ast.Object identity) to the
// parameter declaration — the allowance is conditioned on the parameter
// never being reassigned anywhere in its owning function, not merely on
// matching the parameter's Object.
package reassignedparameter

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithReassignedParameter(id ir.CapabilityID, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	id = ir.CapabilityID("acme.diagram.rebound")
	return ir.DefineCapability(
		id, // want "does not resolve to a package-level"
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

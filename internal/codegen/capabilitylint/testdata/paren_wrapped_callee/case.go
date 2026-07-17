// Package parenwrappedcallee is a capabilitylint fixture: it wraps the
// MustDefineCapability *callee* itself in redundant parentheses (not the
// identity argument, which testdata/paren_wrapped_literal already covers).
// This shape — (ir.MustDefineCapability[input, output])(...) — compiles
// successfully and gofmt does not strip the parens; without unwrapping them
// at the callee, this call site is not recognized as a lint target at all
// (a distinct, more severe gap than a rejected argument: an invisible call
// site produces zero findings for a reason indistinguishable from "passed",
// bypassing both this package's own tests and the module-wide gate).
package parenwrappedcallee

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithParenWrappedCallee(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) ir.Capability[input, output] {
	return (ir.MustDefineCapability[input, output])(
		"acme.diagram.render",
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

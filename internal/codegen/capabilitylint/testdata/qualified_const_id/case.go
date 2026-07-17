// Package qualifiedconstid is a capabilitylint fixture proving a
// package-qualified reference to a canonical const declared in a different
// package (other.RenderCapabilityID) is not flagged: capabilitylint.Check
// cannot resolve it without loading the imported package, so it
// conservatively allows the shape rather than false-positiving on
// legitimate cross-package reuse.
package qualifiedconstid

import (
	"github.com/dayvidpham/pasture/internal/codegen/capabilitylint/testdata/qualified_const_id/other"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func defineWithQualifiedConst(inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	return ir.DefineCapability(
		other.RenderCapabilityID,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

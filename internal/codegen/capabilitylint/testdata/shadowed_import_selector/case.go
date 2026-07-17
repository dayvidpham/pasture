// Package shadowedimportselector is a capabilitylint fixture: it imports an
// unrelated package ("fmt") and then shadows that import's local name with a
// function-local struct value, passing a field selector on the shadowing
// local as the capability identity. Without scope verification, this
// "fmt.Value" selector is indistinguishable — by name alone — from a
// genuine cross-package pkg.SomeCapabilityID reference, since "fmt" really
// is one of this file's imports; it must still be rejected, because the
// "fmt" at the call site resolves (via go/parser) to the shadowing local,
// not the import.
package shadowedimportselector

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

type holder struct {
	Value ir.CapabilityID
}

func defineWithShadowedImportSelector(userValue string, inputCodec ir.Codec[input], outputCodec ir.Codec[output], effects ir.EffectSet) (ir.Capability[input, output], error) {
	fmt := holder{Value: ir.CapabilityID(fmt.Sprintf("acme.diagram.%s", userValue))}
	return ir.DefineCapability(
		fmt.Value,
		ir.CapabilityContractVersion("1.0.0"),
		ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

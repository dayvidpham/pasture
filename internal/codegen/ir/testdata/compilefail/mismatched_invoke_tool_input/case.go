// Package compilefail is an isolated compile-fail fixture: InvokeTool must
// reject an input value whose type does not match the Capability's own
// generic In type parameter, even though wrongInput is itself a perfectly
// valid, unrelated Go struct.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

type wrongInput struct {
	Count int `json:"count"`
}

const fixtureID ir.CapabilityID = "pasture.fixture.mismatched-input/v1"

func capability() ir.Capability[input, output] {
	inputCodec, err := ir.NewJSONCodec[input]("pasture.fixture.mismatched-input-in/v1", nil)
	if err != nil {
		panic(err)
	}
	outputCodec, err := ir.NewJSONCodec[output]("pasture.fixture.mismatched-input-out/v1", nil)
	if err != nil {
		panic(err)
	}
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	return ir.MustDefineCapability(fixtureID, ir.CapabilityContractVersion("1.0.0"), ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"}, effects, inputCodec, outputCodec)
}

func invalidUse() {
	ir.InvokeTool(capability(), wrongInput{Count: 1})
}

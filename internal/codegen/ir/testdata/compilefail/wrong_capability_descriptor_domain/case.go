// Package compilefail is an isolated compile-fail fixture: an
// OperationDescriptor (the #38 core-operation lookup descriptor) must not
// satisfy InvokeTool's Capability lookup boundary, despite both being
// structurally similar opaque generic descriptors over the same In/Out
// types.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func descriptor() ir.OperationDescriptor[input, output] {
	id, err := ir.NewSemanticOperationID("pasture.fixture.wrong-domain/v1")
	if err != nil {
		panic(err)
	}
	inputCodec, err := ir.NewJSONCodec[input]("pasture.fixture.wrong-domain-input/v1", nil)
	if err != nil {
		panic(err)
	}
	outputCodec, err := ir.NewJSONCodec[output]("pasture.fixture.wrong-domain-output/v1", nil)
	if err != nil {
		panic(err)
	}
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	value, err := ir.NewOperationDescriptor(id, inputCodec, outputCodec, ir.DescriptorSemantics{Summary: "fixture", Result: "fixture result"}, effects)
	if err != nil {
		panic(err)
	}
	return value
}

func invalidUse() {
	ir.InvokeTool(descriptor(), input{Name: "example"})
}

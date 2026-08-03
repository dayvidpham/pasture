package compilepass

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Created bool `json:"created"`
}

func descriptor() ir.OperationDescriptor[input, output] {
	operationID, err := ir.NewSemanticOperationID("pasture.fixture.create/v1")
	if err != nil {
		panic(err)
	}
	inputCodec, err := ir.NewJSONCodec[input]("pasture.fixture.input/v1", nil)
	if err != nil {
		panic(err)
	}
	outputCodec, err := ir.NewJSONCodec[output]("pasture.fixture.output/v1", nil)
	if err != nil {
		panic(err)
	}
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	value, err := ir.NewOperationDescriptor(
		operationID, inputCodec, outputCodec,
		ir.DescriptorSemantics{Summary: "fixture", Result: "fixture result"}, effects,
	)
	if err != nil {
		panic(err)
	}
	return value
}

func lookup(_ ir.OperationDescriptor[input, output]) {}

func compilePass() { lookup(descriptor()) }

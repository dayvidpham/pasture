// Command panicfixture is a subprocess fixture proving an invalid static
// declaration panics with the original, actionable DefineCapability
// validation error: invalidStaticCapability is a package-level var
// initialized through MustDefineCapability with a non-namespaced identity,
// so the process crashes during package initialization, before main ever
// runs, with DefineCapability's own diagnostic on stderr.
package main

import "github.com/dayvidpham/pasture/internal/codegen/ir"

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

func mustEffects() ir.EffectSet {
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	return effects
}

func mustInputCodec() ir.Codec[input] {
	codec, err := ir.NewJSONCodec[input]("pasture.fixture.panic-input/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

func mustOutputCodec() ir.Codec[output] {
	codec, err := ir.NewJSONCodec[output]("pasture.fixture.panic-output/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

// invalidStaticCapability intentionally uses a non-namespaced identity to
// prove MustDefineCapability panics with the actionable DefineCapability
// validation error at package-level static declaration time.
var invalidStaticCapability = ir.MustDefineCapability[input, output](
	ir.CapabilityID("not-namespaced"),
	ir.CapabilityContractVersion("1.0.0"),
	ir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
	mustEffects(),
	mustInputCodec(),
	mustOutputCodec(),
)

func main() {
	_ = invalidStaticCapability
}

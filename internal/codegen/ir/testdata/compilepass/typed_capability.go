package compilepass

import "github.com/dayvidpham/pasture/internal/codegen/ir"

// capabilityInput/capabilityResult are deliberately named differently from
// this directory's typed_descriptors.go input/output types: both files
// build as one package, so their symbols must not collide.
type capabilityInput struct {
	Name string `json:"name"`
}

type capabilityResult struct {
	Rendered bool `json:"rendered"`
}

// capabilityFixtureID is the canonical typed const identity #41 requires at
// every capability's declaration site.
const capabilityFixtureID ir.CapabilityID = "pasture.fixture.capability/v1"

func mustCapabilityInputCodec() ir.Codec[capabilityInput] {
	codec, err := ir.NewJSONCodec[capabilityInput]("pasture.fixture.capability-input/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

func mustCapabilityOutputCodec() ir.Codec[capabilityResult] {
	codec, err := ir.NewJSONCodec[capabilityResult]("pasture.fixture.capability-output/v1", nil)
	if err != nil {
		panic(err)
	}
	return codec
}

func mustCapabilityEffects() ir.EffectSet {
	effects, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	return effects
}

// capabilityFixture stores the opaque descriptor in a package-level var
// through MustDefineCapability, exactly as the accepted contract requires.
var capabilityFixture = ir.MustDefineCapability[capabilityInput, capabilityResult](
	capabilityFixtureID,
	ir.CapabilityContractVersion("1.0.0"),
	ir.CapabilitySemantics{Summary: "fixture capability", Result: "fixture capability result"},
	mustCapabilityEffects(),
	mustCapabilityInputCodec(),
	mustCapabilityOutputCodec(),
)

// invokeCapabilityFixture invokes the descriptor with matching typed input,
// proving InvokeTool accepts only the opaque Capability descriptor.
func invokeCapabilityFixture() ir.SemanticOperation {
	return ir.InvokeTool(capabilityFixture, capabilityInput{Name: "example"})
}

func compilePassCapability() { _ = invokeCapabilityFixture() }

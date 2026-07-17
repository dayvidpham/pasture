// Package shadowedirimportselector is the "ir-shadow" variant of
// shadowed_import_selector: it proves the scope-aware import-qualifier
// check works uniformly even when the shadowed import's local name happens
// to be "ir" — the same conventional name this whole package's target
// (github.com/dayvidpham/pasture/internal/codegen/ir) is usually imported
// under — not only an unrelated name like "fmt". To keep the
// DefineCapability call itself unaffected by the shadow (so this fixture
// still exercises rule 3, not merely rule 2's target-recognition), the
// capability package is imported under a different alias (capir); a
// completely unrelated package ("strings") is aliased "ir" purely so that
// name is a genuine file import for the shadow to exploit.
package shadowedirimportselector

import (
	ir "strings"

	capir "github.com/dayvidpham/pasture/internal/codegen/ir"
)

type input struct {
	Name string `json:"name"`
}

type output struct {
	Rendered bool `json:"rendered"`
}

type holder struct {
	Value capir.CapabilityID
}

func defineWithShadowedIRSelector(userValue string, inputCodec capir.Codec[input], outputCodec capir.Codec[output], effects capir.EffectSet) (capir.Capability[input, output], error) {
	prefixed := ir.ToUpper(userValue)
	ir := holder{Value: capir.CapabilityID(prefixed)}
	return capir.DefineCapability(
		ir.Value,
		capir.CapabilityContractVersion("1.0.0"),
		capir.CapabilitySemantics{Summary: "fixture", Result: "fixture result"},
		effects,
		inputCodec,
		outputCodec,
	)
}

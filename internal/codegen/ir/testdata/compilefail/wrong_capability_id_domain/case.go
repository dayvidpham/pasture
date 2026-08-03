// Package compilefail is an isolated compile-fail fixture: a validated
// SkillID must not satisfy a CapabilityID parameter. Both are ultimately
// string-shaped portable identities, but CapabilityID's underlying type
// (string) and SkillID's underlying type (an opaque single-field struct)
// differ, so Go's nominal typing keeps them apart even though CapabilityID
// itself is not opaque against raw literals (see capabilitylint for that
// boundary).
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

func takesCapabilityID(_ ir.CapabilityID) {}

func invalidUse() {
	skill, err := ir.NewSkillID("pasture.skill.example/v1")
	if err != nil {
		panic(err)
	}
	takesCapabilityID(skill)
}

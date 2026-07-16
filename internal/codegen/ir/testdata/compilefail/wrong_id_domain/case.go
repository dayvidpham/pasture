// Package compilefail is an isolated compile-fail fixture: a validated
// SkillID must not satisfy a SemanticOperationID parameter — both are
// structurally identical (a private string field) but are distinct portable
// identity domains, and Go's nominal typing must keep them apart even though
// a reviewer skimming the shape alone could not tell them apart.
package compilefail

import "github.com/dayvidpham/pasture/internal/codegen/ir"

func takesID(_ ir.SemanticOperationID) {}

func invalidUse() {
	skill, err := ir.NewSkillID("pasture.skill.example/v1")
	if err != nil {
		panic(err)
	}
	takesID(skill)
}

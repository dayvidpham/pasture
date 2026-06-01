// White-box tests for ValidateGlobalIds (SLICE-2).
//
// Each test drives validateGlobalIdsFrom with a controlled fixture so the
// real package-level registries are never mutated. The tests are designed to
// fail until the full validator implementation lands (L3).
//
// Coverage:
//   - Duplicate ID in each namespace (6 namespaces × 1 test each)
//   - Unresolved FragmentId marker (ProseSection + BehaviorSpec)
//   - Invalid fragment (no payload / both payloads / kind mismatch)
//   - Clean fixture → nil
//   - Real registry (post rev-vote-options dedup) → nil
package codegen

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/types"
)

// ─── Fixture helpers ──────────────────────────────────────────────────────────

// minimalRoleSpecs returns a role-specs map with one role and no behaviors.
func minimalRoleSpecs() map[types.RoleId]RoleSpec {
	return map[types.RoleId]RoleSpec{
		"test-role": {Id: "test-role", Behaviors: nil},
	}
}

// minimalHandoffSpecs returns a handoff-specs map with one entry.
func minimalHandoffSpecs() map[string]HandoffSpec {
	return map[string]HandoffSpec{
		"h-test": {Id: "h-test"},
	}
}

// minimalCommandSpecs returns a command-specs map with one entry.
func minimalCommandSpecs() map[string]CommandSpec {
	return map[string]CommandSpec{
		"cmd-test": {Id: "cmd-test"},
	}
}

// minimalBodySpecs returns a SkillBodySpecs map with one skill that has
// one inline ProseSection and no FragmentId markers.
func minimalBodySpecs(prosId string) map[string]SkillBody {
	return map[string]SkillBody{
		"test-skill": {
			Sections: []ProseSection{{Id: prosId, Title: "T", Content: "C"}},
		},
	}
}

// emptyBodySpecs returns an empty SkillBodySpecs (no skills).
func emptyBodySpecs() map[string]SkillBody {
	return map[string]SkillBody{}
}

// minimalFragmentSpecs returns a fragment specs map with one valid prose fragment.
func minimalFragmentSpecs(id FragmentId) map[FragmentId]SharedFragment {
	p := ProseSection{Id: string(id), Title: "T", Content: "C"}
	return map[FragmentId]SharedFragment{
		id: {Id: id, Kind: FragmentKindProse, Prose: &p},
	}
}

// emptyFragmentSpecs returns an empty fragment specs map.
func emptyFragmentSpecs() map[FragmentId]SharedFragment {
	return map[FragmentId]SharedFragment{}
}

// emptyAllFragmentIds returns an empty AllFragmentIds slice (no constants declared).
func emptyAllFragmentIds() []FragmentId { return []FragmentId{} }

// allFragmentIdsFor returns an AllFragmentIds slice for the given ids.
func allFragmentIdsFor(ids ...FragmentId) []FragmentId { return ids }

// allFragmentIdsForFragSpecs extracts the sorted keys of a fragment specs map
// as the matching AllFragmentIds slice. Used in tests where parity must pass
// to reach the structural or resolution check under test.
func allFragmentIdsForFragSpecs(m map[FragmentId]SharedFragment) []FragmentId {
	return sortedFragmentIds(m)
}

// ─── Collision tests: one per namespace ──────────────────────────────────────

// TestValidateGlobalIDs_DuplicateInAgentNamespace verifies that two roles with
// the same RoleId key produce an actionable IDCollision error naming "agent".
func TestValidateGlobalIDs_DuplicateInAgentNamespace(t *testing.T) {
	// Inject a body-spec whose inline id collides with an agent id.
	roleSpecs := map[types.RoleId]RoleSpec{
		"my-agent": {Id: "my-agent"},
	}
	bodySpecs := map[string]SkillBody{
		"test-skill": {
			Sections: []ProseSection{{Id: "my-agent", Title: "T", Content: "C"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), roleSpecs, minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err, "collision between agent id and inline id must error")

	var collision *IDCollision
	require.True(t, errors.As(err, &collision), "error must be *IDCollision")
	assert.Equal(t, "my-agent", collision.Id, "collision id")
	assert.Contains(t, err.Error(), "my-agent", "error must name the duplicate id")
	assert.Contains(t, err.Error(), "agent", "error must name the namespace")
	assert.Contains(t, err.Error(), "SkillBodySpecs inline", "error must name second namespace")
}

// TestValidateGlobalIDs_DuplicateInRoleBehaviorNamespace verifies that a
// role-behavior B-* ID colliding with an inline skill-body ID is detected.
func TestValidateGlobalIDs_DuplicateInRoleBehaviorNamespace(t *testing.T) {
	roleSpecs := map[types.RoleId]RoleSpec{
		"role-a": {Id: "role-a", Behaviors: []BehaviorSpec{
			{Id: "B-shared-behavior", Given: "g", When: "w", Then: "t"},
		}},
	}
	bodySpecs := map[string]SkillBody{
		"skill-x": {
			Behaviors: []BehaviorSpec{{Id: "B-shared-behavior", Given: "g2", When: "w2", Then: "t2"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), roleSpecs, minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "B-shared-behavior", collision.Id)
	assert.Contains(t, err.Error(), "B-shared-behavior")
	assert.Contains(t, err.Error(), "role-behavior")
}

// TestValidateGlobalIDs_DuplicateInHandoffNamespace verifies that a handoff id
// colliding with an inline skill-body ID is detected.
func TestValidateGlobalIDs_DuplicateInHandoffNamespace(t *testing.T) {
	handoffSpecs := map[string]HandoffSpec{
		"h-collision": {Id: "h-collision"},
	}
	bodySpecs := map[string]SkillBody{
		"skill-y": {
			Sections: []ProseSection{{Id: "h-collision", Title: "T", Content: "C"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), handoffSpecs, minimalCommandSpecs())
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "h-collision", collision.Id)
	assert.Contains(t, err.Error(), "handoff")
}

// TestValidateGlobalIDs_DuplicateInCommandNamespace verifies that a command id
// colliding with an inline skill-body ID is detected.
func TestValidateGlobalIDs_DuplicateInCommandNamespace(t *testing.T) {
	commandSpecs := map[string]CommandSpec{
		"cmd-collision": {Id: "cmd-collision"},
	}
	bodySpecs := map[string]SkillBody{
		"skill-z": {
			Sections: []ProseSection{{Id: "cmd-collision", Title: "T", Content: "C"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), minimalHandoffSpecs(), commandSpecs)
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "cmd-collision", collision.Id)
	assert.Contains(t, err.Error(), "command")
}

// TestValidateGlobalIDs_DuplicateInFragmentNamespace verifies that a fragment id
// colliding with an inline skill-body ID is detected.
func TestValidateGlobalIDs_DuplicateInFragmentNamespace(t *testing.T) {
	proseFrag := ProseSection{Id: "shared-frag", Title: "T", Content: "C"}
	fragmentSpecs := map[FragmentId]SharedFragment{
		"shared-frag": {Id: "shared-frag", Kind: FragmentKindProse, Prose: &proseFrag},
	}
	bodySpecs := map[string]SkillBody{
		"skill-a": {
			// Inline (non-marker) section whose id collides with the fragment id.
			Sections: []ProseSection{{Id: "shared-frag", Title: "T", Content: "C"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, fragmentSpecs, allFragmentIdsFor("shared-frag"), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "shared-frag", collision.Id)
	assert.Contains(t, err.Error(), "SharedFragmentSpecs")
}

// TestValidateGlobalIDs_DuplicateInlineAcrossSkills verifies that the same ID
// appearing in two different skills' inline sections is caught.
func TestValidateGlobalIDs_DuplicateInlineAcrossSkills(t *testing.T) {
	bodySpecs := map[string]SkillBody{
		"skill-alpha": {
			Sections: []ProseSection{{Id: "dup-section", Title: "T", Content: "C"}},
		},
		"skill-beta": {
			Sections: []ProseSection{{Id: "dup-section", Title: "T2", Content: "C2"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "dup-section", collision.Id)
	assert.Contains(t, err.Error(), "SkillBodySpecs inline")
	// Both location strings must appear in the error.
	assert.True(t,
		strings.Contains(err.Error(), "skill-alpha") || strings.Contains(err.Error(), "skill-beta"),
		"error must name at least one of the two skills",
	)
}

// ─── Unresolved marker tests ─────────────────────────────────────────────────

// TestValidateGlobalIDs_UnresolvedProseSectionMarker verifies that a ProseSection
// marker whose FragmentId is absent from SharedFragmentSpecs returns an
// UnresolvedMarker error.
func TestValidateGlobalIDs_UnresolvedProseSectionMarker(t *testing.T) {
	bodySpecs := map[string]SkillBody{
		"skill-with-marker": {
			Sections: []ProseSection{fragRef("frag--does-not-exist")},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var marker *UnresolvedMarker
	require.True(t, errors.As(err, &marker), "error must be *UnresolvedMarker")
	assert.Equal(t, FragmentId("frag--does-not-exist"), marker.FragmentId)
	assert.Equal(t, "skill-with-marker", marker.ConsumerSkill)
	assert.Equal(t, "ProseSection", marker.MarkerKind)
	assert.Contains(t, err.Error(), "frag--does-not-exist")
	assert.Contains(t, err.Error(), "skill-with-marker")
}

// TestValidateGlobalIDs_UnresolvedBehaviorMarker verifies that a BehaviorSpec
// marker whose FragmentId is absent from SharedFragmentSpecs returns an error.
func TestValidateGlobalIDs_UnresolvedBehaviorMarker(t *testing.T) {
	bodySpecs := map[string]SkillBody{
		"skill-b": {
			Behaviors: []BehaviorSpec{behaviorRef("frag--missing-behavior")},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var marker *UnresolvedMarker
	require.True(t, errors.As(err, &marker))
	assert.Equal(t, FragmentId("frag--missing-behavior"), marker.FragmentId)
	assert.Equal(t, "BehaviorSpec", marker.MarkerKind)
}

// ─── Invalid fragment tests ───────────────────────────────────────────────────

// TestValidateGlobalIDs_FragmentNoPayload verifies that a fragment with neither
// Prose nor Behavior set returns an InvalidFragment error.
func TestValidateGlobalIDs_FragmentNoPayload(t *testing.T) {
	fragmentSpecs := map[FragmentId]SharedFragment{
		"frag-empty": {Id: "frag-empty", Kind: FragmentKindProse, Prose: nil},
	}

	err := validateGlobalIdsFrom(emptyBodySpecs(), fragmentSpecs, allFragmentIdsForFragSpecs(fragmentSpecs), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var inv *InvalidFragment
	require.True(t, errors.As(err, &inv))
	assert.Equal(t, FragmentId("frag-empty"), inv.FragmentId)
	assert.Contains(t, err.Error(), "frag-empty")
}

// TestValidateGlobalIDs_FragmentBothPayloads verifies that a fragment with both
// Prose and Behavior set returns an InvalidFragment error.
func TestValidateGlobalIDs_FragmentBothPayloads(t *testing.T) {
	p := ProseSection{Id: "fp", Title: "T", Content: "C"}
	b := BehaviorSpec{Id: "fb", Given: "g", When: "w", Then: "t"}
	fragmentSpecs := map[FragmentId]SharedFragment{
		"frag-both": {Id: "frag-both", Kind: FragmentKindProse, Prose: &p, Behavior: &b},
	}

	err := validateGlobalIdsFrom(emptyBodySpecs(), fragmentSpecs, allFragmentIdsForFragSpecs(fragmentSpecs), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var inv *InvalidFragment
	require.True(t, errors.As(err, &inv))
	assert.Equal(t, FragmentId("frag-both"), inv.FragmentId)
}

// TestValidateGlobalIDs_FragmentKindMismatch verifies that a fragment where Kind
// does not match the set payload returns an InvalidFragment error.
func TestValidateGlobalIDs_FragmentKindMismatch(t *testing.T) {
	b := BehaviorSpec{Id: "fb2", Given: "g", When: "w", Then: "t"}
	fragmentSpecs := map[FragmentId]SharedFragment{
		// Kind says Prose but Behavior is set, not Prose.
		"frag-mismatch": {Id: "frag-mismatch", Kind: FragmentKindProse, Behavior: &b},
	}

	err := validateGlobalIdsFrom(emptyBodySpecs(), fragmentSpecs, allFragmentIdsForFragSpecs(fragmentSpecs), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var inv *InvalidFragment
	require.True(t, errors.As(err, &inv))
	assert.Equal(t, FragmentId("frag-mismatch"), inv.FragmentId)
}

// ─── Happy path ───────────────────────────────────────────────────────────────

// TestValidateGlobalIDs_CleanFixture verifies that a fully disjoint fixture
// with one entry per namespace returns nil.
func TestValidateGlobalIDs_CleanFixture(t *testing.T) {
	p := ProseSection{Id: "frag--clean", Title: "T", Content: "C"}
	fragmentSpecs := map[FragmentId]SharedFragment{
		"frag--clean": {Id: "frag--clean", Kind: FragmentKindProse, Prose: &p},
	}
	roleSpecs := map[types.RoleId]RoleSpec{
		"clean-role": {Id: "clean-role", Behaviors: []BehaviorSpec{
			{Id: "B-clean-behavior", Given: "g", When: "w", Then: "t"},
		}},
	}
	handoffSpecs := map[string]HandoffSpec{
		"h-clean": {Id: "h-clean"},
	}
	commandSpecs := map[string]CommandSpec{
		"cmd-clean": {Id: "cmd-clean"},
	}
	bodySpecs := map[string]SkillBody{
		"clean-skill": {
			Sections:  []ProseSection{fragRef("frag--clean")}, // marker — no inline id
			Behaviors: []BehaviorSpec{{Id: "B-inline-only", Given: "g2", When: "w2", Then: "t2"}},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, fragmentSpecs, allFragmentIdsForFragSpecs(fragmentSpecs), roleSpecs, handoffSpecs, commandSpecs)
	assert.NoError(t, err, "clean fixture must return nil")
}

// TestValidateGlobalIDs_NestedSubsectionDupDetected verifies that a duplicate id
// hidden inside a ProseSection.Subsections slice is still caught.
func TestValidateGlobalIDs_NestedSubsectionDupDetected(t *testing.T) {
	bodySpecs := map[string]SkillBody{
		"outer": {
			Sections: []ProseSection{
				{Id: "parent-sec", Title: "Parent", Subsections: []ProseSection{
					{Id: "nested-dup", Title: "Sub", Content: "c"},
				}},
			},
		},
		"other-skill": {
			Sections: []ProseSection{
				{Id: "nested-dup", Title: "Collision", Content: "c2"},
			},
		},
	}

	err := validateGlobalIdsFrom(bodySpecs, emptyFragmentSpecs(), emptyAllFragmentIds(), minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err)

	var collision *IDCollision
	require.True(t, errors.As(err, &collision))
	assert.Equal(t, "nested-dup", collision.Id)
}

// TestValidateGlobalIDs_RealRegistryGreenAfterDedup verifies that the real
// package-level registries (SkillBodySpecs, SharedFragmentSpecs, RoleSpecs,
// HandoffSpecs, CommandSpecs) pass ValidateGlobalIds with zero errors AFTER
// the rev-vote-options dedup is applied.
//
// This is the golden-path acceptance test: if the real registry has any
// remaining duplicate, this test surfaces it immediately.
func TestValidateGlobalIDs_RealRegistryGreenAfterDedup(t *testing.T) {
	err := ValidateGlobalIds()
	assert.NoError(t, err,
		"the real codegen registry must pass ValidateGlobalIds after rev-vote-options dedup; "+
			"if this fails, either the dedup is incomplete or a new collision was introduced")
}

// ─── Parity tests ─────────────────────────────────────────────────────────────

// TestValidateGlobalIds_ParityOrphanConstant verifies that a FragmentId constant
// listed in AllFragmentIds but absent from SharedFragmentSpecs triggers a
// FragmentParityError with OrphanConstant set.
func TestValidateGlobalIds_ParityOrphanConstant(t *testing.T) {
	// allFragmentIds declares a constant that has no matching spec entry.
	allFragmentIds := []FragmentId{"frag--declared-but-no-spec"}
	fragmentSpecs := emptyFragmentSpecs() // empty — no spec for the constant

	err := validateGlobalIdsFrom(emptyBodySpecs(), fragmentSpecs, allFragmentIds,
		minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err, "orphan constant must produce an error")

	var parityErr *FragmentParityError
	require.True(t, errors.As(err, &parityErr), "error must be *FragmentParityError")
	assert.Equal(t, FragmentId("frag--declared-but-no-spec"), parityErr.OrphanConstant)
	assert.Contains(t, err.Error(), "frag--declared-but-no-spec")
	assert.Contains(t, err.Error(), "AllFragmentIds")
}

// TestValidateGlobalIds_ParityOrphanSpec verifies that a SharedFragmentSpecs
// entry whose key is absent from AllFragmentIds triggers a FragmentParityError
// with OrphanSpec set.
func TestValidateGlobalIds_ParityOrphanSpec(t *testing.T) {
	p := ProseSection{Id: "frag--spec-but-no-constant", Title: "T", Content: "C"}
	fragmentSpecs := map[FragmentId]SharedFragment{
		"frag--spec-but-no-constant": {
			Id:    "frag--spec-but-no-constant",
			Kind:  FragmentKindProse,
			Prose: &p,
		},
	}
	allFragmentIds := emptyAllFragmentIds() // no constants declared

	err := validateGlobalIdsFrom(emptyBodySpecs(), fragmentSpecs, allFragmentIds,
		minimalRoleSpecs(), minimalHandoffSpecs(), minimalCommandSpecs())
	require.Error(t, err, "orphan spec must produce an error")

	var parityErr *FragmentParityError
	require.True(t, errors.As(err, &parityErr), "error must be *FragmentParityError")
	assert.Equal(t, FragmentId("frag--spec-but-no-constant"), parityErr.OrphanSpec)
	assert.Contains(t, err.Error(), "frag--spec-but-no-constant")
	assert.Contains(t, err.Error(), "SharedFragmentSpecs")
}

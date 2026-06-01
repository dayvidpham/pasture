// White-box tests for the SharedFragment registry mechanism (SLICE-1).
//
// These tests exercise the production code paths directly:
//   - fragRef / behaviorRef helper constructors
//   - resolveBodyFragments (marker resolution with an in-test fixture registry)
//   - FragmentToOwnerRefs (deterministic inversion of SkillBodySpecs)
//
// No production registries are mutated; fixture registries are passed to
// resolveBodyFragments directly via the registry parameter.
package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Helper constructors ──────────────────────────────────────────────────────

// TestFragRef_MarkerOnly verifies that fragRef returns a ProseSection whose
// only non-zero field is FragRef.
func TestFragRef_MarkerOnly(t *testing.T) {
	ref := fragRef("my-frag")
	assert.Equal(t, FragmentId("my-frag"), ref.FragRef, "FragRef must be set")
	assert.Empty(t, ref.Id, "ID must be zero")
	assert.Empty(t, ref.Title, "Title must be zero")
	assert.Empty(t, ref.Content, "Content must be zero")
	assert.Empty(t, ref.Subsections, "Subsections must be zero")
}

// TestBehaviorRef_MarkerOnly verifies that behaviorRef returns a BehaviorSpec
// whose only non-zero field is FragRef.
func TestBehaviorRef_MarkerOnly(t *testing.T) {
	ref := behaviorRef("my-beh")
	assert.Equal(t, FragmentId("my-beh"), ref.FragRef, "FragRef must be set")
	assert.Empty(t, ref.Id, "ID must be zero")
	assert.Empty(t, ref.Given, "Given must be zero")
	assert.Empty(t, ref.When, "When must be zero")
	assert.Empty(t, ref.Then, "Then must be zero")
	assert.Empty(t, ref.ShouldNot, "ShouldNot must be zero")
}

// ─── Marker resolution ────────────────────────────────────────────────────────

// fixtureRegistry returns a minimal SharedFragmentSpecs registry for testing.
// It contains one prose fragment ("frag-prose") and one behavior fragment
// ("frag-behavior") with recognisable payloads.
func fixtureRegistry() map[FragmentId]SharedFragment {
	proseFrag := ProseSection{
		Id:      "frag-prose",
		Title:   "Shared Prose Title",
		Content: "Shared prose content — byte-identical across all consumers.",
	}
	behFrag := BehaviorSpec{
		Id:        "frag-behavior",
		Given:     "a shared behavior",
		When:      "referenced by two consumers",
		Then:      "both render byte-identical output",
		ShouldNot: "diverge",
	}
	return map[FragmentId]SharedFragment{
		"frag-prose": {
			Id:    "frag-prose",
			Kind:  FragmentKindProse,
			Prose: &proseFrag,
		},
		"frag-behavior": {
			Id:       "frag-behavior",
			Kind:     FragmentKindBehavior,
			Behavior: &behFrag,
		},
	}
}

// TestResolveBodyFragments_ProseSectionByteIdentical verifies that two
// consumers referencing the same prose fragment via markers resolve to
// byte-identical ProseSection payloads.
func TestResolveBodyFragments_ProseSectionByteIdentical(t *testing.T) {
	registry := fixtureRegistry()

	// Two consumers: each has one prose marker + one non-marker section.
	sectionsA := []ProseSection{
		{Id: "unique-a", Title: "Unique to A", Content: "Content A"},
		fragRef("frag-prose"),
	}
	sectionsB := []ProseSection{
		fragRef("frag-prose"),
		{Id: "unique-b", Title: "Unique to B", Content: "Content B"},
	}

	resolvedA, _, err := resolveBodyFragments(sectionsA, nil, registry, "consumer-a", "skills/consumer-a/SKILL.md")
	require.NoError(t, err, "consumer-a resolution must not error")
	resolvedB, _, err := resolveBodyFragments(sectionsB, nil, registry, "consumer-b", "skills/consumer-b/SKILL.md")
	require.NoError(t, err, "consumer-b resolution must not error")

	// Locate the resolved fragment in each output slice.
	var proseA, proseB *ProseSection
	for i := range resolvedA {
		if resolvedA[i].Id == "frag-prose" {
			proseA = &resolvedA[i]
		}
	}
	for i := range resolvedB {
		if resolvedB[i].Id == "frag-prose" {
			proseB = &resolvedB[i]
		}
	}

	require.NotNil(t, proseA, "consumer-a should have the resolved prose fragment")
	require.NotNil(t, proseB, "consumer-b should have the resolved prose fragment")

	// Byte-identity: both consumers received the same payload.
	assert.Equal(t, *proseA, *proseB,
		"resolved ProseSection must be byte-identical across both consumers")
}

// TestResolveBodyFragments_BehaviorByteIdentical verifies that two consumers
// referencing the same behavior fragment via markers resolve to byte-identical
// BehaviorSpec payloads.
func TestResolveBodyFragments_BehaviorByteIdentical(t *testing.T) {
	registry := fixtureRegistry()

	behaviorsA := []BehaviorSpec{
		{Id: "unique-a", Given: "only in A"},
		behaviorRef("frag-behavior"),
	}
	behaviorsB := []BehaviorSpec{
		behaviorRef("frag-behavior"),
		{Id: "unique-b", Given: "only in B"},
	}

	_, resolvedA, err := resolveBodyFragments(nil, behaviorsA, registry, "consumer-a", "skills/consumer-a/SKILL.md")
	require.NoError(t, err, "consumer-a behavior resolution must not error")
	_, resolvedB, err := resolveBodyFragments(nil, behaviorsB, registry, "consumer-b", "skills/consumer-b/SKILL.md")
	require.NoError(t, err, "consumer-b behavior resolution must not error")

	var behA, behB *BehaviorSpec
	for i := range resolvedA {
		if resolvedA[i].Id == "frag-behavior" {
			behA = &resolvedA[i]
		}
	}
	for i := range resolvedB {
		if resolvedB[i].Id == "frag-behavior" {
			behB = &resolvedB[i]
		}
	}

	require.NotNil(t, behA, "consumer-a should have the resolved behavior fragment")
	require.NotNil(t, behB, "consumer-b should have the resolved behavior fragment")

	assert.Equal(t, *behA, *behB,
		"resolved BehaviorSpec must be byte-identical across both consumers")
}

// TestResolveBodyFragments_Passthrough verifies that entries without a
// FragRef are passed through unchanged.
func TestResolveBodyFragments_Passthrough(t *testing.T) {
	registry := fixtureRegistry()

	sections := []ProseSection{
		{Id: "plain", Title: "Plain Section", Content: "No fragment reference"},
	}
	behaviors := []BehaviorSpec{
		{Id: "plain-beh", Given: "some input", When: "called", Then: "works"},
	}

	resolvedSections, resolvedBehaviors, err := resolveBodyFragments(sections, behaviors, registry, "consumer", "skills/consumer/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, sections, resolvedSections, "plain sections must pass through unchanged")
	assert.Equal(t, behaviors, resolvedBehaviors, "plain behaviors must pass through unchanged")
}

// TestResolveBodyFragments_EmptyRegistryNoOp verifies that with an empty
// SharedFragmentSpecs registry, resolution is a no-op (no markers in input
// means nothing to resolve; non-marker entries pass through).
func TestResolveBodyFragments_EmptyRegistryNoOp(t *testing.T) {
	emptyRegistry := map[FragmentId]SharedFragment{}

	sections := []ProseSection{
		{Id: "sec1", Title: "Section 1", Content: "Content 1"},
	}
	behaviors := []BehaviorSpec{
		{Id: "beh1", Given: "g", When: "w", Then: "t"},
	}

	resolvedSections, resolvedBehaviors, err := resolveBodyFragments(sections, behaviors, emptyRegistry, "consumer", "skills/consumer/SKILL.md")
	require.NoError(t, err, "empty registry resolution must not error")
	assert.Equal(t, sections, resolvedSections, "sections must be unchanged with empty registry")
	assert.Equal(t, behaviors, resolvedBehaviors, "behaviors must be unchanged with empty registry")
}

// TestResolveBodyFragments_UnresolvableMarkerError verifies that a marker with
// a FragRef not present in the registry returns an actionable error.
func TestResolveBodyFragments_UnresolvableMarkerError(t *testing.T) {
	registry := fixtureRegistry()

	sections := []ProseSection{
		fragRef("does-not-exist"),
	}

	_, _, err := resolveBodyFragments(sections, nil, registry, "consumer-x", "skills/consumer-x/SKILL.md")
	require.Error(t, err, "unresolvable fragment marker must return an error")
	// Error must be actionable: include the fragment ID, consumer skill, and hint.
	assert.Contains(t, err.Error(), "does-not-exist", "error must name the missing fragment ID")
	assert.Contains(t, err.Error(), "consumer-x", "error must name the consumer skill")
}

// TestResolveBodyFragments_NestedSubsections verifies that FragRef markers
// in ProseSection.Subsections are also resolved.
func TestResolveBodyFragments_NestedSubsections(t *testing.T) {
	registry := fixtureRegistry()

	// Top-level section contains a nested subsection that is a marker.
	sections := []ProseSection{
		{
			Id:    "parent",
			Title: "Parent Section",
			Subsections: []ProseSection{
				fragRef("frag-prose"),
			},
		},
	}

	resolvedSections, _, err := resolveBodyFragments(sections, nil, registry, "consumer", "skills/consumer/SKILL.md")
	require.NoError(t, err)
	require.Len(t, resolvedSections, 1, "one top-level section")
	require.Len(t, resolvedSections[0].Subsections, 1, "one nested subsection")
	assert.Equal(t, "frag-prose", resolvedSections[0].Subsections[0].Id,
		"nested subsection marker must be resolved to the prose fragment")
}

// ─── FragmentToOwnerRefs ──────────────────────────────────────────────────────

// TestFragmentToOwnerRefs_SortedDeterministic verifies that when two different
// SkillBody entries both reference the same fragment via markers,
// FragmentToOwnerRefs returns both owners in sorted order.
//
// This test drives FragmentToOwnerRefs via the REAL SkillBodySpecs (which
// contains no markers in SLICE-1, producing an empty result). To verify the
// inversion logic itself, we temporarily replace SkillBodySpecs with a fixture
// map containing known markers using a helper that drives the same inversion
// logic but against a supplied map.
//
// Note: because SkillBodySpecs is package-level and tests run in the same
// process, we exercise fragmentToOwnerRefsFrom (a helper that accepts a
// registry map) to avoid mutating the real SkillBodySpecs.
func TestFragmentToOwnerRefs_SortedDeterministic(t *testing.T) {
	// Fixture body map: two skills both reference "shared-frag" via a marker.
	fixtureBodyMap := map[string]SkillBody{
		"zebra-skill": {
			Sections: []ProseSection{fragRef("shared-frag")},
		},
		"alpha-skill": {
			Sections: []ProseSection{fragRef("shared-frag")},
		},
		"beta-skill": {
			Behaviors: []BehaviorSpec{behaviorRef("other-frag")},
		},
	}

	result := fragmentToOwnerRefsFrom(fixtureBodyMap)

	// "shared-frag" should list both consumers in sorted order.
	owners, ok := result["shared-frag"]
	require.True(t, ok, "shared-frag must appear in FragmentToOwnerRefs result")
	assert.Equal(t, []string{"alpha-skill", "zebra-skill"}, owners,
		"owners must be sorted alphabetically")

	// "other-frag" should list exactly one consumer.
	otherOwners, ok := result["other-frag"]
	require.True(t, ok, "other-frag must appear in FragmentToOwnerRefs result")
	assert.Equal(t, []string{"beta-skill"}, otherOwners)
}

// TestFragmentToOwnerRefs_RealRegistryHasRevVoteOptionsMarkers verifies that
// FragmentToOwnerRefs returns the expected owner map for the real SkillBodySpecs
// after the SLICE-2 rev-vote-options dedup: both "reviewer" and "reviewer-vote"
// reference frag--rev-vote-options via placement markers.
//
// NOTE: In SLICE-1, when SharedFragmentSpecs was empty and no markers existed,
// this map was expected to be empty. SLICE-2 introduces the first real markers.
func TestFragmentToOwnerRefs_RealRegistryHasRevVoteOptionsMarkers(t *testing.T) {
	result := FragmentToOwnerRefs()
	owners, ok := result[FragRevVoteOptions]
	require.True(t, ok,
		"FragmentToOwnerRefs must include FragRevVoteOptions after SLICE-2 dedup")
	assert.Equal(t, []string{"reviewer", "reviewer-vote"}, owners,
		"frag--rev-vote-options owners must be sorted: reviewer before reviewer-vote")
}

// TestFragmentToOwnerRefs_Deterministic verifies that calling FragmentToOwnerRefs
// twice returns equal maps (no random-map-iteration non-determinism).
func TestFragmentToOwnerRefs_Deterministic(t *testing.T) {
	fixtureBodyMap := map[string]SkillBody{
		"c-skill": {Sections: []ProseSection{fragRef("frag-x")}},
		"a-skill": {Sections: []ProseSection{fragRef("frag-x")}},
		"b-skill": {Behaviors: []BehaviorSpec{behaviorRef("frag-x")}},
	}

	result1 := fragmentToOwnerRefsFrom(fixtureBodyMap)
	result2 := fragmentToOwnerRefsFrom(fixtureBodyMap)

	require.Equal(t, result1, result2, "FragmentToOwnerRefs must be deterministic across calls")
	// Specifically: owners of "frag-x" must be sorted.
	owners := result1["frag-x"]
	assert.Equal(t, []string{"a-skill", "b-skill", "c-skill"}, owners,
		"owners must be alphabetically sorted")
}

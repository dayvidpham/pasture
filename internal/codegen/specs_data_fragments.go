// Canonical SharedFragment registry and helper constructors.
//
// This file declares the SharedFragmentSpecs map (empty in SLICE-1 —
// mechanism only, no dedup yet), fragRef/behaviorRef marker constructors, and
// FragmentToOwnerRefs, which inverts the consumer-keyed SkillBodySpecs to
// produce a map from fragment ID to sorted owner skill-dir keys.
//
// Mirror: ConstraintToRoleRefs() at context.go:577-593 inverts the
// consumer-keyed roleConstraints map; FragmentToOwnerRefs mirrors that
// pattern exactly, operating over SkillBodySpecs instead.
package codegen

import (
	"fmt"
	"sort"
)

// SharedFragmentSpecs is the canonical registry of reusable shared fragments.
// Keyed by fragment ID (must match the FragmentID field used in marker entries).
//
// SLICE-1: empty — mechanism only. SLICE-2 onwards will populate this map
// as actual fragments are extracted from SkillBodySpecs during dedup.
var SharedFragmentSpecs = map[string]SharedFragment{}

// fragRef returns a ProseSection placement marker that references the given
// fragment ID. All fields except FragmentID are left at their zero values;
// the pre-render resolution pass in skills.go replaces the marker with the
// actual ProseSection payload from SharedFragmentSpecs.
func fragRef(id string) ProseSection {
	return ProseSection{FragmentID: id}
}

// behaviorRef returns a BehaviorSpec placement marker that references the given
// fragment ID. All fields except FragmentID are left at their zero values;
// the pre-render resolution pass in skills.go replaces the marker with the
// actual BehaviorSpec payload from SharedFragmentSpecs.
func behaviorRef(id string) BehaviorSpec {
	return BehaviorSpec{FragmentID: id}
}

// FragmentToOwnerRefs returns a map from fragment ID to the sorted list of
// skill-dir keys (SkillBodySpecs keys) whose body sections or behaviors
// reference that fragment via a placement marker.
//
// It mirrors ConstraintToRoleRefs (context.go:577-593): iterate the
// consumer-keyed map, invert to fragment-keyed, sort owner slices for
// deterministic output.
func FragmentToOwnerRefs() map[string][]string {
	return fragmentToOwnerRefsFrom(SkillBodySpecs)
}

// fragmentToOwnerRefsFrom is the testable inner implementation of
// FragmentToOwnerRefs. It accepts any SkillBody map so tests can pass a
// fixture map without mutating the package-level SkillBodySpecs.
func fragmentToOwnerRefsFrom(bodySpecs map[string]SkillBody) map[string][]string {
	result := make(map[string][]string)
	for skillDir, body := range bodySpecs {
		// Collect fragment IDs referenced by top-level Sections.
		for _, sec := range body.Sections {
			if sec.FragmentID != "" {
				result[sec.FragmentID] = append(result[sec.FragmentID], skillDir)
			}
			// Collect fragment IDs referenced by nested Subsections.
			for _, sub := range sec.Subsections {
				if sub.FragmentID != "" {
					result[sub.FragmentID] = append(result[sub.FragmentID], skillDir)
				}
			}
		}
		// Collect fragment IDs referenced by Behaviors.
		for _, beh := range body.Behaviors {
			if beh.FragmentID != "" {
				result[beh.FragmentID] = append(result[beh.FragmentID], skillDir)
			}
		}
	}
	// Sort each owner slice for deterministic output.
	for fragID := range result {
		owners := result[fragID]
		sort.Strings(owners)
		result[fragID] = owners
	}
	return result
}

// resolveBodyFragments resolves placement markers in sections and behaviors
// using the provided registry. Entries without FragmentID pass through
// unchanged. Entries with FragmentID are replaced with the fragment payload
// from the registry. Nested ProseSection.Subsections are resolved recursively.
//
// Parameters:
//   - sections: top-level ProseSection slice (may contain markers)
//   - behaviors: BehaviorSpec slice (may contain markers)
//   - registry: fragment ID → SharedFragment map (use SharedFragmentSpecs in production)
//   - consumerSkill: skill-dir key for actionable error messages
//   - consumerFile: file path for actionable error messages
//
// Returns an actionable error if a marker references a fragment ID not present
// in the registry, describing what ID was missing, which consumer referenced
// it, where the consumer file lives, and how to fix it.
func resolveBodyFragments(
	sections []ProseSection,
	behaviors []BehaviorSpec,
	registry map[string]SharedFragment,
	consumerSkill, consumerFile string,
) ([]ProseSection, []BehaviorSpec, error) {
	resolvedSections, err := resolveProseSections(sections, registry, consumerSkill, consumerFile)
	if err != nil {
		return nil, nil, err
	}
	resolvedBehaviors, err := resolveBehaviors(behaviors, registry, consumerSkill, consumerFile)
	if err != nil {
		return nil, nil, err
	}
	return resolvedSections, resolvedBehaviors, nil
}

// resolveProseSections resolves fragment markers in a slice of ProseSection,
// including nested Subsections, using the provided registry.
func resolveProseSections(
	sections []ProseSection,
	registry map[string]SharedFragment,
	consumerSkill, consumerFile string,
) ([]ProseSection, error) {
	if len(sections) == 0 {
		return sections, nil
	}
	result := make([]ProseSection, 0, len(sections))
	for _, sec := range sections {
		if sec.FragmentID == "" {
			// Non-marker: resolve subsections recursively, then pass through.
			resolvedSubs, err := resolveProseSections(sec.Subsections, registry, consumerSkill, consumerFile)
			if err != nil {
				return nil, err
			}
			sec.Subsections = resolvedSubs
			result = append(result, sec)
			continue
		}
		// Marker: look up fragment in registry.
		frag, ok := registry[sec.FragmentID]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable prose marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a ProseSection with FragmentID=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindProse and a non-nil Prose payload",
				sec.FragmentID, consumerSkill, consumerFile, sec.FragmentID, sec.FragmentID,
			)
		}
		if frag.Kind != FragmentKindProse || frag.Prose == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not prose) or nil Prose in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindProse and a non-nil Prose pointer in SharedFragmentSpecs[%q]",
				sec.FragmentID, frag.Kind, consumerSkill, consumerFile, sec.FragmentID,
			)
		}
		result = append(result, *frag.Prose)
	}
	return result, nil
}

// resolveBehaviors resolves fragment markers in a slice of BehaviorSpec
// using the provided registry.
func resolveBehaviors(
	behaviors []BehaviorSpec,
	registry map[string]SharedFragment,
	consumerSkill, consumerFile string,
) ([]BehaviorSpec, error) {
	if len(behaviors) == 0 {
		return behaviors, nil
	}
	result := make([]BehaviorSpec, 0, len(behaviors))
	for _, beh := range behaviors {
		if beh.FragmentID == "" {
			result = append(result, beh)
			continue
		}
		// Marker: look up fragment in registry.
		frag, ok := registry[beh.FragmentID]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable behavior marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a BehaviorSpec with FragmentID=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindBehavior and a non-nil Behavior payload",
				beh.FragmentID, consumerSkill, consumerFile, beh.FragmentID, beh.FragmentID,
			)
		}
		if frag.Kind != FragmentKindBehavior || frag.Behavior == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not behavior) or nil Behavior in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindBehavior and a non-nil Behavior pointer in SharedFragmentSpecs[%q]",
				beh.FragmentID, frag.Kind, consumerSkill, consumerFile, beh.FragmentID,
			)
		}
		result = append(result, *frag.Behavior)
	}
	return result, nil
}

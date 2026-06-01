// Canonical SharedFragment registry and helper constructors.
//
// This file declares the SharedFragmentSpecs map, fragRef/behaviorRef marker
// constructors, and FragmentToOwnerRefs, which inverts the consumer-keyed
// SkillBodySpecs to produce a map from FragmentId to sorted owner skill-dir
// keys.
//
// Mirror: ConstraintToRoleRefs() at context.go inverts the consumer-keyed
// roleConstraints map; FragmentToOwnerRefs mirrors that pattern exactly,
// operating over SkillBodySpecs instead.
package codegen

import (
	"fmt"
	"sort"
)

// SharedFragmentSpecs is the canonical registry of reusable shared fragments.
// Keyed by FragmentId typed constant (must match the FragRef field used in
// placement marker entries).
//
// SLICE-2 adds: FragRevVoteOptions (green-enabler; resolves the sole
// pre-existing same-id collision between reviewer-vote and reviewer skill
// bodies). Canonical content per D3 (UAT-ratified): ACCEPT row = "All review
// criteria satisfied; no BLOCKER items". Both consumer bodies carry fragRef
// markers. Set keys MUST equal AllFragmentIds (validated by ValidateGlobalIds).
var SharedFragmentSpecs = map[FragmentId]SharedFragment{
	FragRevVoteOptions: func() SharedFragment {
		prose := ProseSection{
			Id:    string(FragRevVoteOptions),
			Title: "Vote Options",
			Content: `| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.`,
		}
		return SharedFragment{
			Id:    FragRevVoteOptions,
			Kind:  FragmentKindProse,
			Prose: &prose,
		}
	}(),
}

// fragRef returns a ProseSection placement marker that references the given
// fragment. All fields except FragRef are left at their zero values; the
// pre-render resolution pass in skills.go replaces the marker with the actual
// ProseSection payload from SharedFragmentSpecs.
func fragRef(id FragmentId) ProseSection {
	return ProseSection{FragRef: id}
}

// behaviorRef returns a BehaviorSpec placement marker that references the given
// fragment. All fields except FragRef are left at their zero values; the
// pre-render resolution pass in skills.go replaces the marker with the actual
// BehaviorSpec payload from SharedFragmentSpecs.
func behaviorRef(id FragmentId) BehaviorSpec {
	return BehaviorSpec{FragRef: id}
}

// FragmentToOwnerRefs returns a map from FragmentId to the sorted list of
// skill-dir keys (SkillBodySpecs keys) whose body sections or behaviors
// reference that fragment via a placement marker.
//
// It mirrors ConstraintToRoleRefs: iterate the consumer-keyed map, invert to
// fragment-keyed, sort owner slices for deterministic output.
func FragmentToOwnerRefs() map[FragmentId][]string {
	return fragmentToOwnerRefsFrom(SkillBodySpecs)
}

// fragmentToOwnerRefsFrom is the testable inner implementation of
// FragmentToOwnerRefs. It accepts any SkillBody map so tests can pass a
// fixture map without mutating the package-level SkillBodySpecs.
func fragmentToOwnerRefsFrom(bodySpecs map[string]SkillBody) map[FragmentId][]string {
	result := make(map[FragmentId][]string)
	for skillDir, body := range bodySpecs {
		// Collect fragment refs from top-level Sections (and their Subsections).
		for _, sec := range body.Sections {
			if sec.FragRef != "" {
				result[sec.FragRef] = append(result[sec.FragRef], skillDir)
			}
			for _, sub := range sec.Subsections {
				if sub.FragRef != "" {
					result[sub.FragRef] = append(result[sub.FragRef], skillDir)
				}
			}
		}
		// Collect fragment refs from Behaviors.
		for _, beh := range body.Behaviors {
			if beh.FragRef != "" {
				result[beh.FragRef] = append(result[beh.FragRef], skillDir)
			}
		}
	}
	// Sort each owner slice for deterministic output.
	for fragId := range result {
		owners := result[fragId]
		sort.Strings(owners)
		result[fragId] = owners
	}
	return result
}

// resolveBodyFragments resolves placement markers in sections and behaviors
// using the provided registry. Entries without FragRef pass through unchanged.
// Entries with FragRef are replaced with the fragment payload from the registry.
// Nested ProseSection.Subsections are resolved recursively.
//
// Parameters:
//   - sections: top-level ProseSection slice (may contain markers)
//   - behaviors: BehaviorSpec slice (may contain markers)
//   - registry: fragment registry (use SharedFragmentSpecs in production)
//   - consumerSkill: skill-dir key for actionable error messages
//   - consumerFile: file path for actionable error messages
//
// Returns an actionable error if a marker references a FragmentId not present
// in the registry.
func resolveBodyFragments(
	sections []ProseSection,
	behaviors []BehaviorSpec,
	registry map[FragmentId]SharedFragment,
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
	registry map[FragmentId]SharedFragment,
	consumerSkill, consumerFile string,
) ([]ProseSection, error) {
	if len(sections) == 0 {
		return sections, nil
	}
	result := make([]ProseSection, 0, len(sections))
	for _, sec := range sections {
		if sec.FragRef == "" {
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
		frag, ok := registry[sec.FragRef]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable prose marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a ProseSection with FragRef=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindProse and a non-nil Prose payload",
				sec.FragRef, consumerSkill, consumerFile, sec.FragRef, sec.FragRef,
			)
		}
		if frag.Kind != FragmentKindProse || frag.Prose == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not prose) or nil Prose in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindProse and a non-nil Prose pointer in SharedFragmentSpecs[%q]",
				sec.FragRef, frag.Kind, consumerSkill, consumerFile, sec.FragRef,
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
	registry map[FragmentId]SharedFragment,
	consumerSkill, consumerFile string,
) ([]BehaviorSpec, error) {
	if len(behaviors) == 0 {
		return behaviors, nil
	}
	result := make([]BehaviorSpec, 0, len(behaviors))
	for _, beh := range behaviors {
		if beh.FragRef == "" {
			result = append(result, beh)
			continue
		}
		// Marker: look up fragment in registry.
		frag, ok := registry[beh.FragRef]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: unresolvable behavior marker %q in skill %q — "+
					"where: %s — "+
					"when: pre-render fragment resolution — "+
					"what it means: a BehaviorSpec with FragRef=%q references a fragment "+
					"that does not exist in SharedFragmentSpecs — "+
					"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
					"with Kind=FragmentKindBehavior and a non-nil Behavior payload",
				beh.FragRef, consumerSkill, consumerFile, beh.FragRef, beh.FragRef,
			)
		}
		if frag.Kind != FragmentKindBehavior || frag.Behavior == nil {
			return nil, fmt.Errorf(
				"codegen.resolveBodyFragments: fragment %q has Kind=%q (not behavior) or nil Behavior in skill %q — "+
					"where: %s — "+
					"fix: set Kind=FragmentKindBehavior and a non-nil Behavior pointer in SharedFragmentSpecs[%q]",
				beh.FragRef, frag.Kind, consumerSkill, consumerFile, beh.FragRef,
			)
		}
		result = append(result, *frag.Behavior)
	}
	return result, nil
}

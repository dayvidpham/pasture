// Package codegen — global-ID uniqueness enforcement (SLICE-2, URD R5+R7).
//
// ValidateGlobalIds checks that every ID-bearing spec in the codegen
// registries is globally unique across ALL namespaces:
//
//   - SkillBodySpecs inline section / behavior IDs (skip FragRef-only markers)
//   - SharedFragmentSpecs fragment IDs
//   - agent IDs        (RoleSpecs map keys, e.g. "supervisor", "worker")
//   - role-behavior IDs (RoleSpec.Behaviors[*].Id, e.g. "B-arch-elicit")
//   - handoff IDs      (HandoffSpecs map keys, e.g. "h1" … "h6")
//   - command IDs      (CommandSpecs map keys, e.g. "cmd-worker")
//
// It also verifies structural validity of SharedFragmentSpecs entries:
//   - every FragRef placement marker in SkillBodySpecs resolves
//   - every fragment has exactly one payload (Prose XOR Behavior)
//
// Parity check (AMENDMENT-2): set(AllFragmentIds) must equal
// set(keys(SharedFragmentSpecs)), catching both orphan directions (a declared
// constant with no spec, or a spec with no constant).
//
// Call site: tools/codegen/main.go — wired after the generators so that
// go generate hard-fails on the first violation before writing any file.
package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Error types ──────────────────────────────────────────────────────────────

// IDCollision describes a global-ID uniqueness violation: the same string ID
// appears in two distinct locations across the codegen registries.
//
// All fields are always populated so the error message is self-contained.
type IDCollision struct {
	// Id is the duplicate identifier string (e.g. "rev-vote-options").
	Id string

	// Namespace identifies which registry the collision spans
	// (e.g. "SkillBodySpecs inline", "SharedFragmentSpecs", "role-behavior").
	Namespace string

	// LocationA is the first occurrence description (registry/skill-dir).
	LocationA string

	// LocationB is the second occurrence description.
	LocationB string
}

// Error returns an actionable message: what/why/where/when/meaning/fix.
func (e *IDCollision) Error() string {
	return fmt.Sprintf(
		"codegen.ValidateGlobalIds: duplicate ID %q in namespace %q — "+
			"what: two specs share the same ID string; "+
			"why: global-ID uniqueness is required to prevent ambiguous cross-references; "+
			"first occurrence: %s; second occurrence: %s; "+
			"when: codegen pre-flight validation (go generate); "+
			"what it means: generated SKILL.md files would contain ambiguous cross-references; "+
			"fix: either rename one entry to a distinct ID, or extract the shared content into "+
			"SharedFragmentSpecs with a canonical frag--* ID and replace both inline entries "+
			"with fragRef/behaviorRef markers pointing at that fragment",
		e.Id, e.Namespace, e.LocationA, e.LocationB,
	)
}

// UnresolvedMarker describes a FragRef placement marker that references a
// fragment not present in SharedFragmentSpecs.
type UnresolvedMarker struct {
	// FragRef is the unresolvable marker reference.
	FragRef FragmentId

	// ConsumerSkill is the SkillBodySpecs key where the marker appears.
	ConsumerSkill string

	// MarkerKind is "ProseSection" or "BehaviorSpec".
	MarkerKind string
}

// Error returns an actionable message.
func (e *UnresolvedMarker) Error() string {
	return fmt.Sprintf(
		"codegen.ValidateGlobalIds: unresolved %s marker %q in skill %q — "+
			"what: a placement marker references a fragment that does not exist in SharedFragmentSpecs; "+
			"why: every FragRef marker must resolve before rendering; "+
			"where: SkillBodySpecs[%q] contains a %s with FragRef=%q; "+
			"when: codegen pre-flight validation (go generate); "+
			"what it means: go generate would error at render time; "+
			"fix: add fragment %q to SharedFragmentSpecs in specs_data_fragments.go "+
			"with the appropriate Kind and non-nil payload",
		e.MarkerKind, e.FragRef, e.ConsumerSkill,
		e.ConsumerSkill, e.MarkerKind, e.FragRef,
		e.FragRef,
	)
}

// InvalidFragment describes a SharedFragmentSpecs entry that violates the
// exactly-one-payload rule (must have Prose XOR Behavior, never both/neither).
type InvalidFragment struct {
	// FragRef is the offending fragment's map key.
	FragRef FragmentId

	// Reason describes the specific violation.
	Reason string
}

// Error returns an actionable message.
func (e *InvalidFragment) Error() string {
	return fmt.Sprintf(
		"codegen.ValidateGlobalIds: invalid fragment %q in SharedFragmentSpecs — "+
			"what: %s; "+
			"why: each SharedFragment must carry exactly one payload (Prose XOR Behavior); "+
			"where: SharedFragmentSpecs[%q] in specs_data_fragments.go; "+
			"when: codegen pre-flight validation (go generate); "+
			"fix: set exactly one of Prose or Behavior (never both, never neither) and set "+
			"Kind to the matching FragmentKind constant",
		e.FragRef, e.Reason, e.FragRef,
	)
}

// FragmentParityError describes a mismatch between AllFragmentIds (the declared
// typed constants) and the keys of SharedFragmentSpecs.
type FragmentParityError struct {
	// OrphanConstant is non-empty when a FragmentId constant has no matching
	// SharedFragmentSpecs entry.
	OrphanConstant FragmentId

	// OrphanSpec is non-empty when a SharedFragmentSpecs key has no matching
	// FragmentId constant in AllFragmentIds.
	OrphanSpec FragmentId
}

// Error returns an actionable message.
func (e *FragmentParityError) Error() string {
	if e.OrphanConstant != "" {
		return fmt.Sprintf(
			"codegen.ValidateGlobalIds: FragmentId constant %q has no matching entry in SharedFragmentSpecs — "+
				"what: AllFragmentIds contains a constant that is not backed by a SharedFragmentSpecs entry; "+
				"why: every declared FragmentId constant must have a corresponding fragment spec "+
				"(parity: set(AllFragmentIds) == set(keys(SharedFragmentSpecs))); "+
				"where: constant declared in specs.go, specs_data_fragments.go has no matching key; "+
				"when: codegen pre-flight validation (go generate); "+
				"fix: add a SharedFragmentSpecs entry with key %q, or remove the constant from AllFragmentIds",
			e.OrphanConstant, e.OrphanConstant,
		)
	}
	return fmt.Sprintf(
		"codegen.ValidateGlobalIds: SharedFragmentSpecs key %q has no matching FragmentId constant in AllFragmentIds — "+
			"what: SharedFragmentSpecs contains an entry whose key is not listed in AllFragmentIds; "+
			"why: every fragment spec must have a corresponding typed constant "+
			"(parity: set(AllFragmentIds) == set(keys(SharedFragmentSpecs))); "+
			"where: key %q in SharedFragmentSpecs in specs_data_fragments.go; "+
			"when: codegen pre-flight validation (go generate); "+
			"fix: add FragmentId constant %q to specs.go and include it in AllFragmentIds, "+
			"or remove the entry from SharedFragmentSpecs",
		e.OrphanSpec, e.OrphanSpec, e.OrphanSpec,
	)
}

// ─── Validator ────────────────────────────────────────────────────────────────

// ValidateGlobalIds validates global ID uniqueness, structural integrity, and
// AllFragmentIds↔SharedFragmentSpecs parity across all codegen registries.
// Returns nil on success; returns the first violation as an error on failure.
//
// Validation order (deterministic):
//  1. AllFragmentIds ↔ SharedFragmentSpecs parity
//  2. SharedFragmentSpecs: exactly-one-payload rule per fragment
//  3. Cross-namespace uniqueness: agent → role-behavior → handoff → command →
//     SharedFragmentSpecs → SkillBodySpecs inline IDs
//  4. FragRef marker resolution: every marker resolves in SharedFragmentSpecs
func ValidateGlobalIds() error {
	return validateGlobalIdsFrom(
		SkillBodySpecs,
		SharedFragmentSpecs,
		AllFragmentIds,
		RoleSpecs,
		HandoffSpecs,
		CommandSpecs,
	)
}

// validateGlobalIdsFrom is the testable inner implementation. It accepts
// the registry maps and the AllFragmentIds slice so tests can inject controlled
// fixtures without mutating package-level state.
func validateGlobalIdsFrom(
	bodySpecs map[string]SkillBody,
	fragmentSpecs map[FragmentId]SharedFragment,
	allFragmentIds []FragmentId,
	roleSpecs map[protocol.RoleId]RoleSpec,
	handoffSpecs map[string]HandoffSpec,
	commandSpecs map[string]CommandSpec,
) error {
	// ── 1. AllFragmentIds ↔ SharedFragmentSpecs parity ───────────────────────
	// Build a set from AllFragmentIds, check every key in fragmentSpecs has a
	// constant, and every constant has a spec.
	constantSet := make(map[FragmentId]bool, len(allFragmentIds))
	for _, c := range allFragmentIds {
		constantSet[c] = true
	}
	// Constant without spec?
	for _, c := range allFragmentIds {
		if _, ok := fragmentSpecs[c]; !ok {
			return &FragmentParityError{OrphanConstant: c}
		}
	}
	// Spec without constant?
	for _, fragId := range sortedFragmentIds(fragmentSpecs) {
		if !constantSet[fragId] {
			return &FragmentParityError{OrphanSpec: fragId}
		}
	}

	// ── 2. Fragment structural validity ──────────────────────────────────────
	for _, fragId := range sortedFragmentIds(fragmentSpecs) {
		frag := fragmentSpecs[fragId]
		hasProse := frag.Prose != nil
		hasBehavior := frag.Behavior != nil
		switch {
		case !hasProse && !hasBehavior:
			return &InvalidFragment{
				FragRef: fragId,
				Reason:  "both Prose and Behavior are nil (no payload)",
			}
		case hasProse && hasBehavior:
			return &InvalidFragment{
				FragRef: fragId,
				Reason:  "both Prose and Behavior are non-nil (ambiguous payload)",
			}
		case hasProse && frag.Kind != FragmentKindProse:
			return &InvalidFragment{
				FragRef: fragId,
				Reason:  fmt.Sprintf("Prose is set but Kind=%q (want %q)", frag.Kind, FragmentKindProse),
			}
		case hasBehavior && frag.Kind != FragmentKindBehavior:
			return &InvalidFragment{
				FragRef: fragId,
				Reason:  fmt.Sprintf("Behavior is set but Kind=%q (want %q)", frag.Kind, FragmentKindBehavior),
			}
		}
	}

	// ── 3. Cross-namespace ID uniqueness ─────────────────────────────────────
	// seen maps each id → "namespace\x00location" for the first occurrence seen.
	// All namespace IDs are compared as flat strings (FragmentId cast to string).
	seen := make(map[string]string)

	// register checks that id is not yet seen; returns IDCollision if it is.
	register := func(id, namespace, location string) error {
		if id == "" {
			return nil
		}
		if prev, exists := seen[id]; exists {
			parts := strings.SplitN(prev, "\x00", 2)
			prevNamespace, prevLocation := parts[0], parts[1]
			return &IDCollision{
				Id:        id,
				Namespace: namespace,
				LocationA: fmt.Sprintf("%s/%s", prevNamespace, prevLocation),
				LocationB: fmt.Sprintf("%s/%s", namespace, location),
			}
		}
		seen[id] = namespace + "\x00" + location
		return nil
	}

	// Agent IDs — RoleSpecs keys (e.g. "supervisor", "worker").
	for _, roleId := range sortedRoleIds(roleSpecs) {
		if err := register(string(roleId), "agent", string(roleId)); err != nil {
			return err
		}
	}

	// Role-behavior IDs — RoleSpec.Behaviors[*].Id (e.g. "B-arch-elicit").
	for _, roleId := range sortedRoleIds(roleSpecs) {
		roleSpec := roleSpecs[roleId]
		for _, beh := range roleSpec.Behaviors {
			if beh.Id == "" {
				continue
			}
			loc := fmt.Sprintf("RoleSpecs[%q].Behaviors", roleId)
			if err := register(beh.Id, "role-behavior", loc); err != nil {
				return err
			}
		}
	}

	// Handoff IDs — HandoffSpecs keys (e.g. "h1" … "h6").
	for _, handoffId := range sortedStringKeys(handoffSpecs) {
		if err := register(handoffId, "handoff", handoffId); err != nil {
			return err
		}
	}

	// Command IDs — CommandSpecs keys (e.g. "cmd-worker").
	for _, cmdId := range sortedStringKeys(commandSpecs) {
		if err := register(cmdId, "command", cmdId); err != nil {
			return err
		}
	}

	// SharedFragmentSpecs fragment IDs (e.g. "frag--rev-vote-options").
	// Cast to string for flat cross-namespace comparison.
	for _, fragId := range sortedFragmentIds(fragmentSpecs) {
		if err := register(string(fragId), "SharedFragmentSpecs", string(fragId)); err != nil {
			return err
		}
	}

	// SkillBodySpecs inline section and behavior IDs.
	// Also collect FragRef markers for resolution check (step 4).
	var markers []pendingMarkerRef

	for _, skillDir := range sortedStringKeys(bodySpecs) {
		body := bodySpecs[skillDir]

		// Recurse into Sections (including Subsections).
		if err := registerSectionIds(body.Sections, skillDir, register, &markers); err != nil {
			return err
		}

		// Behaviors.
		for _, beh := range body.Behaviors {
			if beh.FragRef != "" {
				markers = append(markers, pendingMarkerRef{beh.FragRef, skillDir, "BehaviorSpec"})
				continue
			}
			if beh.Id != "" {
				if err := register(beh.Id, "SkillBodySpecs inline", skillDir); err != nil {
					return err
				}
			}
		}
	}

	// ── 4. Marker resolution ─────────────────────────────────────────────────
	for _, m := range markers {
		if _, ok := fragmentSpecs[m.fragId]; !ok {
			return &UnresolvedMarker{
				FragRef:       m.fragId,
				ConsumerSkill: m.skill,
				MarkerKind:    m.kind,
			}
		}
	}

	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// pendingMarkerRef records a FragRef marker found in SkillBodySpecs that must
// be resolved against SharedFragmentSpecs in a later validation pass.
type pendingMarkerRef struct {
	fragId FragmentId
	skill  string
	kind   string // "ProseSection" or "BehaviorSpec"
}

// registerSectionIds recursively collects ProseSection IDs (and markers) from
// a section slice, including nested Subsections, calling register for each.
func registerSectionIds(
	sections []ProseSection,
	skillDir string,
	register func(id, namespace, location string) error,
	markers *[]pendingMarkerRef,
) error {
	for _, sec := range sections {
		if sec.FragRef != "" {
			*markers = append(*markers, pendingMarkerRef{sec.FragRef, skillDir, "ProseSection"})
			continue
		}
		if sec.Id != "" {
			if err := register(sec.Id, "SkillBodySpecs inline", skillDir); err != nil {
				return err
			}
		}
		if err := registerSectionIds(sec.Subsections, skillDir, register, markers); err != nil {
			return err
		}
	}
	return nil
}

// sortedStringKeys returns a sorted slice of string keys from a map[string]V.
func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedFragmentIds returns sorted FragmentId keys from a map[FragmentId]SharedFragment.
func sortedFragmentIds(m map[FragmentId]SharedFragment) []FragmentId {
	ids := make([]FragmentId, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

// sortedRoleIds returns sorted RoleId values from a map[protocol.RoleId]RoleSpec.
func sortedRoleIds(m map[protocol.RoleId]RoleSpec) []protocol.RoleId {
	ids := make([]protocol.RoleId, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

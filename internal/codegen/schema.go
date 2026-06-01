// Package codegen — schema.xml generation.
//
// This file ports gen_schema.py to Go. It generates schema.xml from
// the canonical Go spec data maps (ConstraintSpecs, PhaseSpecs, etc.)
// using manual XML building (bytes.Buffer + fmt.Fprintf) to achieve
// CDATA sections for <code> elements and fine-grained indentation
// control matching the Python output.
//
// 15 of 17 section builders use encoding/xml struct marshalling (SLICE-B).
// The remaining 2 (buildConstraints, buildProcedureSteps) use manual
// fmt.Fprintf because they contain CDATA sections that encoding/xml
// cannot produce.
//
// Public API:
//
//	GenerateSchema(w io.Writer) error
//	GenerateSchemaToFile(path string, opts GenerateOptions) (string, error)
package codegen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// marshalSection marshals v using encoding/xml.MarshalIndent with the given
// depth for base indentation (2 spaces per depth level), and writes the
// result followed by a newline to buf.
//
// This is the shared helper for all 15 non-CDATA section builders.
func marshalSection(buf *bytes.Buffer, depth int, v interface{}) {
	prefix := indent(depth)
	data, err := xml.MarshalIndent(v, prefix, "  ")
	if err != nil {
		// Panic here since this is a code generation error in static data —
		// it indicates a programming bug, not a runtime condition.
		panic(fmt.Sprintf("marshalSection: xml.MarshalIndent failed: %v", err))
	}
	buf.Write(data)
	buf.WriteByte('\n')
}

// ─── Constraint role/phase ref helpers ────────────────────────────────────────

// rolePriority is the canonical sort order for role-ref attributes on
// <constraint> elements. Mirrors Python _ROLE_PRIORITY.
var rolePriority = []types.RoleId{
	types.RoleEpoch, types.RoleReviewer, types.RoleArchitect,
	types.RoleSupervisor, types.RoleWorker,
}

// phaseOrder is the canonical sort order for phase-ref attributes on
// <constraint> elements. Mirrors Python _PHASE_ORDER.
var phaseOrder = []protocol.PhaseId{
	protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
	protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
	protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
	protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
}

// constraintRoleRef returns the comma-separated role-ref string for the
// given constraint ID, or "" if the constraint is general (all roles).
// Mirrors Python _build_constraint_role_refs.
func constraintRoleRef(cid string) string {
	if generalConstraints[cid] {
		return "" // general constraint → omit role-ref
	}
	roleToConstraints := ConstraintToRoleRefs()
	roles, ok := roleToConstraints[cid]
	if !ok || len(roles) == 0 {
		return ""
	}
	// Sort by rolePriority order.
	priorityIdx := make(map[types.RoleId]int, len(rolePriority))
	for i, r := range rolePriority {
		priorityIdx[r] = i
	}
	sort.Slice(roles, func(i, j int) bool {
		pi, pj := priorityIdx[roles[i]], priorityIdx[roles[j]]
		return pi < pj
	})
	parts := make([]string, len(roles))
	for i, r := range roles {
		parts[i] = string(r)
	}
	return strings.Join(parts, ",")
}

// constraintPhaseRef returns the comma-separated phase-ref string for the
// given constraint ID, or "" if the constraint applies to all phases.
// Mirrors Python _build_constraint_phase_refs.
func constraintPhaseRef(cid string) string {
	if generalConstraints[cid] {
		return "" // general constraint → omit phase-ref
	}
	phaseToConstraints := ConstraintToPhaseRefs()
	phases, ok := phaseToConstraints[cid]
	if !ok || len(phases) == 0 {
		return ""
	}
	// Check if it's in ALL phases (excluding PhaseComplete).
	allPhases := true
	for _, p := range phaseOrder {
		found := false
		for _, cp := range phases {
			if cp == p {
				found = true
				break
			}
		}
		if !found {
			allPhases = false
			break
		}
	}
	if allPhases {
		return "" // applies to all phases → omit
	}

	// Sort by phaseOrder.
	orderIdx := make(map[protocol.PhaseId]int, len(phaseOrder))
	for i, p := range phaseOrder {
		orderIdx[p] = i
	}
	sort.Slice(phases, func(i, j int) bool {
		pi, pj := orderIdx[phases[i]], orderIdx[phases[j]]
		return pi < pj
	})
	parts := make([]string, len(phases))
	for i, p := range phases {
		parts[i] = phaseXMLId(p)
	}
	return strings.Join(parts, ",")
}

// ─── XML helper utilities ─────────────────────────────────────────────────────

// xmlAttr writes a single attribute to buf: name="escaped-value".
// Returns the attribute string.
func xmlAttr(name, value string) string {
	return fmt.Sprintf(` %s="%s"`, name, xmlEscapeAttr(value))
}

// xmlEscapeAttr escapes a string for use in an XML attribute value.
func xmlEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// xmlEscapeText escapes a string for use in XML text content.
func xmlEscapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// indent returns an indentation string for the given depth level (2 spaces each).
func indent(depth int) string {
	return strings.Repeat("  ", depth)
}

// sectionComment returns a section divider comment string.
// Mirrors Python _section_comment.
func sectionComment(title string, depth int) string {
	bar := strings.Repeat("═", 71)
	return fmt.Sprintf("%s<!-- %s\n%s     %s\n%s     %s -->",
		indent(depth), bar, indent(depth), title, indent(depth), bar)
}

// openTag returns an opening tag with attrs already formatted (e.g. from xmlAttr calls).
func openTag(name string, attrs ...string) string {
	return "<" + name + strings.Join(attrs, "") + ">"
}

// selfCloseTag returns a self-closing tag.
func selfCloseTag(name string, attrs ...string) string {
	return "<" + name + strings.Join(attrs, "") + " />"
}

// comment returns an XML comment.
func comment(text string) string {
	return "<!--" + text + "-->"
}

// ─── Section builders ─────────────────────────────────────────────────────────

func buildEnums(buf *bytes.Buffer, depth int) {
	section := EnumsSection{
		Enums: []EnumType{
			{
				Name: "DomainType",
				Values: []EnumValue{
					{Id: "user", Description: "User-facing interaction (requests, elicitation, UAT)"},
					{Id: "plan", Description: "Planning and design (proposals, reviews, ratification)"},
					{Id: "impl", Description: "Implementation (slices, code review, landing)"},
				},
			},
			{
				Name: "VoteType",
				Values: []EnumValue{
					{Id: "ACCEPT", Description: "All review criteria satisfied; no BLOCKER items"},
					{Id: "REVISE", Description: "BLOCKER issues found; must provide actionable feedback"},
				},
			},
			{
				Name: "SeverityLevel",
				Values: []EnumValue{
					{Id: "BLOCKER", Blocks: "true", Label: "pasture:severity:blocker", Description: "Security, type errors, test failures, broken production code paths"},
					{Id: "IMPORTANT", Blocks: "false", Label: "pasture:severity:important", Description: "Performance, missing validation, architectural concerns"},
					{Id: "MINOR", Blocks: "false", Label: "pasture:severity:minor", Description: "Style, optional optimizations, naming improvements"},
				},
			},
			{
				Name: "ExecutionMode",
				Values: []EnumValue{
					{Id: "sequential", Description: "Must complete before next step starts"},
					{Id: "parallel", Description: "Can run concurrently with sibling steps in same parallel-group"},
				},
			},
			{
				Name: "ContentLevel",
				Values: []EnumValue{
					{Id: "full-provenance", Description: "Full inline context with all decisions and rationale"},
					{Id: "summary-with-ids", Description: "Summary with Beads task ID references"},
				},
			},
			// Classification axes (s1_1-classify)
			{
				Name: "ClassificationScope",
				Values: []EnumValue{
					{Id: "single-file", Description: "Change is isolated to a single file"},
					{Id: "module", Description: "Change spans a module or package"},
					{Id: "cross-cutting", Description: "Change affects multiple modules or subsystems"},
				},
			},
			{
				Name: "ClassificationComplexity",
				Values: []EnumValue{
					{Id: "low", Description: "Straightforward implementation, familiar patterns"},
					{Id: "medium", Description: "Some design decisions needed, moderate scope"},
					{Id: "high", Description: "Significant design work, unfamiliar territory, or many moving parts"},
				},
			},
			{
				Name: "ClassificationRisk",
				Values: []EnumValue{
					{Id: "internal-only", Description: "No external API changes, no breaking changes"},
					{Id: "new-api", Description: "Introduces new public interfaces or APIs"},
					{Id: "breaking-changes", Description: "Modifies existing behavior or public contracts"},
				},
			},
			{
				Name: "ClassificationNovelty",
				Values: []EnumValue{
					{Id: "familiar", Description: "Well-known patterns, team has done this before"},
					{Id: "new-territory", Description: "Unfamiliar domain, requires research and exploration"},
				},
			},
			{
				Name: "ResearchDepth",
				Values: []EnumValue{
					{Id: "quick-scan", Description: "Familiar domain, low complexity — brief prior art check (local only)"},
					{Id: "standard-research", Description: "Moderate complexity or some novelty — find existing patterns and standards (local + docs)"},
					{Id: "deep-dive", Description: "High complexity, new territory, or high risk — thorough domain analysis (local + web)"},
				},
			},
		},
	}
	marshalSection(buf, depth, section)
}

func buildLabels(buf *bytes.Buffer, depth int) {
	phaseLabelIDs := []string{
		"L-p1s1_1", "L-p1s1_2", "L-p1s1_3",
		"L-p2s2_1", "L-p2s2_2",
		"L-p3s3", "L-p4s4", "L-p5s5", "L-p6s6", "L-p7s7",
		"L-p8s8", "L-p9s9", "L-p10s10", "L-p11s11", "L-p12s12",
	}
	specialLabelIDs := []string{
		"L-urd", "L-superseded",
		"L-sev-blocker", "L-sev-import", "L-sev-minor",
		"L-followup",
	}

	section := LabelsSection{}

	// Phase labels (one per substep)
	for _, lid := range phaseLabelIDs {
		spec := LabelSpecs[lid]
		section.Labels = append(section.Labels, LabelElem{
			Id:         spec.Id,
			Value:      spec.Value,
			PhaseRef:   spec.PhaseRef,
			SubstepRef: spec.SubstepRef,
		})
	}

	// Special labels (not phase-scoped)
	for _, lid := range specialLabelIDs {
		spec := LabelSpecs[lid]
		section.Labels = append(section.Labels, LabelElem{
			Id:          spec.Id,
			Value:       spec.Value,
			Special:     "true",
			Description: spec.Description,
			SeverityRef: spec.SeverityRef,
		})
	}

	marshalSection(buf, depth, section)
}

func buildReviewAxes(buf *bytes.Buffer, depth int) {
	axisOrder := []string{"axis-correctness", "axis-test_quality", "axis-elegance"}
	section := ReviewAxesSection{}
	for _, axisId := range axisOrder {
		spec, ok := ReviewAxisSpecs[axisId]
		if !ok {
			continue
		}
		axis := ReviewAxisElem{
			Id:     spec.Id,
			Letter: spec.Letter,
			Name:   spec.Name,
			Short:  spec.Short,
		}
		if len(spec.KeyQuestions) > 0 {
			axis.KeyQuestions = &KeyQuestionsElem{Questions: spec.KeyQuestions}
		}
		section.Axes = append(section.Axes, axis)
	}
	marshalSection(buf, depth, section)
}

// phaseDescriptions maps phase ID strings to their description text.
// Mirrors the inline dict in Python _build_phases.
var phaseDescriptions = map[string]string{
	"p1":  "Capture, classify, research, and explore user request",
	"p2":  "User Requirements Elicitation survey and URD creation",
	"p3":  "Architect creates technical proposal",
	"p4":  "3 axis-specific reviewers assess proposal",
	"p5":  "User acceptance test on the plan",
	"p6":  "Ratify the accepted proposal, supersede old ones",
	"p7":  "Architect hands off to supervisor",
	"p8":  "Supervisor decomposes ratified plan into vertical slices",
	"p9":  "Parallel workers implement vertical slices",
	"p10": "3 axis-specific reviewers review ALL slices",
	"p11": "User acceptance test on the implementation",
	"p12": "Commit, push, close tasks, hand off",
}

// p7SkillInvocation is the static supplement for the p7→p8 transition.
var p7SkillInvocation = map[string]string{
	"target-role": "supervisor",
	"command-ref": "cmd-supervisor",
	"directive":   "Supervisor launch prompt MUST start with Skill(/pasture:supervisor)",
}

// buildPhaseTaskTitles derives per-phase task-title hints from TitleConventions.
// Mirrors Python _build_phase_task_titles.
func buildPhaseTaskTitles() map[string][]map[string]string {
	byPhase := make(map[string][]TitleConvention)
	for _, tc := range TitleConventions {
		if tc.PhaseRef != "" {
			byPhase[tc.PhaseRef] = append(byPhase[tc.PhaseRef], tc)
		}
	}
	result := make(map[string][]map[string]string)
	for pid, tcs := range byPhase {
		multi := len(tcs) > 1
		var entries []map[string]string
		for _, tc := range tcs {
			entry := map[string]string{"pattern": tc.Pattern}
			if multi {
				// LabelRef format: "L-p{N}s{step}" (e.g. "L-p2s2_1").
				// Parsing steps:
				//   1. Drop the "L-" prefix to get "p2s2_1".
				//   2. Split on the first "s" to separate the phase token ("p2")
				//      from the substep suffix ("2_1").
				//   3. Prepend "s" to reconstruct the substep ID ("s2_1").
				label := tc.LabelRef[2:]               // drop "L-" prefix: "p2s2_1"
				parts := strings.SplitN(label, "s", 2) // ["p2", "2_1"]
				if len(parts) == 2 {
					entry["substep"] = "s" + parts[1]
				}
			}
			if tc.Note != "" {
				entry["convention"] = tc.Note
			}
			entries = append(entries, entry)
		}
		result[pid] = entries
	}
	return result
}

func buildPhases(buf *bytes.Buffer, depth int) {
	phaseTaskTitles := buildPhaseTaskTitles()

	orderedPhaseIds := []protocol.PhaseId{
		protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
		protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
		protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
		protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
	}

	layerNames := []string{"Types", "Tests", "Implementation"}
	section := PhasesSection{}

	for _, phaseId := range orderedPhaseIds {
		spec, ok := PhaseSpecs[phaseId]
		if !ok {
			continue
		}
		pid := phaseXMLId(spec.Id)

		phase := PhaseElem{
			Id:     pid,
			Number: strconv.Itoa(spec.Number),
			Domain: string(spec.Domain),
			Name:   spec.Name,
		}

		// Description
		if desc, ok := phaseDescriptions[pid]; ok {
			phase.Description = desc
		}

		// Substeps
		substeps := SubstepDataMap[pid]
		if len(substeps) > 0 {
			subElem := &SubstepsElem{}
			for _, sd := range substeps {
				substep := SubstepElem{
					Id:            sd.Id,
					Type:          sd.Type,
					Execution:     sd.Execution,
					Order:         strconv.Itoa(sd.Order),
					ParallelGroup: sd.ParallelGroup,
					LabelRef:      sd.LabelRef,
					Description:   sd.Description,
				}
				if sd.ExtraLabel != "" {
					substep.ExtraLabel = &ExtraLabelElem{Ref: sd.ExtraLabel}
				}
				if sd.Instances != nil {
					substep.Instances = &InstancesElem{
						Count: sd.Instances.Count,
						Per:   sd.Instances.Per,
					}
				}
				if sd.StartupSequence {
					supSteps := ProcedureSteps[types.RoleSupervisor]
					startup := &StartupSequenceElem{}
					for _, step := range supSteps {
						pstep := ProcedureStepElem{
							Order:       strconv.Itoa(step.Order),
							Id:          step.Id,
							Instruction: step.Instruction,
							Command:     step.Command,
							Context:     step.Context,
						}
						if step.NextState != "" {
							pstep.NextState = phaseXMLId(step.NextState)
						}
						startup.Steps = append(startup.Steps, pstep)
					}
					substep.Startup = startup
				}
				subElem.Substeps = append(subElem.Substeps, substep)
			}
			phase.Substeps = subElem
		}

		// Task-title(s) from phase task titles
		if tts, ok := phaseTaskTitles[pid]; ok {
			for _, tt := range tts {
				ttElem := TaskTitleElem{
					Pattern: tt["pattern"],
					Substep: tt["substep"],
				}
				if conv, ok := tt["convention"]; ok {
					ttElem.Convention = conv
				}
				phase.TaskTitles = append(phase.TaskTitles, ttElem)
			}
		}

		// Special phase elements
		switch pid {
		case "p4":
			phase.SeverityTree = &SeverityTreeElem{
				Enabled: "false",
				Reason:  "Plan reviews use binary ACCEPT/REVISE only",
			}
		case "p6":
			phase.SameActorAs = &SameActorAsElem{
				PhaseRef: "p5",
				Note:     "Architect performs p5, p6, p7 — no handoff between them",
			}
		case "p9":
			workerSteps := ProcedureSteps[types.RoleWorker]
			tdd := &TDDLayersElem{}
			for _, step := range workerSteps {
				name := ""
				if step.Order >= 1 && step.Order <= len(layerNames) {
					name = layerNames[step.Order-1]
				}
				tdd.Layers = append(tdd.Layers, TDDLayerElem{
					Number:      strconv.Itoa(step.Order),
					Name:        name,
					Description: step.Instruction,
				})
			}
			phase.TDDLayers = tdd
		case "p10":
			phase.SeverityTree = &SeverityTreeElem{
				Enabled:  "true",
				Creation: "eager",
				Rules: []string{
					"Always create 3 severity groups per review round, even if empty.",
					"Empty groups have no children and are closed immediately.",
				},
				Groups: []SevGroupElem{
					{SeverityRef: "BLOCKER", LabelRef: "L-sev-blocker", DualParent: "true"},
					{SeverityRef: "IMPORTANT", LabelRef: "L-sev-import"},
					{SeverityRef: "MINOR", LabelRef: "L-sev-minor"},
				},
			}
			phase.FollowupEpic = &FollowupEpicElem{
				LabelRef:       "L-followup",
				Trigger:        "review-completion AND (IMPORTANT OR MINOR findings exist)",
				GatedOnBlocker: "false",
				OwnerRole:      "supervisor",
			}
		}

		// Transitions
		if len(spec.Transitions) > 0 {
			trans := &TransitionsElem{}
			for _, t := range spec.Transitions {
				tElem := TransitionElem{
					ToPhase:   phaseXMLId(t.ToPhase),
					Condition: t.Condition,
					Action:    t.Action,
				}
				// p7→p8 transition needs skill-invocation
				if pid == "p7" && t.ToPhase == protocol.PhaseImplPlan {
					tElem.SkillInvocation = &SkillInvocationElem{
						TargetRole: p7SkillInvocation["target-role"],
						CommandRef: p7SkillInvocation["command-ref"],
						Directive:  p7SkillInvocation["directive"],
					}
				}
				trans.Transitions = append(trans.Transitions, tElem)
			}
			phase.Transitions = trans
		}

		section.Phases = append(section.Phases, phase)
	}

	marshalSection(buf, depth, section)
}

// roleDelegates is the static delegate data for epoch role.
var roleDelegates = map[string][]map[string]string{
	"epoch": {
		{"to-role": "architect", "phases": "p1,p2,p3,p4,p5,p6,p7"},
		{"to-role": "supervisor", "phases": "p7,p8,p9,p10,p11,p12"},
	},
}

// roleLabelAwareness is the static label-awareness text per role.
var roleLabelAwareness = map[string]string{
	"architect": "pasture:p1-user, pasture:p2-user, pasture:p3-plan, pasture:p4-plan, pasture:p5-user, pasture:p6-plan, pasture:p7-plan",
	"reviewer": "pasture:p4-plan:s4-review, pasture:p10-impl:s10-review, " +
		"pasture:severity:blocker, pasture:severity:important, pasture:severity:minor",
	"supervisor": "pasture:p7-plan, pasture:p8-impl, pasture:p9-impl, pasture:p10-impl, " +
		"pasture:p11-user, pasture:p12-impl, pasture:epic-followup",
	"worker": "pasture:p9-impl:s9-slice",
}

// roleInvariants is the static invariants list per role.
var roleInvariants = map[string][]string{
	"supervisor": {
		"NEVER implements code — always spawns workers",
		"NEVER explores codebase directly — delegates to ephemeral Explore subagents",
		"ALWAYS creates leaf tasks within each slice — no undecomposed slices",
		"Creates follow-up epic when code review has IMPORTANT or MINOR findings",
	},
}

// roleOwnershipModel is the static ownership model text per role.
var roleOwnershipModel = map[string]string{
	"worker": "One worker per production code path. Owns full vertical\n      (types → tests → implementation → wiring).",
}

// roleUsesAxes is the static axes list per role.
var roleUsesAxes = map[string][]string{
	"reviewer": {"axis-correctness", "axis-test_quality", "axis-elegance"},
}

func buildRoles(buf *bytes.Buffer, depth int) {
	roleOrder := []types.RoleId{
		types.RoleEpoch, types.RoleArchitect, types.RoleReviewer,
		types.RoleSupervisor, types.RoleWorker,
	}

	section := RolesSection{}

	for _, roleId := range roleOrder {
		spec, ok := RoleSpecs[roleId]
		if !ok {
			continue
		}
		rid := string(spec.Id)

		role := RoleElem{
			Id:          rid,
			Name:        spec.Name,
			Description: spec.Description,
		}

		// owns-phases: sort by phase number
		sorted := make([]protocol.PhaseId, len(spec.OwnedPhases))
		copy(sorted, spec.OwnedPhases)
		sort.Slice(sorted, func(i, j int) bool {
			return phaseNumber(sorted[i]) < phaseNumber(sorted[j])
		})
		ownedPhases := &OwnedPhasesElem{}
		for _, phaseRef := range sorted {
			ownedPhases.PhaseRefs = append(ownedPhases.PhaseRefs, PhaseRefElem{Ref: phaseXMLId(phaseRef)})
		}
		role.OwnedPhases = ownedPhases

		// Delegates (epoch only)
		if delegates, ok := roleDelegates[rid]; ok {
			delElem := &DelegatesElem{}
			for _, del := range delegates {
				delElem.Delegates = append(delElem.Delegates, DelegateElem{
					ToRole: del["to-role"],
					Phases: del["phases"],
				})
			}
			role.Delegates = delElem
		}

		// Label awareness
		if la, ok := roleLabelAwareness[rid]; ok {
			role.LabelAwareness = la
		}

		// Uses axes (reviewer)
		if axes, ok := roleUsesAxes[rid]; ok {
			usesAxes := &UsesAxesElem{}
			for _, axRef := range axes {
				usesAxes.AxisRefs = append(usesAxes.AxisRefs, AxisRefElem{Ref: axRef})
			}
			role.UsesAxes = usesAxes
		}

		// Invariants (supervisor)
		if invs, ok := roleInvariants[rid]; ok {
			role.Invariants = &InvariantsElem{Invariants: invs}
		}

		// Tools, model, thinking
		if len(spec.Tools) > 0 {
			role.Tools = strings.Join(spec.Tools, ", ")
		}
		role.Model = spec.Model
		role.Thinking = spec.Thinking

		// Ownership model (worker)
		if om, ok := roleOwnershipModel[rid]; ok {
			role.OwnershipModel = om
		}

		// Introduction and ownership narrative
		role.Introduction = spec.Introduction
		role.OwnershipNarrative = spec.OwnershipNarrative

		// Behaviors
		if len(spec.Behaviors) > 0 {
			behaviors := &BehaviorsElem{}
			for _, b := range spec.Behaviors {
				behaviors.Behaviors = append(behaviors.Behaviors, BehaviorElem{
					Id:        b.Id,
					Given:     b.Given,
					When:      b.When,
					Then:      b.Then,
					ShouldNot: b.ShouldNot,
				})
			}
			role.Behaviors = behaviors
		}

		section.Roles = append(section.Roles, role)
	}

	marshalSection(buf, depth, section)
}

// commandOrder is the canonical ordering of commands in schema.xml.
// Mirrors Python command_order list in _build_commands.
var commandOrder = []string{
	// Orchestration
	"cmd-epoch", "cmd-status",
	// User interaction
	"cmd-user-request", "cmd-user-elicit", "cmd-user-uat",
	// Architect
	"cmd-architect", "cmd-arch-propose", "cmd-arch-review",
	"cmd-arch-ratify", "cmd-arch-handoff",
	// Supervisor
	"cmd-supervisor", "cmd-sup-plan", "cmd-sup-spawn",
	"cmd-sup-track", "cmd-sup-commit",
	// Worker
	"cmd-worker", "cmd-work-impl", "cmd-work-complete", "cmd-work-blocked",
	// Reviewer
	"cmd-reviewer", "cmd-rev-plan", "cmd-rev-code",
	"cmd-rev-comment", "cmd-rev-vote",
	// Implementation coordination
	"cmd-impl-slice", "cmd-impl-review",
	// Exploration
	"cmd-explore", "cmd-research",
}

// commandGroupComments marks the start of each command group with a comment.
var commandGroupComments = map[string]string{
	"cmd-epoch":        " ── Orchestration ──────────────────────────────────────────────── ",
	"cmd-user-request": " ── User interaction ───────────────────────────────────────── ",
	"cmd-architect":    " ── Architect ──────────────────────────────────────────────────── ",
	"cmd-supervisor":   " ── Supervisor ─────────────────────────────────────────────────── ",
	"cmd-worker":       " ── Worker ─────────────────────────────────────────────────────── ",
	"cmd-reviewer":     " ── Reviewer ───────────────────────────────────────────────────── ",
	"cmd-impl-slice":   " ── Implementation coordination ────────────────────────────────── ",
	"cmd-explore":      " ── Exploration ────────────────────────────────────────────────── ",
}

func buildCommands(buf *bytes.Buffer, depth int) {
	section := CommandsSection{}

	for _, cid := range commandOrder {
		spec, ok := CommandSpecs[cid]
		if !ok {
			continue
		}

		cmd := CommandElem{
			Id:          spec.Id,
			Name:        spec.Name,
			RoleRef:     string(spec.RoleRef),
			Description: spec.Description,
		}

		// phases
		if len(spec.Phases) > 0 {
			phases := &CommandPhasesElem{}
			for _, phaseRef := range spec.Phases {
				phases.PhaseRefs = append(phases.PhaseRefs, PhaseRefElem{Ref: phaseXMLId(phaseRef)})
			}
			cmd.Phases = phases
		}

		// creates-labels
		if len(spec.CreatesLabels) > 0 {
			labels := &CreatesLabelsElem{}
			for _, labelRef := range spec.CreatesLabels {
				labels.LabelRefs = append(labels.LabelRefs, LabelRefElem{Ref: labelRef})
			}
			cmd.CreatesLabels = labels
		}

		// file
		cmd.File = spec.File

		// cmd-explore special note
		if cid == "cmd-explore" {
			cmd.Note = "Used in Phase 1 (s1_3) by architect, and in Phase 8 by supervisor&#39;s ephemeral Explore subagents."
		}

		section.Commands = append(section.Commands, cmd)
	}

	marshalSection(buf, depth, section)
}

// handoffFilePatterns maps handoff IDs to their file pattern strings.
var handoffFilePatterns = map[string]string{
	"h1": "architect-to-supervisor.md",
	"h2": "supervisor-to-worker.md",
	"h3": "supervisor-to-reviewer.md",
	"h4": "worker-to-reviewer.md",
	"h5": "reviewer-to-followup.md",
	"h6": "supervisor-to-architect.md",
}

// handoffSkillInvocations maps handoff IDs to their skill invocation data.
var handoffSkillInvocations = map[string]map[string]string{
	"h1": {
		"directive": "Skill(/pasture:supervisor)",
		"note":      "Supervisor launch prompt MUST start with this invocation. Without it, supervisor skips leaf task creation.",
	},
	"h2": {
		"directive": "Skill(/pasture:worker)",
		"note":      "Worker message MUST include explicit instruction to call this skill.",
	},
	"h3": {
		"directive": "Skill(/pasture:reviewer)",
		"note":      "Reviewer prompt MUST include instruction to call this skill.",
	},
}

// handoffNotes maps handoff IDs to extra notes.
var handoffNotes = map[string]string{
	"h5": "Reviewer hands IMPORTANT/MINOR findings to supervisor, who creates the follow-up epic",
	"h6": "Follow-up specific. Supervisor completes FOLLOWUP_URE and FOLLOWUP_URD,\n      then hands off to architect with scoped findings and requirements\n      for FOLLOWUP_PROPOSAL creation.",
}

// handoffTriggers maps handoff IDs to trigger strings.
var handoffTriggers = map[string]string{
	"h5": "IMPORTANT or MINOR findings exist",
	"h6": "follow-up lifecycle only",
}

func buildHandoffs(buf *bytes.Buffer, depth int) {
	section := HandoffsSection{
		StoragePattern: ".git/.aura/handoff/{request-task-id}/{source}-to-{target}.md",
	}

	handoffOrderList := []string{"h1", "h2", "h3", "h4", "h5", "h6"}

	for _, hid := range handoffOrderList {
		spec, ok := HandoffSpecs[hid]
		if !ok {
			continue
		}

		handoff := HandoffElem{
			Id:           spec.Id,
			SourceRole:   string(spec.SourceRole),
			TargetRole:   string(spec.TargetRole),
			AtPhase:      phaseXMLId(spec.AtPhase),
			ContentLevel: spec.ContentLevel,
		}

		if fp, ok := handoffFilePatterns[hid]; ok {
			handoff.FilePattern = fp
		}
		if trigger, ok := handoffTriggers[hid]; ok {
			if hid == "h6" {
				handoff.Context = trigger
			} else {
				handoff.Trigger = trigger
			}
		}

		// required-fields
		handoff.RequiredFields = &RequiredFieldsElem{
			Text: strings.Join(spec.RequiredFields, ", "),
		}

		// skill-invocation
		if si, ok := handoffSkillInvocations[hid]; ok {
			siElem := &SkillInvocationElem{
				Directive: si["directive"],
			}
			if note, ok := si["note"]; ok {
				siElem.Note = note
			}
			handoff.SkillInvocation = siElem
		}

		// notes
		if note, ok := handoffNotes[hid]; ok {
			handoff.Note = &HandoffNoteElem{Text: note}
		}

		section.Handoffs = append(section.Handoffs, handoff)
	}

	// same-actor-transitions
	section.SameActorTransitions = &SameActorTransitionsElem{
		Note: "No handoff document needed",
		Transitions: []SameActorTransitionElem{
			{FromPhase: "p5", ToPhase: "p6", Actor: "architect"},
			{FromPhase: "p6", ToPhase: "p7", Actor: "architect"},
		},
	}

	marshalSection(buf, depth, section)
}

// constraintOrder is the canonical ordering of constraints matching Python CONSTRAINT_SPECS insertion order.
var constraintOrder = []string{
	"C-audit-never-delete",
	"C-audit-dep-chain",
	"C-review-consensus",
	"C-review-binary",
	"C-severity-eager",
	"C-severity-not-plan",
	"C-blocker-dual-parent",
	"C-followup-timing",
	"C-vertical-slices",
	"C-supervisor-no-impl",
	"C-supervisor-explore-ephemeral",
	"C-clean-review-exit",
	"C-autonomous-progression",
	"C-integration-points",
	"C-slice-review-before-close",
	"C-max-review-cycles",
	"C-slice-leaf-tasks",
	"C-handoff-skill-invocation",
	"C-dep-direction",
	"C-frontmatter-refs",
	"C-agent-commit",
	"C-proposal-naming",
	"C-review-naming",
	"C-ure-verbatim",
	"C-followup-lifecycle",
	"C-followup-leaf-adoption",
	"C-worker-gates",
	"C-actionable-errors",
}

// constraintGroupComments marks the start of each constraint group with a comment.
var constraintGroupComments = map[string]string{
	"C-audit-never-delete": " Audit trail ",
	"C-review-consensus":   " Reviews ",
	"C-vertical-slices":    " Ownership ",
	"C-dep-direction":      " Task management ",
	"C-agent-commit":       " Git ",
	"C-proposal-naming":    " Naming ",
	"C-ure-verbatim":       " User interviews ",
	"C-followup-lifecycle": " Follow-up lifecycle ",
	"C-actionable-errors":  " Error quality ",
	"C-worker-gates":       " Worker completion ",
}

func buildConstraints(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	w(d + "<constraints>")

	for _, cid := range constraintOrder {
		spec, ok := ConstraintSpecs[cid]
		if !ok {
			continue
		}

		if grpComment, ok := constraintGroupComments[cid]; ok {
			w(d1 + comment(grpComment))
		}

		cAttrs := []string{
			xmlAttr("id", spec.Id),
			xmlAttr("given", spec.Given),
			xmlAttr("when", spec.When),
			xmlAttr("then", spec.Then),
			xmlAttr("should-not", spec.ShouldNot),
		}

		roleRef := constraintRoleRef(cid)
		if roleRef != "" {
			cAttrs = append(cAttrs, xmlAttr("role-ref", roleRef))
		}
		phaseRef := constraintPhaseRef(cid)
		if phaseRef != "" {
			cAttrs = append(cAttrs, xmlAttr("phase-ref", phaseRef))
		}
		if spec.Command != "" {
			cAttrs = append(cAttrs, xmlAttr("command", spec.Command))
		}

		if len(spec.Examples) == 0 {
			w(d1 + selfCloseTag("constraint", cAttrs...))
		} else {
			w(d1 + openTag("constraint", cAttrs...))
			for _, ex := range spec.Examples {
				exAttrs := []string{
					xmlAttr("id", ex.Id),
					xmlAttr("lang", ex.Lang),
					xmlAttr("label", ex.Label),
				}
				if ex.AlsoIllustrates != "" {
					exAttrs = append(exAttrs, xmlAttr("also-illustrates", ex.AlsoIllustrates))
				}
				w(d2 + openTag("example", exAttrs...))
				// Use CDATA for code content
				w(d3 + "<code><![CDATA[" + ex.Code + "]]></code>")
				w(d2 + "</example>")
			}
			w(d1 + "</constraint>")
		}
	}

	w(d + "</constraints>")
}

func buildTaskTitles(buf *bytes.Buffer, depth int) {
	section := TaskTitlesSection{}
	for _, tc := range TitleConventions {
		section.Conventions = append(section.Conventions, TitleConventionElem{
			Pattern:       tc.Pattern,
			LabelRef:      tc.LabelRef,
			CreatedBy:     tc.CreatedBy,
			PhaseRef:      tc.PhaseRef,
			ExtraLabelRef: tc.ExtraLabelRef,
			Note:          tc.Note,
		})
	}
	marshalSection(buf, depth, section)
}

func buildDocuments(buf *bytes.Buffer, depth int) {
	type docEntry struct {
		id      string
		path    string
		purpose string
		covers  []map[string]string
	}

	docs := []docEntry{
		{
			id:      "doc-readme",
			path:    "protocol/README.md",
			purpose: "Protocol entry point and quick-start guide",
			covers: []map[string]string{
				{"type": "phase", "refs": "p1,p2,p3,p4,p5,p6,p7,p8,p9,p10,p11,p12", "depth": "overview"},
				{"type": "label", "refs": "all", "depth": "schema-summary"},
			},
		},
		{
			id:      "doc-claude",
			path:    "protocol/CLAUDE.md",
			purpose: "Core agent directive: philosophy, constraints, roles, label schema",
			covers: []map[string]string{
				{"type": "phase", "refs": "p1,p2,p3,p4,p5,p6,p7,p8,p9,p10,p11,p12", "depth": "summary"},
				{"type": "role", "refs": "architect,reviewer,supervisor,worker", "depth": "summary"},
				{"type": "label", "refs": "all", "depth": "full"},
				{"type": "constraint", "refs": "all-protocol", "depth": "full"},
				{"type": "task-title", "refs": "all", "depth": "full"},
				{"type": "handoff", "refs": "h1,h2,h3,h4,h5,h6", "depth": "summary"},
				{"type": "severity", "refs": "BLOCKER,IMPORTANT,MINOR", "depth": "full"},
				{"type": "review-axis", "refs": "axis-correctness,axis-test_quality,axis-elegance", "depth": "summary"},
			},
		},
		{
			id:      "doc-constraints",
			path:    "protocol/CONSTRAINTS.md",
			purpose: "Coding standards, checklists, severity definitions, naming conventions",
			covers: []map[string]string{
				{"type": "constraint", "refs": "all", "depth": "full"},
				{"type": "severity", "refs": "BLOCKER,IMPORTANT,MINOR", "depth": "full"},
				{"type": "vote", "refs": "ACCEPT,REVISE", "depth": "full"},
				{"type": "label", "refs": "all", "depth": "schema"},
				{"type": "task-title", "refs": "all", "depth": "full"},
			},
		},
		{
			id:      "doc-process",
			path:    "protocol/PROCESS.md",
			purpose: "Step-by-step workflow execution (single source of truth)",
			covers: []map[string]string{
				{"type": "phase", "refs": "p1,p2,p3,p4,p5,p6,p7,p8,p9,p10,p11,p12", "depth": "full"},
				{"type": "substep", "refs": "all", "depth": "full"},
				{"type": "role", "refs": "architect,reviewer,supervisor,worker", "depth": "tools-matrix"},
				{"type": "command", "refs": "all", "depth": "tools-matrix"},
				{"type": "label", "refs": "all", "depth": "full"},
				{"type": "transition", "refs": "all", "depth": "full"},
				{"type": "severity", "refs": "BLOCKER,IMPORTANT,MINOR", "depth": "full"},
			},
		},
		{
			id:      "doc-agents",
			path:    "protocol/AGENTS.md",
			purpose: "Role taxonomy: phases owned, tools, handoffs per agent",
			covers: []map[string]string{
				{"type": "role", "refs": "epoch,architect,reviewer,supervisor,worker", "depth": "full"},
				{"type": "phase", "refs": "all", "depth": "role-mapping"},
				{"type": "command", "refs": "all", "depth": "role-mapping"},
				{"type": "handoff", "refs": "h1,h2,h3,h4,h5,h6", "depth": "full"},
				{"type": "review-axis", "refs": "axis-correctness,axis-test_quality,axis-elegance", "depth": "full"},
			},
		},
		{
			id:      "doc-skills",
			path:    "protocol/SKILLS.md",
			purpose: "Command reference: all /pasture:* skills mapped to phase and role",
			covers: []map[string]string{
				{"type": "command", "refs": "all", "depth": "full"},
				{"type": "phase", "refs": "all", "depth": "command-mapping"},
				{"type": "role", "refs": "all", "depth": "command-mapping"},
				{"type": "label", "refs": "all", "depth": "command-creates"},
				{"type": "review-axis", "refs": "axis-correctness,axis-test_quality,axis-elegance", "depth": "summary"},
			},
		},
		{
			id:      "doc-handoff",
			path:    "protocol/HANDOFF_TEMPLATE.md",
			purpose: "Standardized template for 6 actor-change transitions",
			covers: []map[string]string{
				{"type": "handoff", "refs": "h1,h2,h3,h4,h5,h6", "depth": "full"},
				{"type": "role", "refs": "architect,supervisor,worker,reviewer", "depth": "handoff-fields"},
			},
		},
		{
			id:      "doc-migration",
			path:    "protocol/MIGRATION_v1_to_v2.md",
			purpose: "Label and title migration from v1 to v2",
			covers: []map[string]string{
				{"type": "label", "refs": "all", "depth": "v1-v2-mapping"},
				{"type": "task-title", "refs": "all", "depth": "v1-v2-mapping"},
				{"type": "vote", "refs": "ACCEPT,REVISE", "depth": "v1-v2-mapping"},
			},
		},
		{
			id:      "doc-uat-template",
			path:    "protocol/UAT_TEMPLATE.md",
			purpose: "User Acceptance Test structured output template",
			covers: []map[string]string{
				{"type": "phase", "refs": "p5,p11", "depth": "template"},
			},
		},
		{
			id:      "doc-uat-example",
			path:    "protocol/UAT_EXAMPLE.md",
			purpose: "Worked UAT example",
			covers: []map[string]string{
				{"type": "phase", "refs": "p5", "depth": "example"},
			},
		},
		{
			id:      "doc-schema",
			path:    "protocol/schema.xml",
			purpose: "This file: canonical machine-readable protocol definition (BCNF)",
			covers: []map[string]string{
				{"type": "all", "depth": "full", "note": "Single source of truth for all entity definitions and relationships"},
			},
		},
	}

	rootDocs := []docEntry{
		{
			id:      "doc-root-readme",
			path:    "README.md",
			purpose: "Project README with workflow overview, commands, structure",
			covers: []map[string]string{
				{"type": "phase", "refs": "all", "depth": "overview"},
				{"type": "command", "refs": "all", "depth": "table"},
				{"type": "role", "refs": "all", "depth": "table"},
			},
		},
		{
			id:      "doc-root-agents",
			path:    "AGENTS.md",
			purpose: "Agent orchestration guide for this repository",
			covers: []map[string]string{
				{"type": "role", "refs": "all", "depth": "orchestration"},
				{"type": "constraint", "refs": "C-dep-direction,C-agent-commit", "depth": "full"},
			},
		},
	}

	allDocs := append(docs, rootDocs...)
	section := DocumentsSection{}
	for _, doc := range allDocs {
		docElem := DocumentElem{
			Id:      doc.id,
			Path:    doc.path,
			Purpose: doc.purpose,
		}
		covers := &CoversElem{}
		for _, cover := range doc.covers {
			entity := CoverEntityElem{
				Type:  cover["type"],
				Depth: cover["depth"],
			}
			if refs, ok := cover["refs"]; ok {
				entity.Refs = refs
			}
			if note, ok := cover["note"]; ok {
				entity.Note = note
			}
			covers.Entities = append(covers.Entities, entity)
		}
		docElem.Covers = covers
		section.Documents = append(section.Documents, docElem)
	}
	marshalSection(buf, depth, section)
}

func buildDependencyModel(buf *bytes.Buffer, depth int) {
	section := DependencyModelSection{
		Rule:           "Parent (stays open) is blocked-by child (must finish first). Work flows bottom-up; closure flows top-down.",
		CanonicalChain: "REQUEST \u2192 blocked-by ELICIT \u2192 blocked-by PROPOSAL \u2192 blocked-by IMPL_PLAN \u2192 blocked-by SLICE-N \u2192 blocked-by leaf tasks",
		Command:        "bd dep add {parent-id} --blocked-by {child-id}",
		AntiPattern:    "bd dep add {child-id} --blocked-by {parent-id}",
		ReferenceLinks: &ReferenceLinksElem{
			Note:    "URD and other reference docs use frontmatter, not blocking deps",
			Pattern: "description frontmatter:\n  references:\n    urd: {urd-task-id}\n    request: {request-task-id}",
		},
	}
	marshalSection(buf, depth, section)
}

func buildFollowupLifecycle(buf *bytes.Buffer, depth int) {
	section := FollowupLifecycleSection{
		Trigger:        "Code review completion AND (IMPORTANT OR MINOR findings exist)",
		OwnerRole:      "supervisor",
		GatedOnBlocker: "false",
		DependencyChain: &DepChainElem{
			Note: "Same protocol phases but with FOLLOWUP_ prefix",
			Steps: []DepChainStepElem{
				{TaskTitle: "FOLLOWUP: {description}", PhaseRef: "p10", Description: "Epic created by supervisor. References original URD and review tasks."},
				{TaskTitle: "FOLLOWUP_URE: {description}", PhaseRef: "p2", Description: "Scoping URE with user to determine which findings to address."},
				{TaskTitle: "FOLLOWUP_URD: {description}", PhaseRef: "p2", Description: "Requirements doc for follow-up scope. References original URD."},
				{TaskTitle: "FOLLOWUP_PROPOSAL-{N}: {description}", PhaseRef: "p3", Description: "Proposal accounting for original URD + FOLLOWUP_URD + outstanding findings."},
				{TaskTitle: "FOLLOWUP_IMPL_PLAN: {description}", PhaseRef: "p8", Description: "Supervisor decomposes follow-up into slices."},
				{TaskTitle: "FOLLOWUP_SLICE-{N}: {description}", PhaseRef: "p9", Description: "Each slice adopts original IMPORTANT/MINOR leaf tasks as children."},
			},
		},
		LeafTaskAdoption: &LeafTaskAdoptElem{
			Rule:    "When supervisor creates FOLLOWUP_SLICE-N, the IMPORTANT/MINOR leaf tasks from the original review gain a second parent: the follow-up slice. This is the same dual-parent pattern as BLOCKER findings.",
			Command: "bd dep add {followup-slice-id} --blocked-by {important-leaf-task-id}\nbd dep add {followup-slice-id} --blocked-by {minor-leaf-task-id}",
			Note:    "Leaf tasks retain their original parent (the severity group from the original review) AND gain the follow-up slice as a second parent. Both must close for the leaf to be fully resolved.",
		},
		References: &FollowupRefsElem{
			Refs: []FollowupRefElem{
				{Type: "relates_to", Target: "original URD", Note: "Follow-up epic references original URD via frontmatter"},
				{Type: "relates_to", Target: "original REVIEW tasks", Note: "Follow-up epic references review tasks via frontmatter"},
			},
		},
		HandoffChain: &HandoffChainElem{
			Note: "How handoffs flow through the follow-up lifecycle",
			Transitions: []HandoffChainTransElem{
				{Order: "1", HandoffRef: "h5", Description: "Reviewer \u2192 Followup: Bridge from original review to follow-up epic. Created by supervisor when IMPORTANT/MINOR findings exist. This handoff STARTS the follow-up lifecycle."},
				{Order: "2", HandoffRef: "none", SameActor: "true", Description: "Supervisor creates FOLLOWUP_URE (same actor \u2014 supervisor owns follow-up epic and initiates scoping)"},
				{Order: "3", HandoffRef: "none", SameActor: "true", Description: "Supervisor creates FOLLOWUP_URD (same actor within Phase 2 \u2014 supervisor synthesizes follow-up requirements)"},
				{Order: "4", HandoffRef: "h6", Description: "Supervisor \u2192 Architect: Hands off completed FOLLOWUP_URE + FOLLOWUP_URD to architect for FOLLOWUP_PROPOSAL creation. Architect receives scoped findings and requirements."},
				{Order: "5", HandoffRef: "h1", Description: "Architect \u2192 Supervisor: After FOLLOWUP_PROPOSAL is ratified, architect hands off to supervisor for FOLLOWUP_IMPL_PLAN. Handoff doc references original URD, FOLLOWUP_URD, and outstanding findings."},
				{Order: "6", HandoffRef: "h2", Description: "Supervisor \u2192 Worker: FOLLOWUP_SLICE-N assignment. Worker receives both the follow-up slice spec AND the original leaf task IDs they must resolve."},
				{Order: "7", HandoffRef: "h3", Description: "Supervisor \u2192 Reviewer: Code review of follow-up slices. Reviewer receives follow-up context + original findings being addressed."},
				{Order: "8", HandoffRef: "h4", Description: "Worker \u2192 Reviewer: Worker completes follow-up slice. Handoff includes which original leaf tasks were resolved."},
			},
		},
	}
	marshalSection(buf, depth, section)
}

func buildProcedureSteps(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)
	d4 := indent(depth + 4)

	roleOrder := []types.RoleId{
		types.RoleEpoch, types.RoleArchitect, types.RoleReviewer,
		types.RoleSupervisor, types.RoleWorker,
	}

	w(d + "<procedure-steps>")

	for _, roleId := range roleOrder {
		steps, ok := ProcedureSteps[roleId]
		if !ok || len(steps) == 0 {
			continue
		}

		w(d1 + openTag("role", xmlAttr("ref", string(roleId))))
		for _, step := range steps {
			stepAttrs := []string{
				xmlAttr("order", strconv.Itoa(step.Order)),
				xmlAttr("id", step.Id),
			}
			if step.NextState != "" {
				stepAttrs = append(stepAttrs, xmlAttr("next-state", phaseXMLId(step.NextState)))
			}
			w(d2 + openTag("step", stepAttrs...))
			w(d3 + "<instruction>" + xmlEscapeText(step.Instruction) + "</instruction>")
			if step.Command != "" {
				w(d3 + "<command>" + xmlEscapeText(step.Command) + "</command>")
			}
			if step.Context != "" {
				w(d3 + "<context>" + xmlEscapeText(step.Context) + "</context>")
			}
			for _, ex := range step.Examples {
				exAttrs := []string{
					xmlAttr("id", ex.Id),
					xmlAttr("lang", ex.Lang),
					xmlAttr("label", ex.Label),
				}
				if ex.AlsoIllustrates != "" {
					exAttrs = append(exAttrs, xmlAttr("also-illustrates", ex.AlsoIllustrates))
				}
				w(d3 + openTag("example", exAttrs...))
				w(d4 + "<code><![CDATA[" + ex.Code + "]]></code>")
				w(d3 + "</example>")
			}
			w(d2 + "</step>")
		}
		w(d1 + "</role>")
	}

	w(d + "</procedure-steps>")
}

func buildChecklists(buf *bytes.Buffer, depth int) {
	// Stable ordering: sort by key
	keys := make([]string, 0, len(ChecklistSpecs))
	for k := range ChecklistSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	section := ChecklistsSection{}
	for _, key := range keys {
		cl := ChecklistSpecs[key]
		checklistElem := ChecklistElem{
			Id:      key,
			RoleRef: string(cl.RoleRef),
			Gate:    cl.Gate,
		}
		for _, item := range cl.Items {
			required := "false"
			if item.Required {
				required = "true"
			}
			checklistElem.Items = append(checklistElem.Items, ChecklistItemElem{
				Id:       item.Id,
				Required: required,
				Text:     item.Text,
			})
		}
		section.Checklists = append(section.Checklists, checklistElem)
	}
	marshalSection(buf, depth, section)
}

func buildCoordinationCommands(buf *bytes.Buffer, depth int) {
	// Stable ordering: sort by ID
	keys := make([]string, 0, len(CoordinationCommands))
	for k := range CoordinationCommands {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	section := CoordinationCommandsSection{}
	for _, key := range keys {
		cmd := CoordinationCommands[key]
		cmdElem := CoordCmdElem{
			Id:       cmd.Id,
			Action:   cmd.Action,
			Template: cmd.Template,
			RoleRef:  string(cmd.RoleRef),
		}
		if cmd.Shared {
			cmdElem.Shared = "true"
		}
		section.Commands = append(section.Commands, cmdElem)
	}
	marshalSection(buf, depth, section)
}

func buildWorkflows(buf *bytes.Buffer, depth int) {
	// Stable ordering: sort by ID
	keys := make([]string, 0, len(WorkflowSpecs))
	for k := range WorkflowSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	section := WorkflowsSection{}
	for _, key := range keys {
		wf := WorkflowSpecs[key]
		wfElem := WorkflowElem{
			Id:          wf.Id,
			Name:        wf.Name,
			RoleRef:     string(wf.RoleRef),
			Description: wf.Description,
		}
		for _, stage := range wf.Stages {
			stageElem := StageElem{
				Id:        stage.Id,
				Name:      stage.Name,
				Order:     strconv.Itoa(stage.Order),
				Execution: stage.Execution,
			}
			if stage.PhaseRef != "" {
				stageElem.PhaseRef = phaseXMLId(stage.PhaseRef)
			}
			for _, action := range stage.Actions {
				actionElem := ActionElem{
					Id:          action.Id,
					Instruction: action.Instruction,
					Command:     action.Command,
				}
				stageElem.Actions = append(stageElem.Actions, actionElem)
			}
			for _, ec := range stage.ExitConditions {
				stageElem.ExitConditions = append(stageElem.ExitConditions, ExitCondElem{
					Type:      ec.Type,
					Condition: ec.Condition,
				})
			}
			wfElem.Stages = append(wfElem.Stages, stageElem)
		}
		section.Workflows = append(section.Workflows, wfElem)
	}
	marshalSection(buf, depth, section)
}

func buildFigures(buf *bytes.Buffer, depth int) {
	// Stable ordering: sort by ID
	keys := make([]string, 0, len(FigureSpecs))
	for k := range FigureSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	section := FiguresSection{}
	for _, key := range keys {
		fig := FigureSpecs[key]
		figElem := FigureElem{
			Id:         fig.Id,
			Title:      fig.Title,
			Type:       fig.Type,
			SectionRef: fig.SectionRef,
		}
		// role-refs sorted
		sortedRoles := make([]types.RoleId, len(fig.RoleRefs))
		copy(sortedRoles, fig.RoleRefs)
		sort.Slice(sortedRoles, func(i, j int) bool {
			return string(sortedRoles[i]) < string(sortedRoles[j])
		})
		for _, rr := range sortedRoles {
			figElem.RoleRefs = append(figElem.RoleRefs, RefElem{Ref: string(rr)})
		}
		// workflow-refs sorted
		sortedWF := make([]string, len(fig.WorkflowRefs))
		copy(sortedWF, fig.WorkflowRefs)
		sort.Strings(sortedWF)
		for _, wr := range sortedWF {
			figElem.WorkflowRefs = append(figElem.WorkflowRefs, RefElem{Ref: wr})
		}
		// command-refs sorted
		sortedCR := make([]string, len(fig.CommandRefs))
		copy(sortedCR, fig.CommandRefs)
		sort.Strings(sortedCR)
		for _, cr := range sortedCR {
			figElem.CommandRefs = append(figElem.CommandRefs, RefElem{Ref: cr})
		}
		section.Figures = append(section.Figures, figElem)
	}
	marshalSection(buf, depth, section)
}

// phaseNumber returns the phase number for a PhaseId for sorting.
func phaseNumber(id protocol.PhaseId) int {
	spec, ok := PhaseSpecs[id]
	if !ok {
		return 0
	}
	return spec.Number
}

// phaseXMLId converts a PhaseId to its schema.xml id format (e.g. "p1", "p10").
// The XML schema uses p{N} identifiers, while Go uses descriptive names.
func phaseXMLId(id protocol.PhaseId) string {
	n := phaseNumber(id)
	if n == 0 {
		return string(id)
	}
	return "p" + strconv.Itoa(n)
}

// ─── Public API ───────────────────────────────────────────────────────────────

// GenerateSchema generates schema.xml from canonical Go spec data and writes
// the result to w.
//
// The output matches the structure of the Python gen_schema.py output:
//   - XML declaration with UTF-8 encoding
//   - <pasture-protocol version="2.0"> root element
//   - All sections with section-divider comments
//   - CDATA sections for <code> element content
//   - 2-space indentation throughout
//
// Returns an error only if writing to w fails.
func GenerateSchema(w io.Writer) error {
	content := generateSchemaContent()
	_, err := io.WriteString(w, content)
	return err
}

// GenerateSchemaToFile generates schema.xml, optionally prints a diff if the
// file already exists and opts.Diff is true, and writes if opts.Write is true.
//
// Returns the generated XML content as a string, and any error encountered
// during file I/O. Returns an error if:
//   - opts.Write is true and the parent directory does not exist or is not writable
//   - reading the existing file for diff comparison fails
func GenerateSchemaToFile(path string, opts GenerateOptions) (string, error) {
	content := generateSchemaContent()

	if opts.Diff {
		existing, err := os.ReadFile(path)
		if err == nil {
			oldContent := string(existing)
			if oldContent == content {
				fmt.Printf("No changes — %s is up to date.\n", path)
			} else {
				printUnifiedDiff(path, oldContent, content)
			}
		}
		// If file doesn't exist, no diff to show.
	}

	if opts.Write {
		existing, _ := os.ReadFile(path)
		if string(existing) != content {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return content, fmt.Errorf(
					"GenerateSchemaToFile: failed to write %q — "+
						"ensure the parent directory exists and is writable: %w",
					path, err,
				)
			}
		}
	}

	return content, nil
}

// generateSchemaContent builds and returns the full XML string.
func generateSchemaContent() string {
	var buf bytes.Buffer

	buf.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n")
	buf.WriteString("<pasture-protocol" + xmlAttr("version", "2.0") + ">\n")

	// Header comment
	buf.WriteString("  <!--\n")
	buf.WriteString("  Pasture Protocol Schema v2.0\n\n")
	buf.WriteString("  Canonical, machine-readable definition of the Pasture multi-agent protocol.\n")
	buf.WriteString("  All markdown documentation (PROCESS.md, AGENTS.md, SKILLS.md, etc.) is\n")
	buf.WriteString("  derived from this schema. Changes to the protocol MUST be reflected here first.\n\n")
	buf.WriteString("  Design: Boyce-Codd Normal Form (BCNF)\n")
	buf.WriteString("  - Each fact stored exactly once\n")
	buf.WriteString("  - Relationships via idref attributes, no duplication\n")
	buf.WriteString("  - No transitive dependencies\n")
	buf.WriteString("  - Enums define closed sets; entities reference enums by id\n")
	buf.WriteString("-->\n")

	sections := []struct {
		comment string
		build   func(*bytes.Buffer, int)
	}{
		{"ENUMERATIONS", buildEnums},
		{"LABELS (closed set)\n\n     Label schema: pasture:p{phase}-{domain}:s{step}-{type}\n     Special labels do not follow the phase pattern.", buildLabels},
		{"REVIEW AXES", buildReviewAxes},
		{"PHASES (12-phase lifecycle)\n\n     Order of operations is defined by:\n       1. phase/@number (global ordering)\n       2. substep/@order within a phase\n       3. substep/@execution + @parallel-group (concurrency)\n       4. transition/@condition (gate to next phase)", buildPhases},
		{"ROLES\n\n     Each role owns a set of phases and has access to specific commands.\n     The role-phase mapping is the primary relationship; commands are\n     grouped under their owning role.", buildRoles},
		{"COMMANDS (skills)\n\n     Each skill maps to a SKILL.md file in skills/, belongs to a role,\n     operates in specific phases, and may create specific labels on tasks.", buildCommands},
		{"HANDOFFS (actor-change transitions)\n\n     6 transitions require handoff documents.\n     Same-actor transitions (p5→p6, p6→p7) do NOT require handoffs.", buildHandoffs},
		{"CONSTRAINTS (Given/When/Then/Should)\n\n     Protocol-level constraints. Coding-standard constraints live in\n     CONSTRAINTS.md and are not duplicated here.", buildConstraints},
		{"TASK TITLE CONVENTIONS\n\n     Mapping from task titles to labels and creating roles.", buildTaskTitles},
		{"DOCUMENTS\n\n     Mapping from protocol documentation files to the entities they cover.", buildDocuments},
		{"DEPENDENCY DIRECTION (Beads)\n\n     Canonical definition of how work flows through the dependency tree.", buildDependencyModel},
		{"FOLLOW-UP LIFECYCLE (R6 from URD)\n\n     When code review produces IMPORTANT or MINOR findings, the supervisor\n     creates a follow-up epic that runs the same protocol phases with\n     FOLLOWUP_* prefixed task types. The IMPORTANT/MINOR leaf tasks from\n     the original review gain a second parent: the follow-up slice they\n     are assigned to (dual-parent).\n\n     Kind: Separate enum values (FOLLOWUP_URE, FOLLOWUP_SLICE, etc.).\n     Simple single-parent epic relationship — no followup-of-followup.", buildFollowupLifecycle},
		{"PROCEDURE STEPS\n\n     Per-role ordered steps (startup sequence for supervisor,\n     TDD layers for worker). Only roles with non-empty steps are listed.", buildProcedureSteps},
		{"CHECKLISTS\n\n     Per-role quality gate checklists for slice completion, review readiness,\n     and landing.", buildChecklists},
		{"COORDINATION COMMANDS\n\n     Beads coordination commands shared across roles and role-specific.", buildCoordinationCommands},
		{"WORKFLOWS\n\n     Named agent workflows: Ride the Wave (supervisor), Layer Cake (worker),\n     Architect State Flow (architect).", buildWorkflows},
		{"FIGURES\n\n     ASCII diagram figures associated with roles and workflows.\n     Content stored in YAML files, not in schema.xml.", buildFigures},
	}

	for _, sec := range sections {
		buf.WriteString(sectionComment(sec.comment, 1) + "\n")
		sec.build(&buf, 1)
	}

	buf.WriteString("</pasture-protocol>\n")

	return buf.String()
}

// printUnifiedDiff prints a simple line-by-line diff between old and new content.
func printUnifiedDiff(path, oldContent, newContent string) {
	// Simple unified diff implementation.
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	fmt.Printf("\n--- Unified diff for %s ---\n", path)
	fmt.Printf("--- a/%s\n", lastPathComponent(path))
	fmt.Printf("+++ b/%s\n", lastPathComponent(path))

	// Use a simple line-diff approach.
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if oldLine != "" {
				fmt.Printf("-%s\n", oldLine)
			}
			if newLine != "" {
				fmt.Printf("+%s\n", newLine)
			}
		}
	}

	fmt.Printf("--- End diff ---\n\n")
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func lastPathComponent(path string) string {
	// Find last slash.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// Compile-time check: regexp package used for test helpers only.
var _ = regexp.MustCompile

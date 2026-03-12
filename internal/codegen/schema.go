// Package codegen — schema.xml generation.
//
// This file ports gen_schema.py to Go. It generates schema.xml from
// the canonical Go spec data maps (ConstraintSpecs, PhaseSpecs, etc.)
// using manual XML building (bytes.Buffer + fmt.Fprintf) to achieve
// CDATA sections for <code> elements and fine-grained indentation
// control matching the Python output.
//
// Public API:
//
//	GenerateSchema(w io.Writer) error
//	GenerateSchemaToFile(path string, opts GenerateOptions) (string, error)
package codegen

import (
	"bytes"
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
		parts[i] = phaseXMLID(p)
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)

	w(d + "<enums>")

	// DomainType
	w(d1 + `<enum name="DomainType">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "user"), xmlAttr("description", "User-facing interaction (requests, elicitation, UAT)")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "plan"), xmlAttr("description", "Planning and design (proposals, reviews, ratification)")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "impl"), xmlAttr("description", "Implementation (slices, code review, landing)")))
	w(d1 + "</enum>")

	// VoteType
	w(d1 + `<enum name="VoteType">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "ACCEPT"), xmlAttr("description", "All review criteria satisfied; no BLOCKER items")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "REVISE"), xmlAttr("description", "BLOCKER issues found; must provide actionable feedback")))
	w(d1 + "</enum>")

	// SeverityLevel
	w(d1 + `<enum name="SeverityLevel">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "BLOCKER"), xmlAttr("blocks", "true"), xmlAttr("label", "aura:severity:blocker"), xmlAttr("description", "Security, type errors, test failures, broken production code paths")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "IMPORTANT"), xmlAttr("blocks", "false"), xmlAttr("label", "aura:severity:important"), xmlAttr("description", "Performance, missing validation, architectural concerns")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "MINOR"), xmlAttr("blocks", "false"), xmlAttr("label", "aura:severity:minor"), xmlAttr("description", "Style, optional optimizations, naming improvements")))
	w(d1 + "</enum>")

	// ExecutionMode
	w(d1 + `<enum name="ExecutionMode">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "sequential"), xmlAttr("description", "Must complete before next step starts")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "parallel"), xmlAttr("description", "Can run concurrently with sibling steps in same parallel-group")))
	w(d1 + "</enum>")

	// ContentLevel
	w(d1 + `<enum name="ContentLevel">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "full-provenance"), xmlAttr("description", "Full inline context with all decisions and rationale")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "summary-with-ids"), xmlAttr("description", "Summary with Beads task ID references")))
	w(d1 + "</enum>")

	// Classification axes comment
	w(d1 + comment(" Classification axes (s1_1-classify) "))

	// ClassificationScope
	w(d1 + `<enum name="ClassificationScope">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "single-file"), xmlAttr("description", "Change is isolated to a single file")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "module"), xmlAttr("description", "Change spans a module or package")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "cross-cutting"), xmlAttr("description", "Change affects multiple modules or subsystems")))
	w(d1 + "</enum>")

	// ClassificationComplexity
	w(d1 + `<enum name="ClassificationComplexity">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "low"), xmlAttr("description", "Straightforward implementation, familiar patterns")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "medium"), xmlAttr("description", "Some design decisions needed, moderate scope")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "high"), xmlAttr("description", "Significant design work, unfamiliar territory, or many moving parts")))
	w(d1 + "</enum>")

	// ClassificationRisk
	w(d1 + `<enum name="ClassificationRisk">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "internal-only"), xmlAttr("description", "No external API changes, no breaking changes")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "new-api"), xmlAttr("description", "Introduces new public interfaces or APIs")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "breaking-changes"), xmlAttr("description", "Modifies existing behavior or public contracts")))
	w(d1 + "</enum>")

	// ClassificationNovelty
	w(d1 + `<enum name="ClassificationNovelty">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "familiar"), xmlAttr("description", "Well-known patterns, team has done this before")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "new-territory"), xmlAttr("description", "Unfamiliar domain, requires research and exploration")))
	w(d1 + "</enum>")

	// ResearchDepth
	w(d1 + `<enum name="ResearchDepth">`)
	w(d2 + selfCloseTag("value", xmlAttr("id", "quick-scan"), xmlAttr("description", "Familiar domain, low complexity — brief prior art check (local only)")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "standard-research"), xmlAttr("description", "Moderate complexity or some novelty — find existing patterns and standards (local + docs)")))
	w(d2 + selfCloseTag("value", xmlAttr("id", "deep-dive"), xmlAttr("description", "High complexity, new territory, or high risk — thorough domain analysis (local + web)")))
	w(d1 + "</enum>")

	w(d + "</enums>")
}

func buildLabels(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)

	w(d + "<labels>")
	w(d1 + comment(" Phase labels (one per substep) "))

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

	for _, lid := range phaseLabelIDs {
		spec := LabelSpecs[lid]
		attrs := []string{xmlAttr("id", spec.ID), xmlAttr("value", spec.Value)}
		if spec.PhaseRef != "" {
			attrs = append(attrs, xmlAttr("phase-ref", spec.PhaseRef))
		}
		if spec.SubstepRef != "" {
			attrs = append(attrs, xmlAttr("substep-ref", spec.SubstepRef))
		}
		w(d1 + selfCloseTag("label", attrs...))
	}

	w(d1 + comment(" Special labels (not phase-scoped) "))
	for _, lid := range specialLabelIDs {
		spec := LabelSpecs[lid]
		attrs := []string{xmlAttr("id", spec.ID), xmlAttr("value", spec.Value), xmlAttr("special", "true")}
		if spec.Description != "" {
			attrs = append(attrs, xmlAttr("description", spec.Description))
		}
		if spec.SeverityRef != "" {
			attrs = append(attrs, xmlAttr("severity-ref", spec.SeverityRef))
		}
		w(d1 + selfCloseTag("label", attrs...))
	}

	w(d + "</labels>")
}

func buildReviewAxes(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	w(d + "<review-axes>")

	axisOrder := []string{"axis-correctness", "axis-test_quality", "axis-elegance"}
	for _, axisID := range axisOrder {
		spec, ok := ReviewAxisSpecs[axisID]
		if !ok {
			continue
		}
		w(d1 + openTag("axis",
			xmlAttr("id", spec.ID),
			xmlAttr("letter", spec.Letter),
			xmlAttr("name", spec.Name),
			xmlAttr("short", spec.Short),
		))
		w(d2 + "<key-questions>")
		for _, q := range spec.KeyQuestions {
			w(d3 + openTag("q") + xmlEscapeText(q) + "</q>")
		}
		w(d2 + "</key-questions>")
		w(d1 + "</axis>")
	}

	w(d + "</review-axes>")
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
	"directive":   "Supervisor launch prompt MUST start with Skill(/aura:supervisor)",
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
				// Extract substep id from label_ref: "L-p2s2_1" → "s2_1"
				label := tc.LabelRef[2:] // drop "L-" prefix: "p2s2_1"
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)
	d4 := indent(depth + 4)
	d5 := indent(depth + 5)

	phaseTaskTitles := buildPhaseTaskTitles()

	orderedPhaseIDs := []protocol.PhaseId{
		protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
		protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
		protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
		protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
	}

	w(d + "<phases>")

	for _, phaseID := range orderedPhaseIDs {
		spec, ok := PhaseSpecs[phaseID]
		if !ok {
			continue
		}
		pid := phaseXMLID(spec.ID)

		w(d1 + openTag("phase",
			xmlAttr("id", pid),
			xmlAttr("number", strconv.Itoa(spec.Number)),
			xmlAttr("domain", string(spec.Domain)),
			xmlAttr("name", spec.Name),
		))

		// Description
		if desc, ok := phaseDescriptions[pid]; ok {
			w(d2 + "<description>" + xmlEscapeText(desc) + "</description>")
		}

		// Substeps
		substeps := SubstepDataMap[pid]
		if len(substeps) > 0 {
			w(d2 + "<substeps>")
			for _, sd := range substeps {
				attrs := []string{
					xmlAttr("id", sd.ID),
					xmlAttr("type", sd.Type),
					xmlAttr("execution", sd.Execution),
					xmlAttr("order", strconv.Itoa(sd.Order)),
				}
				if sd.ParallelGroup != "" {
					attrs = append(attrs, xmlAttr("parallel-group", sd.ParallelGroup))
				}
				attrs = append(attrs, xmlAttr("label-ref", sd.LabelRef))

				w(d3 + openTag("substep", attrs...))
				w(d4 + "<description>" + xmlEscapeText(sd.Description) + "</description>")

				if sd.ExtraLabel != "" {
					w(d4 + selfCloseTag("extra-label", xmlAttr("ref", sd.ExtraLabel)))
				}

				if sd.Instances != nil {
					w(d4 + selfCloseTag("instances",
						xmlAttr("count", sd.Instances.Count),
						xmlAttr("per", sd.Instances.Per),
					))
				}

				if sd.StartupSequence {
					w(d4 + "<startup-sequence>")
					supSteps := ProcedureSteps[types.RoleSupervisor]
					layerNames := []string{"Types", "Tests", "Implementation"}
					for _, step := range supSteps {
						stepAttrs := []string{
							xmlAttr("order", strconv.Itoa(step.Order)),
							xmlAttr("id", step.ID),
						}
						if step.NextState != "" {
							stepAttrs = append(stepAttrs, xmlAttr("next-state", phaseXMLID(step.NextState)))
						}
						w(d5 + openTag("step", stepAttrs...))
						w(indent(depth+6) + "<instruction>" + xmlEscapeText(step.Instruction) + "</instruction>")
						if step.Command != "" {
							w(indent(depth+6) + "<command>" + xmlEscapeText(step.Command) + "</command>")
						}
						if step.Context != "" {
							w(indent(depth+6) + "<context>" + xmlEscapeText(step.Context) + "</context>")
						}
						_ = layerNames
						w(d5 + "</step>")
					}
					w(d4 + "</startup-sequence>")
				}

				w(d3 + "</substep>")
			}
			w(d2 + "</substeps>")
		}

		// Task-title(s)
		if tts, ok := phaseTaskTitles[pid]; ok {
			for _, tt := range tts {
				ttAttrs := []string{xmlAttr("pattern", tt["pattern"])}
				if sub, ok := tt["substep"]; ok {
					ttAttrs = append(ttAttrs, xmlAttr("substep", sub))
				}
				if conv, ok := tt["convention"]; ok {
					w(d2 + openTag("task-title", ttAttrs...))
					w(d3 + "<convention>" + xmlEscapeText(conv) + "</convention>")
					w(d2 + "</task-title>")
				} else {
					w(d2 + selfCloseTag("task-title", ttAttrs...))
				}
			}
		}

		// Special phase elements
		switch pid {
		case "p4":
			w(d2 + selfCloseTag("severity-tree",
				xmlAttr("enabled", "false"),
				xmlAttr("reason", "Plan reviews use binary ACCEPT/REVISE only"),
			))
		case "p6":
			w(d2 + selfCloseTag("same-actor-as",
				xmlAttr("phase-ref", "p5"),
				xmlAttr("note", "Architect performs p5, p6, p7 — no handoff between them"),
			))
		case "p9":
			w(d2 + "<tdd-layers>")
			workerSteps := ProcedureSteps[types.RoleWorker]
			layerNames := []string{"Types", "Tests", "Implementation"}
			for _, step := range workerSteps {
				name := ""
				if step.Order >= 1 && step.Order <= len(layerNames) {
					name = layerNames[step.Order-1]
				}
				w(d3 + selfCloseTag("layer",
					xmlAttr("number", strconv.Itoa(step.Order)),
					xmlAttr("name", name),
					xmlAttr("description", step.Instruction),
				))
			}
			w(d2 + "</tdd-layers>")
		case "p10":
			w(d2 + openTag("severity-tree",
				xmlAttr("enabled", "true"),
				xmlAttr("creation", "eager"),
			))
			w(d3 + "<rule>Always create 3 severity groups per review round, even if empty.</rule>")
			w(d3 + "<rule>Empty groups have no children and are closed immediately.</rule>")
			w(d3 + selfCloseTag("group",
				xmlAttr("severity-ref", "BLOCKER"),
				xmlAttr("label-ref", "L-sev-blocker"),
				xmlAttr("dual-parent", "true"),
			))
			w(d3 + selfCloseTag("group",
				xmlAttr("severity-ref", "IMPORTANT"),
				xmlAttr("label-ref", "L-sev-import"),
			))
			w(d3 + selfCloseTag("group",
				xmlAttr("severity-ref", "MINOR"),
				xmlAttr("label-ref", "L-sev-minor"),
			))
			w(d2 + "</severity-tree>")
			w(d2 + selfCloseTag("followup-epic",
				xmlAttr("label-ref", "L-followup"),
				xmlAttr("trigger", "review-completion AND (IMPORTANT OR MINOR findings exist)"),
				xmlAttr("gated-on-blocker", "false"),
				xmlAttr("owner-role", "supervisor"),
			))
		}

		// Transitions
		transitions := spec.Transitions
		if len(transitions) > 0 {
			w(d2 + "<transitions>")
			for _, t := range transitions {
				tAttrs := []string{
					xmlAttr("to-phase", phaseXMLID(t.ToPhase)),
					xmlAttr("condition", t.Condition),
				}
				if t.Action != "" {
					tAttrs = append(tAttrs, xmlAttr("action", t.Action))
				}

				// Check if this is the p7→p8 transition needing skill-invocation.
				isP7toP8 := pid == "p7" && t.ToPhase == protocol.PhaseImplPlan
				if isP7toP8 {
					w(d3 + openTag("transition", tAttrs...))
					w(d4 + selfCloseTag("skill-invocation",
						xmlAttr("target-role", p7SkillInvocation["target-role"]),
						xmlAttr("command-ref", p7SkillInvocation["command-ref"]),
						xmlAttr("directive", p7SkillInvocation["directive"]),
					))
					w(d3 + "</transition>")
				} else {
					w(d3 + selfCloseTag("transition", tAttrs...))
				}
			}
			w(d2 + "</transitions>")
		}

		w(d1 + "</phase>")
	}

	w(d + "</phases>")
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
	"architect": "aura:p1-user, aura:p2-user, aura:p3-plan, aura:p4-plan, aura:p5-user, aura:p6-plan, aura:p7-plan",
	"reviewer": "aura:p4-plan:s4-review, aura:p10-impl:s10-review, " +
		"aura:severity:blocker, aura:severity:important, aura:severity:minor",
	"supervisor": "aura:p7-plan, aura:p8-impl, aura:p9-impl, aura:p10-impl, " +
		"aura:p11-user, aura:p12-impl, aura:epic-followup",
	"worker": "aura:p9-impl:s9-slice",
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	roleOrder := []types.RoleId{
		types.RoleEpoch, types.RoleArchitect, types.RoleReviewer,
		types.RoleSupervisor, types.RoleWorker,
	}

	w(d + "<roles>")

	for _, roleID := range roleOrder {
		spec, ok := RoleSpecs[roleID]
		if !ok {
			continue
		}
		rid := string(spec.ID)

		w(d1 + openTag("role",
			xmlAttr("id", rid),
			xmlAttr("name", spec.Name),
			xmlAttr("description", spec.Description),
		))

		// owns-phases: sort by phase number
		sorted := make([]protocol.PhaseId, len(spec.OwnedPhases))
		copy(sorted, spec.OwnedPhases)
		sort.Slice(sorted, func(i, j int) bool {
			pi := phaseNumber(sorted[i])
			pj := phaseNumber(sorted[j])
			return pi < pj
		})
		w(d2 + "<owns-phases>")
		for _, phaseRef := range sorted {
			w(d3 + selfCloseTag("phase-ref", xmlAttr("ref", phaseXMLID(phaseRef))))
		}
		w(d2 + "</owns-phases>")

		// Delegates (epoch only)
		if delegates, ok := roleDelegates[rid]; ok {
			w(d2 + "<delegates>")
			for _, del := range delegates {
				w(d3 + selfCloseTag("delegate",
					xmlAttr("to-role", del["to-role"]),
					xmlAttr("phases", del["phases"]),
				))
			}
			w(d2 + "</delegates>")
		}

		// Label awareness
		if la, ok := roleLabelAwareness[rid]; ok {
			w(d2 + "<label-awareness>")
			w("\n      " + la + "\n    ")
			w(d2 + "</label-awareness>")
		}

		// Uses axes (reviewer)
		if axes, ok := roleUsesAxes[rid]; ok {
			w(d2 + "<uses-axes>")
			for _, axRef := range axes {
				w(d3 + selfCloseTag("axis-ref", xmlAttr("ref", axRef)))
			}
			w(d2 + "</uses-axes>")
		}

		// Invariants (supervisor)
		if invs, ok := roleInvariants[rid]; ok {
			w(d2 + "<invariants>")
			for _, inv := range invs {
				w(d3 + "<invariant>" + xmlEscapeText(inv) + "</invariant>")
			}
			w(d2 + "</invariants>")
		}

		// Tools, model, thinking
		if len(spec.Tools) > 0 {
			w(d2 + "<tools>" + xmlEscapeText(strings.Join(spec.Tools, ", ")) + "</tools>")
		}
		if spec.Model != "" {
			w(d2 + "<model>" + xmlEscapeText(spec.Model) + "</model>")
		}
		if spec.Thinking != "" {
			w(d2 + "<thinking>" + xmlEscapeText(spec.Thinking) + "</thinking>")
		}

		// Ownership model (worker)
		if om, ok := roleOwnershipModel[rid]; ok {
			w(d2 + "<ownership-model>")
			w("\n      " + om + "\n    ")
			w(d2 + "</ownership-model>")
		}

		// Introduction
		if spec.Introduction != "" {
			w(d2 + "<introduction>" + xmlEscapeText(spec.Introduction) + "</introduction>")
		}

		// Ownership narrative
		if spec.OwnershipNarrative != "" {
			w(d2 + "<ownership-narrative>" + xmlEscapeText(spec.OwnershipNarrative) + "</ownership-narrative>")
		}

		// Behaviors
		if len(spec.Behaviors) > 0 {
			w(d2 + "<behaviors>")
			for _, b := range spec.Behaviors {
				w(d3 + selfCloseTag("behavior",
					xmlAttr("id", b.ID),
					xmlAttr("given", b.Given),
					xmlAttr("when", b.When),
					xmlAttr("then", b.Then),
					xmlAttr("should-not", b.ShouldNot),
				))
			}
			w(d2 + "</behaviors>")
		}

		w(d1 + "</role>")
	}

	w(d + "</roles>")
}

// commandOrder is the canonical ordering of commands in schema.xml.
// Mirrors Python command_order list in _build_commands.
var commandOrder = []string{
	// Orchestration
	"cmd-epoch", "cmd-plan", "cmd-status",
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
	// Messaging
	"cmd-msg-send", "cmd-msg-receive", "cmd-msg-broadcast", "cmd-msg-ack",
	// Exploration
	"cmd-explore", "cmd-research",
	// Utilities
	"cmd-test", "cmd-feedback",
}

// commandGroupComments marks the start of each command group with a comment.
var commandGroupComments = map[string]string{
	"cmd-epoch":       " ── Orchestration ──────────────────────────────────────────────── ",
	"cmd-user-request": " ── User interaction ───────────────────────────────────────── ",
	"cmd-architect":   " ── Architect ──────────────────────────────────────────────────── ",
	"cmd-supervisor":  " ── Supervisor ─────────────────────────────────────────────────── ",
	"cmd-worker":      " ── Worker ─────────────────────────────────────────────────────── ",
	"cmd-reviewer":    " ── Reviewer ───────────────────────────────────────────────────── ",
	"cmd-impl-slice":  " ── Implementation coordination ────────────────────────────────── ",
	"cmd-msg-send":    " ── Messaging (Beads-based IPC) ────────────────────────────────── ",
	"cmd-explore":     " ── Exploration ────────────────────────────────────────────────── ",
	"cmd-test":        " ── Utilities ──────────────────────────────────────────────────── ",
}

func buildCommands(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	w(d + "<commands>")

	for _, cid := range commandOrder {
		spec, ok := CommandSpecs[cid]
		if !ok {
			continue
		}

		if grpComment, ok := commandGroupComments[cid]; ok {
			w(d1 + comment(grpComment))
		}

		cmdAttrs := []string{xmlAttr("id", spec.ID), xmlAttr("name", spec.Name)}
		if spec.RoleRef != "" {
			cmdAttrs = append(cmdAttrs, xmlAttr("role-ref", string(spec.RoleRef)))
		}
		cmdAttrs = append(cmdAttrs, xmlAttr("description", spec.Description))

		w(d1 + openTag("command", cmdAttrs...))

		// phases
		if len(spec.Phases) > 0 {
			w(d2 + "<phases>")
			for _, phaseRef := range spec.Phases {
				w(d3 + selfCloseTag("phase-ref", xmlAttr("ref", phaseXMLID(phaseRef))))
			}
			w(d2 + "</phases>")
		}

		// creates-labels
		if len(spec.CreatesLabels) > 0 {
			w(d2 + "<creates-labels>")
			for _, labelRef := range spec.CreatesLabels {
				w(d3 + selfCloseTag("label-ref", xmlAttr("ref", labelRef)))
			}
			w(d2 + "</creates-labels>")
		}

		// file
		w(d2 + "<file>" + xmlEscapeText(spec.File) + "</file>")

		// cmd-explore special note
		if cid == "cmd-explore" {
			w(d2 + "<note>Used in Phase 1 (s1_3) by architect, and in Phase 8 by supervisor&#39;s ephemeral Explore subagents.</note>")
		}

		w(d1 + "</command>")
	}

	w(d + "</commands>")
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
		"directive": "Skill(/aura:supervisor)",
		"note":      "Supervisor launch prompt MUST start with this invocation. Without it, supervisor skips leaf task creation.",
	},
	"h2": {
		"directive": "Skill(/aura:worker)",
		"note":      "Worker message MUST include explicit instruction to call this skill.",
	},
	"h3": {
		"directive": "Skill(/aura:reviewer)",
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)

	w(d + openTag("handoffs",
		xmlAttr("storage-pattern", ".git/.aura/handoff/{request-task-id}/{source}-to-{target}.md"),
	))

	handoffOrderList := []string{"h1", "h2", "h3", "h4", "h5", "h6"}

	for _, hid := range handoffOrderList {
		spec, ok := HandoffSpecs[hid]
		if !ok {
			continue
		}

		hAttrs := []string{
			xmlAttr("id", spec.ID),
			xmlAttr("source-role", string(spec.SourceRole)),
			xmlAttr("target-role", string(spec.TargetRole)),
			xmlAttr("at-phase", phaseXMLID(spec.AtPhase)),
			xmlAttr("content-level", spec.ContentLevel),
		}
		if fp, ok := handoffFilePatterns[hid]; ok {
			hAttrs = append(hAttrs, xmlAttr("file-pattern", fp))
		}
		if trigger, ok := handoffTriggers[hid]; ok {
			if hid == "h6" {
				hAttrs = append(hAttrs, xmlAttr("context", trigger))
			} else {
				hAttrs = append(hAttrs, xmlAttr("trigger", trigger))
			}
		}

		w(d1 + openTag("handoff", hAttrs...))

		// required-fields
		w(d2 + "<required-fields>")
		w("\n      " + strings.Join(spec.RequiredFields, ", ") + "\n    ")
		w(d2 + "</required-fields>")

		// skill-invocation
		if si, ok := handoffSkillInvocations[hid]; ok {
			siAttrs := []string{xmlAttr("directive", si["directive"])}
			if note, ok := si["note"]; ok {
				siAttrs = append(siAttrs, xmlAttr("note", note))
			}
			w(d2 + selfCloseTag("skill-invocation", siAttrs...))
		}

		// notes
		if note, ok := handoffNotes[hid]; ok {
			w(d2 + "<note>")
			w("\n      " + note + "\n    ")
			w(d2 + "</note>")
		}

		w(d1 + "</handoff>")
	}

	// same-actor-transitions
	w(d1 + openTag("same-actor-transitions", xmlAttr("note", "No handoff document needed")))
	w(d2 + selfCloseTag("transition",
		xmlAttr("from-phase", "p5"),
		xmlAttr("to-phase", "p6"),
		xmlAttr("actor", "architect"),
	))
	w(d2 + selfCloseTag("transition",
		xmlAttr("from-phase", "p6"),
		xmlAttr("to-phase", "p7"),
		xmlAttr("actor", "architect"),
	))
	w(d1 + "</same-actor-transitions>")

	w(d + "</handoffs>")
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
			xmlAttr("id", spec.ID),
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
					xmlAttr("id", ex.ID),
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)

	w(d + "<task-titles>")

	for _, tc := range TitleConventions {
		attrs := []string{
			xmlAttr("pattern", tc.Pattern),
			xmlAttr("label-ref", tc.LabelRef),
			xmlAttr("created-by", tc.CreatedBy),
		}
		if tc.PhaseRef != "" {
			attrs = append(attrs, xmlAttr("phase-ref", tc.PhaseRef))
		}
		if tc.ExtraLabelRef != "" {
			attrs = append(attrs, xmlAttr("extra-label-ref", tc.ExtraLabelRef))
		}
		if tc.Note != "" {
			attrs = append(attrs, xmlAttr("note", tc.Note))
		}
		w(d1 + selfCloseTag("title-convention", attrs...))
	}

	w(d + "</task-titles>")
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
			purpose: "Command reference: all /aura:* skills mapped to phase and role",
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

	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	w(d + "<documents>")

	allDocs := append(docs, rootDocs...)

	// Insert the root-docs comment before the root docs start.
	rootDocsStart := len(docs)

	for i, doc := range allDocs {
		if i == rootDocsStart {
			w(d1 + comment(" Root-level docs (project-specific, not protocol-reusable) "))
		}
		w(d1 + openTag("document",
			xmlAttr("id", doc.id),
			xmlAttr("path", doc.path),
			xmlAttr("purpose", doc.purpose),
		))
		w(d2 + "<covers>")
		for _, cover := range doc.covers {
			attrs := []string{
				xmlAttr("type", cover["type"]),
				xmlAttr("depth", cover["depth"]),
			}
			if refs, ok := cover["refs"]; ok {
				attrs = append(attrs, xmlAttr("refs", refs))
			}
			if note, ok := cover["note"]; ok {
				attrs = append(attrs, xmlAttr("note", note))
			}
			w(d3 + selfCloseTag("entity", attrs...))
		}
		w(d2 + "</covers>")
		w(d1 + "</document>")
	}

	w(d + "</documents>")
}

func buildDependencyModel(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)

	w(d + "<dependency-model>")
	w(d1 + "<rule>")
	w("    Parent (stays open) is blocked-by child (must finish first).")
	w("    Work flows bottom-up; closure flows top-down.")
	w("  ")
	w(d1 + "</rule>")
	w(d1 + "<canonical-chain>")
	w("    REQUEST → blocked-by ELICIT → blocked-by PROPOSAL")
	w("      → blocked-by IMPL_PLAN → blocked-by SLICE-N → blocked-by leaf tasks")
	w("  ")
	w(d1 + "</canonical-chain>")
	w(d1 + "<command>bd dep add {parent-id} --blocked-by {child-id}</command>")
	w(d1 + "<anti-pattern>bd dep add {child-id} --blocked-by {parent-id}</anti-pattern>")
	w(d1 + openTag("reference-links", xmlAttr("note", "URD and other reference docs use frontmatter, not blocking deps")))
	w(d2 + "<pattern>")
	w("      description frontmatter:")
	w("        references:")
	w("          urd: {urd-task-id}")
	w("          request: {request-task-id}")
	w("    ")
	w(d2 + "</pattern>")
	w(d1 + "</reference-links>")
	w(d + "</dependency-model>")
}

func buildFollowupLifecycle(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)

	w(d + "<followup-lifecycle>")
	w(d1 + "<trigger>Code review completion AND (IMPORTANT OR MINOR findings exist)</trigger>")
	w(d1 + "<owner-role>supervisor</owner-role>")
	w(d1 + "<gated-on-blocker>false</gated-on-blocker>")

	w(d1 + openTag("dependency-chain", xmlAttr("note", "Same protocol phases but with FOLLOWUP_ prefix")))
	w(d2 + comment(
		"\n      FOLLOWUP epic (aura:epic-followup)\n" +
			"        ├── relates_to: original URD\n" +
			"        ├── relates_to: original REVIEW-A/B/C tasks\n" +
			"        └── blocked-by: FOLLOWUP_URE\n" +
			"              └── blocked-by: FOLLOWUP_URD\n" +
			"                    └── blocked-by: FOLLOWUP_PROPOSAL-1\n" +
			"                          └── blocked-by: FOLLOWUP_IMPL_PLAN\n" +
			"                                ├── blocked-by: FOLLOWUP_SLICE-1\n" +
			"                                │     ├── blocked-by: important-leaf-task-...\n" +
			"                                │     └── blocked-by: minor-leaf-task-...\n" +
			"                                └── blocked-by: FOLLOWUP_SLICE-2\n" +
			"                                      └── blocked-by: ...\n    ",
	))

	followupSteps := []map[string]string{
		{"task-title": "FOLLOWUP: {description}", "phase-ref": "p10", "description": "Epic created by supervisor. References original URD and review tasks."},
		{"task-title": "FOLLOWUP_URE: {description}", "phase-ref": "p2", "description": "Scoping URE with user to determine which findings to address."},
		{"task-title": "FOLLOWUP_URD: {description}", "phase-ref": "p2", "description": "Requirements doc for follow-up scope. References original URD."},
		{"task-title": "FOLLOWUP_PROPOSAL-{N}: {description}", "phase-ref": "p3", "description": "Proposal accounting for original URD + FOLLOWUP_URD + outstanding findings."},
		{"task-title": "FOLLOWUP_IMPL_PLAN: {description}", "phase-ref": "p8", "description": "Supervisor decomposes follow-up into slices."},
		{"task-title": "FOLLOWUP_SLICE-{N}: {description}", "phase-ref": "p9", "description": "Each slice adopts original IMPORTANT/MINOR leaf tasks as children."},
	}
	for _, step := range followupSteps {
		w(d2 + selfCloseTag("step",
			xmlAttr("task-title", step["task-title"]),
			xmlAttr("phase-ref", step["phase-ref"]),
			xmlAttr("description", step["description"]),
		))
	}
	w(d1 + "</dependency-chain>")

	w(d1 + "<leaf-task-adoption>")
	w(d2 + "<rule>")
	w("      When supervisor creates FOLLOWUP_SLICE-N, the IMPORTANT/MINOR leaf tasks")
	w("      from the original review gain a second parent: the follow-up slice.")
	w("      This is the same dual-parent pattern as BLOCKER findings.")
	w("    ")
	w(d2 + "</rule>")
	w(d2 + "<command>")
	w("      bd dep add {followup-slice-id} --blocked-by {important-leaf-task-id}")
	w("      bd dep add {followup-slice-id} --blocked-by {minor-leaf-task-id}")
	w("    ")
	w(d2 + "</command>")
	w(d2 + "<note>")
	w("      Leaf tasks retain their original parent (the severity group from the original review)")
	w("      AND gain the follow-up slice as a second parent. Both must close for the leaf to be")
	w("      fully resolved.")
	w("    ")
	w(d2 + "</note>")
	w(d1 + "</leaf-task-adoption>")

	w(d1 + "<references>")
	w(d2 + selfCloseTag("ref",
		xmlAttr("type", "relates_to"),
		xmlAttr("target", "original URD"),
		xmlAttr("note", "Follow-up epic references original URD via frontmatter"),
	))
	w(d2 + selfCloseTag("ref",
		xmlAttr("type", "relates_to"),
		xmlAttr("target", "original REVIEW tasks"),
		xmlAttr("note", "Follow-up epic references review tasks via frontmatter"),
	))
	w(d1 + "</references>")

	w(d1 + openTag("handoff-chain", xmlAttr("note", "How handoffs flow through the follow-up lifecycle")))
	w(d2 + comment(
		"\n      The follow-up lifecycle uses 6 handoff transitions (h1-h6), where h6 is unique to the follow-up lifecycle\n" +
			"      but scoped to the follow-up epic. The storage path changes to use the\n" +
			"      follow-up epic ID instead of the original request ID.\n\n" +
			"      Storage: .git/.aura/handoff/{followup-epic-id}/{source}-to-{target}.md\n    ",
	))

	handoffChainSteps := []map[string]string{
		{"order": "1", "handoff-ref": "h5", "description": "Reviewer → Followup: Bridge from original review to follow-up epic. Created by supervisor when IMPORTANT/MINOR findings exist. This handoff STARTS the follow-up lifecycle."},
		{"order": "2", "handoff-ref": "none", "same-actor": "true", "description": "Supervisor creates FOLLOWUP_URE (same actor — supervisor owns follow-up epic and initiates scoping)"},
		{"order": "3", "handoff-ref": "none", "same-actor": "true", "description": "Supervisor creates FOLLOWUP_URD (same actor within Phase 2 — supervisor synthesizes follow-up requirements)"},
		{"order": "4", "handoff-ref": "h6", "description": "Supervisor → Architect: Hands off completed FOLLOWUP_URE + FOLLOWUP_URD to architect for FOLLOWUP_PROPOSAL creation. Architect receives scoped findings and requirements."},
		{"order": "5", "handoff-ref": "h1", "description": "Architect → Supervisor: After FOLLOWUP_PROPOSAL is ratified, architect hands off to supervisor for FOLLOWUP_IMPL_PLAN. Handoff doc references original URD, FOLLOWUP_URD, and outstanding findings."},
		{"order": "6", "handoff-ref": "h2", "description": "Supervisor → Worker: FOLLOWUP_SLICE-N assignment. Worker receives both the follow-up slice spec AND the original leaf task IDs they must resolve."},
		{"order": "7", "handoff-ref": "h3", "description": "Supervisor → Reviewer: Code review of follow-up slices. Reviewer receives follow-up context + original findings being addressed."},
		{"order": "8", "handoff-ref": "h4", "description": "Worker → Reviewer: Worker completes follow-up slice. Handoff includes which original leaf tasks were resolved."},
	}
	for _, step := range handoffChainSteps {
		attrs := []string{
			xmlAttr("order", step["order"]),
			xmlAttr("handoff-ref", step["handoff-ref"]),
			xmlAttr("description", step["description"]),
		}
		if sa, ok := step["same-actor"]; ok {
			attrs = append(attrs, xmlAttr("same-actor", sa))
		}
		w(d3 + selfCloseTag("transition", attrs...))
	}
	w(d1 + "</handoff-chain>")

	w(d + "</followup-lifecycle>")
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

	for _, roleID := range roleOrder {
		steps, ok := ProcedureSteps[roleID]
		if !ok || len(steps) == 0 {
			continue
		}

		w(d1 + openTag("role", xmlAttr("ref", string(roleID))))
		for _, step := range steps {
			stepAttrs := []string{
				xmlAttr("order", strconv.Itoa(step.Order)),
				xmlAttr("id", step.ID),
			}
			if step.NextState != "" {
				stepAttrs = append(stepAttrs, xmlAttr("next-state", phaseXMLID(step.NextState)))
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
					xmlAttr("id", ex.ID),
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
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)

	w(d + "<checklists>")

	// Stable ordering: sort by key
	keys := make([]string, 0, len(ChecklistSpecs))
	for k := range ChecklistSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		cl := ChecklistSpecs[key]
		w(d1 + openTag("checklist",
			xmlAttr("id", key),
			xmlAttr("role-ref", string(cl.RoleRef)),
			xmlAttr("gate", cl.Gate),
		))
		for _, item := range cl.Items {
			required := "false"
			if item.Required {
				required = "true"
			}
			w(d2 + openTag("item",
				xmlAttr("id", item.ID),
				xmlAttr("required", required),
			) + xmlEscapeText(item.Text) + "</item>")
		}
		w(d1 + "</checklist>")
	}

	w(d + "</checklists>")
}

func buildCoordinationCommands(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)

	w(d + "<coordination-commands>")

	// Stable ordering: sort by ID
	keys := make([]string, 0, len(CoordinationCommands))
	for k := range CoordinationCommands {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		cmd := CoordinationCommands[key]
		attrs := []string{
			xmlAttr("id", cmd.ID),
			xmlAttr("action", cmd.Action),
			xmlAttr("template", cmd.Template),
		}
		if cmd.RoleRef != "" {
			attrs = append(attrs, xmlAttr("role-ref", string(cmd.RoleRef)))
		}
		if cmd.Shared {
			attrs = append(attrs, xmlAttr("shared", "true"))
		}
		w(d1 + selfCloseTag("coord-cmd", attrs...))
	}

	w(d + "</coordination-commands>")
}

func buildWorkflows(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)
	d3 := indent(depth + 3)
	d4 := indent(depth + 4)

	w(d + "<workflows>")

	// Stable ordering: sort by ID
	keys := make([]string, 0, len(WorkflowSpecs))
	for k := range WorkflowSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		wf := WorkflowSpecs[key]
		w(d1 + openTag("workflow",
			xmlAttr("id", wf.ID),
			xmlAttr("name", wf.Name),
			xmlAttr("role-ref", string(wf.RoleRef)),
			xmlAttr("description", wf.Description),
		))
		for _, stage := range wf.Stages {
			stageAttrs := []string{
				xmlAttr("id", stage.ID),
				xmlAttr("name", stage.Name),
				xmlAttr("order", strconv.Itoa(stage.Order)),
				xmlAttr("execution", stage.Execution),
			}
			if stage.PhaseRef != "" {
				stageAttrs = append(stageAttrs, xmlAttr("phase-ref", phaseXMLID(stage.PhaseRef)))
			}
			w(d2 + openTag("stage", stageAttrs...))
			for _, action := range stage.Actions {
				actionAttrs := []string{
					xmlAttr("id", action.ID),
					xmlAttr("instruction", action.Instruction),
				}
				if action.Command != "" {
					actionAttrs = append(actionAttrs, xmlAttr("command", action.Command))
				}
				w(d3 + selfCloseTag("action", actionAttrs...))
			}
			for _, ec := range stage.ExitConditions {
				w(d3 + selfCloseTag("exit-condition",
					xmlAttr("type", ec.Type),
					xmlAttr("condition", ec.Condition),
				))
			}
			w(d2 + "</stage>")
		}

		// Per-workflow extra: checklist items embedded under certain workflows.
		// (Handled in the stage loop above via actions/exit-conditions.)
		_ = d4 // unused but may be needed for future nesting
		w(d1 + "</workflow>")
	}

	w(d + "</workflows>")
}

func buildFigures(buf *bytes.Buffer, depth int) {
	w := func(s string) { buf.WriteString(s + "\n") }
	d := indent(depth)
	d1 := indent(depth + 1)
	d2 := indent(depth + 2)

	w(d + "<figures>")

	// Stable ordering: sort by ID
	keys := make([]string, 0, len(FigureSpecs))
	for k := range FigureSpecs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		fig := FigureSpecs[key]
		w(d1 + openTag("figure",
			xmlAttr("id", fig.ID),
			xmlAttr("title", fig.Title),
			xmlAttr("type", fig.Type),
			xmlAttr("section-ref", fig.SectionRef),
		))
		// role-refs sorted
		sortedRoles := make([]types.RoleId, len(fig.RoleRefs))
		copy(sortedRoles, fig.RoleRefs)
		sort.Slice(sortedRoles, func(i, j int) bool {
			return string(sortedRoles[i]) < string(sortedRoles[j])
		})
		for _, rr := range sortedRoles {
			w(d2 + selfCloseTag("role-ref", xmlAttr("ref", string(rr))))
		}
		// workflow-refs sorted
		sortedWF := make([]string, len(fig.WorkflowRefs))
		copy(sortedWF, fig.WorkflowRefs)
		sort.Strings(sortedWF)
		for _, wr := range sortedWF {
			w(d2 + selfCloseTag("workflow-ref", xmlAttr("ref", wr)))
		}
		// command-refs sorted
		sortedCR := make([]string, len(fig.CommandRefs))
		copy(sortedCR, fig.CommandRefs)
		sort.Strings(sortedCR)
		for _, cr := range sortedCR {
			w(d2 + selfCloseTag("command-ref", xmlAttr("ref", cr)))
		}
		w(d1 + "</figure>")
	}

	w(d + "</figures>")
}

// phaseNumber returns the phase number for a PhaseId for sorting.
func phaseNumber(id protocol.PhaseId) int {
	spec, ok := PhaseSpecs[id]
	if !ok {
		return 0
	}
	return spec.Number
}

// phaseXMLID converts a PhaseId to its schema.xml id format (e.g. "p1", "p10").
// The XML schema uses p{N} identifiers, while Go uses descriptive names.
func phaseXMLID(id protocol.PhaseId) string {
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
//   - <aura-protocol version="2.0"> root element
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
	buf.WriteString("<aura-protocol" + xmlAttr("version", "2.0") + ">\n")

	// Header comment
	buf.WriteString("  <!--\n")
	buf.WriteString("  Aura Protocol Schema v2.0\n\n")
	buf.WriteString("  Canonical, machine-readable definition of the Aura multi-agent protocol.\n")
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
		{"LABELS (closed set)\n\n     Label schema: aura:p{phase}-{domain}:s{step}-{type}\n     Special labels do not follow the phase pattern.", buildLabels},
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

	buf.WriteString("</aura-protocol>\n")

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

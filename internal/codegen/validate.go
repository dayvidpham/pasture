// Package codegen — 3-layer schema.xml validator.
//
// This file provides the full implementation of the 3-layer schema.xml
// validator ported from scripts/validate_schema.py. It parses an XML document
// into a generic XMLNode tree and runs three validation passes:
//
//  1. Structural (buildIndex) — required attributes, integer attrs, duplicate IDs.
//  2. Referential (checkRefs) — all cross-references resolve to known IDs.
//  3. Semantic (checkSemantics) — logical protocol-level rules (sequential
//     phase numbers, domain consistency, label uniqueness, etc.).
//
// Public API:
//
//	ValidateSchema(r io.Reader) ([]ValidationError, error)
//	ValidateTree(root *XMLNode) []ValidationError
package codegen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ErrorLayer categorizes validation errors by their detection layer.
// Values match the Python ErrorLayer enum wire values.
type ErrorLayer string

const (
	// LayerStructural covers missing required attributes, duplicate IDs,
	// and malformed XML.
	LayerStructural ErrorLayer = "Structural"

	// LayerReferential covers references (phase-ref, role-ref, etc.) that
	// point to IDs not defined elsewhere in the document.
	LayerReferential ErrorLayer = "Referential Integrity"

	// LayerSemantic covers logical inconsistencies such as out-of-order
	// phase numbers, duplicate axis letters, or invalid enum values.
	LayerSemantic ErrorLayer = "Semantic"
)

// ValidationError represents a single schema validation finding.
// All three fields are always populated; an empty ElementPath indicates the
// error is at the document root level.
type ValidationError struct {
	// Layer is the detection layer that produced this error.
	Layer ErrorLayer

	// ElementPath is the XPath-style description of the offending element,
	// e.g. "phase[@id='p1']/substep[@id='s1_1']".
	ElementPath string

	// Message describes what is wrong, why, and how to fix it.
	Message string
}

// SchemaIndex holds all IDs and metadata extracted during structural
// validation. It is populated by buildIndex and consumed by checkRefs and
// checkSemantics. Mirrors the Python SchemaIndex dataclass.
type SchemaIndex struct {
	// ── ID sets ───────────────────────────────────────────────────────────

	PhaseIDs      map[string]bool
	SubstepIDs    map[string]bool
	LabelIDs      map[string]bool
	RoleIDs       map[string]bool
	CommandIDs    map[string]bool
	AxisIDs       map[string]bool
	HandoffIDs    map[string]bool
	ConstraintIDs map[string]bool
	DocumentIDs   map[string]bool
	TeamIDs       map[string]bool
	WorkflowIDs   map[string]bool
	SeverityIDs   map[string]bool

	// EnumValueIDs maps enum name → set of value IDs defined within it.
	EnumValueIDs map[string]map[string]bool

	// ── Metadata for semantic checks ──────────────────────────────────────

	// PhaseNumbers maps phase_id → numeric order (e.g. "p1" → 1).
	PhaseNumbers map[string]int

	// PhaseDomains maps phase_id → domain string (e.g. "p1" → "user").
	PhaseDomains map[string]string

	// PhaseSubstepOrders maps phase_id → ordered list of substep entries.
	PhaseSubstepOrders map[string][]SubstepOrderEntry

	// LabelValues maps label_id → value string (e.g. "L-p1s1_1" → "aura:p1-user:s1_1-request").
	LabelValues map[string]string

	// AxisLetters maps axis_id → letter string (e.g. "axis-correctness" → "A").
	AxisLetters map[string]string

	// RolePhaseRefs maps role_id → set of phase_ids that role owns.
	RolePhaseRefs map[string]map[string]bool

	// StartupStepOrders maps substep_id → slice of step order values found
	// inside that substep's <startup-sequence>.
	StartupStepOrders map[string][]int
}

// SubstepOrderEntry holds ordering metadata for a single substep within a
// phase. Mirrors the Python tuple (id, order, execution) from phase_substep_orders.
type SubstepOrderEntry struct {
	ID        string
	Order     int
	Execution string // "sequential" or "parallel"
}

// XMLNode is a generic XML tree node for validator tree operations.
// It can represent any element in the schema document and is used by
// ValidateTree to walk the document without requiring concrete struct types.
type XMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []*XMLNode `xml:",any"`
	Text     string     `xml:",chardata"`
}

// UnmarshalXML implements xml.Unmarshaler to parse an XMLNode tree from an
// XML decoder. This gives us a fully recursive generic tree without needing
// concrete struct types for every element.
func (n *XMLNode) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	n.XMLName = start.Name
	n.Attrs = start.Attr
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child := &XMLNode{}
			if err := d.DecodeElement(child, &t); err != nil {
				return err
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			n.Text += string(t)
		case xml.EndElement:
			return nil
		}
	}
}

// ── XMLNode helpers ────────────────────────────────────────────────────────────

// Attr returns the value of the named attribute, or "" if not present.
func (n *XMLNode) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// Find returns the first direct child whose local tag matches tag, or nil.
func (n *XMLNode) Find(tag string) *XMLNode {
	for _, c := range n.Children {
		if c.XMLName.Local == tag {
			return c
		}
	}
	return nil
}

// FindAll returns all direct children whose local tag matches tag.
func (n *XMLNode) FindAll(tag string) []*XMLNode {
	var out []*XMLNode
	for _, c := range n.Children {
		if c.XMLName.Local == tag {
			out = append(out, c)
		}
	}
	return out
}

// Iter recursively finds all descendant nodes (including self) whose local
// tag matches tag, in document order.
func (n *XMLNode) Iter(tag string) []*XMLNode {
	var out []*XMLNode
	n.iterInto(tag, &out)
	return out
}

func (n *XMLNode) iterInto(tag string, out *[]*XMLNode) {
	if n.XMLName.Local == tag {
		*out = append(*out, n)
	}
	for _, c := range n.Children {
		c.iterInto(tag, out)
	}
}

// ── Element description helper ─────────────────────────────────────────────────

// elemDesc produces an XPath-style description matching Python's _elem_desc().
// Priority: id → name → pattern → ref → bare tag.
func elemDesc(n *XMLNode) string {
	tag := n.XMLName.Local
	if id := n.Attr("id"); id != "" {
		return fmt.Sprintf("%s[@id='%s']", tag, id)
	}
	if name := n.Attr("name"); name != "" {
		return fmt.Sprintf("%s[@name='%s']", tag, name)
	}
	if pat := n.Attr("pattern"); pat != "" {
		return fmt.Sprintf("%s[@pattern='%s']", tag, pat)
	}
	if ref := n.Attr("ref"); ref != "" {
		return fmt.Sprintf("%s[@ref='%s']", tag, ref)
	}
	return tag
}

// ── Structural helpers ─────────────────────────────────────────────────────────

func checkRequired(errors *[]ValidationError, elemPath string, node *XMLNode, attrs []string) {
	for _, attr := range attrs {
		val := node.Attr(attr)
		if strings.TrimSpace(val) == "" {
			*errors = append(*errors, ValidationError{
				Layer:       LayerStructural,
				ElementPath: elemPath,
				Message: fmt.Sprintf(
					"<%s> is missing required attribute '%s' — add the '%s' attribute to this element",
					elemPath, attr, attr,
				),
			})
		}
	}
}

func checkRef(errors *[]ValidationError, elemPath, attrName, attrVal string, targetSet map[string]bool, targetName string) {
	if attrVal != "" && !targetSet[attrVal] {
		*errors = append(*errors, ValidationError{
			Layer:       LayerReferential,
			ElementPath: elemPath,
			Message: fmt.Sprintf(
				"%s='%s' references an unknown %s — no %s with id '%s' exists in the document; define a <%s id='%s' .../> element or correct the reference",
				attrName, attrVal, targetName, targetName, attrVal, targetName, attrVal,
			),
		})
	}
}

func checkIDUnique(errors *[]ValidationError, idVal string, idSet map[string]bool, elemPath, typeName string) {
	if idSet[idVal] {
		*errors = append(*errors, ValidationError{
			Layer:       LayerStructural,
			ElementPath: elemPath,
			Message: fmt.Sprintf(
				"duplicate %s id '%s' — each %s must have a unique id attribute; rename one of the <%s id='%s' .../> elements",
				typeName, idVal, typeName, typeName, idVal,
			),
		})
	}
	idSet[idVal] = true
}

func checkIntAttr(errors *[]ValidationError, elemPath, attrName, attrVal string) (int, bool) {
	v, err := strconv.Atoi(attrVal)
	if err != nil {
		*errors = append(*errors, ValidationError{
			Layer:       LayerStructural,
			ElementPath: elemPath,
			Message: fmt.Sprintf(
				"%s='%s' is not a valid integer — the '%s' attribute on <%s> must be a whole number (e.g. 1, 2, 3); got %q which cannot be parsed as an integer",
				attrName, attrVal, attrName, elemPath, attrVal,
			),
		})
		return 0, false
	}
	return v, true
}

// ── Layer 1: buildIndex ────────────────────────────────────────────────────────

func newSchemaIndex() SchemaIndex {
	return SchemaIndex{
		PhaseIDs:           make(map[string]bool),
		SubstepIDs:         make(map[string]bool),
		LabelIDs:           make(map[string]bool),
		RoleIDs:            make(map[string]bool),
		CommandIDs:         make(map[string]bool),
		AxisIDs:            make(map[string]bool),
		HandoffIDs:         make(map[string]bool),
		ConstraintIDs:      make(map[string]bool),
		DocumentIDs:        make(map[string]bool),
		TeamIDs:            make(map[string]bool),
		WorkflowIDs:        make(map[string]bool),
		SeverityIDs:        make(map[string]bool),
		EnumValueIDs:       make(map[string]map[string]bool),
		PhaseNumbers:       make(map[string]int),
		PhaseDomains:       make(map[string]string),
		PhaseSubstepOrders: make(map[string][]SubstepOrderEntry),
		LabelValues:        make(map[string]string),
		AxisLetters:        make(map[string]string),
		RolePhaseRefs:      make(map[string]map[string]bool),
		StartupStepOrders:  make(map[string][]int),
	}
}

func buildIndex(root *XMLNode) (SchemaIndex, []ValidationError) {
	idx := newSchemaIndex()
	var errors []ValidationError

	// Enums
	for _, enumEl := range root.Iter("enum") {
		enumName := enumEl.Attr("name")
		valueIDs := make(map[string]bool)
		for _, val := range enumEl.FindAll("value") {
			desc := fmt.Sprintf("enum[@name='%s']/%s", enumName, elemDesc(val))
			checkRequired(&errors, desc, val, []string{"id", "description"})
			vid := val.Attr("id")
			if vid != "" {
				if valueIDs[vid] {
					errors = append(errors, ValidationError{
						Layer:       LayerStructural,
						ElementPath: desc,
						Message:     fmt.Sprintf("duplicate value id '%s' within enum '%s'", vid, enumName),
					})
				}
				valueIDs[vid] = true
			}
		}
		idx.EnumValueIDs[enumName] = valueIDs
	}
	// severity IDs come from SeverityLevel enum
	if sevValues, ok := idx.EnumValueIDs["SeverityLevel"]; ok {
		for k := range sevValues {
			idx.SeverityIDs[k] = true
		}
	}

	// Labels
	for _, label := range root.Iter("label") {
		desc := elemDesc(label)
		checkRequired(&errors, desc, label, []string{"id", "value"})
		lid := label.Attr("id")
		if lid != "" {
			checkIDUnique(&errors, lid, idx.LabelIDs, desc, "label")
		}
		isSpecial := label.Attr("special") == "true"
		if !isSpecial {
			checkRequired(&errors, desc, label, []string{"phase-ref", "substep-ref"})
		}
		val := label.Attr("value")
		if lid != "" && val != "" {
			idx.LabelValues[lid] = val
		}
	}

	// Review axes
	for _, axis := range root.Iter("axis") {
		desc := elemDesc(axis)
		checkRequired(&errors, desc, axis, []string{"id", "letter", "name"})
		aid := axis.Attr("id")
		if aid != "" {
			checkIDUnique(&errors, aid, idx.AxisIDs, desc, "axis")
		}
		letter := axis.Attr("letter")
		if aid != "" && letter != "" {
			idx.AxisLetters[aid] = letter
		}
	}

	// Phases and substeps
	for _, phase := range root.Iter("phase") {
		desc := elemDesc(phase)
		checkRequired(&errors, desc, phase, []string{"id", "number", "domain", "name"})
		pid := phase.Attr("id")
		if pid != "" {
			checkIDUnique(&errors, pid, idx.PhaseIDs, desc, "phase")
			numStr := phase.Attr("number")
			if numStr != "" {
				if num, ok := checkIntAttr(&errors, desc, "number", numStr); ok {
					idx.PhaseNumbers[pid] = num
				}
			}
			domain := phase.Attr("domain")
			if domain != "" {
				idx.PhaseDomains[pid] = domain
			}
		}

		var substepData []SubstepOrderEntry
		for _, substep := range phase.Iter("substep") {
			sdesc := fmt.Sprintf("%s/%s", desc, elemDesc(substep))
			checkRequired(&errors, sdesc, substep, []string{"id", "type", "execution", "order", "label-ref"})
			sid := substep.Attr("id")
			if sid != "" {
				checkIDUnique(&errors, sid, idx.SubstepIDs, sdesc, "substep")
			}
			orderStr := substep.Attr("order")
			execution := substep.Attr("execution")
			order := 0
			if orderStr != "" {
				if o, ok := checkIntAttr(&errors, sdesc, "order", orderStr); ok {
					order = o
				}
			}
			// Startup sequence steps
			if startupSeq := substep.Find("startup-sequence"); startupSeq != nil {
				var stepOrders []int
				for _, stepEl := range startupSeq.FindAll("step") {
					stepDesc := fmt.Sprintf("%s/startup-sequence/step[@order='%s']", sdesc, stepEl.Attr("order"))
					checkRequired(&errors, stepDesc, stepEl, []string{"order"})
					sorderStr := stepEl.Attr("order")
					if sorderStr != "" {
						if so, ok := checkIntAttr(&errors, stepDesc, "order", sorderStr); ok {
							stepOrders = append(stepOrders, so)
						}
					}
				}
				if sid != "" {
					idx.StartupStepOrders[sid] = stepOrders
				}
			}
			if sid != "" {
				substepData = append(substepData, SubstepOrderEntry{
					ID:        sid,
					Order:     order,
					Execution: execution,
				})
			}
		}
		if pid != "" {
			idx.PhaseSubstepOrders[pid] = substepData
		}
	}

	// Roles (only direct children of <roles>)
	rolesEl := root.Find("roles")
	if rolesEl != nil {
		for _, role := range rolesEl.FindAll("role") {
			desc := elemDesc(role)
			checkRequired(&errors, desc, role, []string{"id", "name"})
			rid := role.Attr("id")
			if rid != "" {
				checkIDUnique(&errors, rid, idx.RoleIDs, desc, "role")
				phaseRefs := make(map[string]bool)
				if ownsPhases := role.Find("owns-phases"); ownsPhases != nil {
					for _, pr := range ownsPhases.FindAll("phase-ref") {
						ref := pr.Attr("ref")
						if ref != "" {
							phaseRefs[ref] = true
						}
					}
				}
				idx.RolePhaseRefs[rid] = phaseRefs

				// Standing teams
				for _, team := range role.Iter("team") {
					teamDesc := fmt.Sprintf("%s/standing-teams/%s", desc, elemDesc(team))
					checkRequired(&errors, teamDesc, team, []string{"id"})
					tid := team.Attr("id")
					if tid != "" {
						checkIDUnique(&errors, tid, idx.TeamIDs, teamDesc, "team")
					}
					for _, agentTmpl := range team.FindAll("agent-template") {
						atDesc := fmt.Sprintf("%s/agent-template", teamDesc)
						checkRequired(&errors, atDesc, agentTmpl, []string{"role", "skill-ref", "invocation", "min-count", "max-count"})
						for _, countAttr := range []string{"min-count", "max-count"} {
							countStr := agentTmpl.Attr(countAttr)
							if countStr != "" {
								checkIntAttr(&errors, atDesc, countAttr, countStr)
							}
						}
					}
				}
			}
		}
	}

	// Commands (only within <commands> section)
	commandsSection := root.Find("commands")
	if commandsSection != nil {
		for _, cmd := range commandsSection.FindAll("command") {
			desc := elemDesc(cmd)
			checkRequired(&errors, desc, cmd, []string{"id", "name"})
			cid := cmd.Attr("id")
			if cid != "" {
				checkIDUnique(&errors, cid, idx.CommandIDs, desc, "command")
			}
		}
	}

	// Handoffs
	for _, handoff := range root.Iter("handoff") {
		desc := elemDesc(handoff)
		checkRequired(&errors, desc, handoff, []string{"id", "source-role", "target-role", "at-phase", "content-level"})
		hid := handoff.Attr("id")
		if hid != "" {
			checkIDUnique(&errors, hid, idx.HandoffIDs, desc, "handoff")
		}
	}

	// Constraints
	for _, constraint := range root.Iter("constraint") {
		desc := elemDesc(constraint)
		checkRequired(&errors, desc, constraint, []string{"id", "given", "when", "then", "should-not"})
		cid := constraint.Attr("id")
		if cid != "" {
			checkIDUnique(&errors, cid, idx.ConstraintIDs, desc, "constraint")
		}
	}

	// Documents
	for _, doc := range root.Iter("document") {
		desc := elemDesc(doc)
		checkRequired(&errors, desc, doc, []string{"id", "path"})
		did := doc.Attr("id")
		if did != "" {
			checkIDUnique(&errors, did, idx.DocumentIDs, desc, "document")
		}
	}

	// Workflows
	for _, wf := range root.Iter("workflow") {
		desc := elemDesc(wf)
		checkRequired(&errors, desc, wf, []string{"id", "name"})
		wid := wf.Attr("id")
		if wid != "" {
			checkIDUnique(&errors, wid, idx.WorkflowIDs, desc, "workflow")
		}
	}

	// Title conventions
	for _, tc := range root.Iter("title-convention") {
		desc := elemDesc(tc)
		checkRequired(&errors, desc, tc, []string{"pattern", "label-ref", "created-by"})
	}

	// Skill invocations (structural: directive required)
	for _, si := range root.Iter("skill-invocation") {
		cmdRef := si.Attr("command-ref")
		siDesc := "skill-invocation"
		if cmdRef != "" {
			siDesc = fmt.Sprintf("skill-invocation[@command-ref='%s']", cmdRef)
		}
		checkRequired(&errors, siDesc, si, []string{"directive"})
	}

	return idx, errors
}

// ── Layer 2: checkRefs ─────────────────────────────────────────────────────────

func entityTypeToSet(typeAttr string, index SchemaIndex) map[string]bool {
	switch typeAttr {
	case "phase":
		return index.PhaseIDs
	case "substep":
		return index.SubstepIDs
	case "label":
		return index.LabelIDs
	case "role":
		return index.RoleIDs
	case "command":
		return index.CommandIDs
	case "constraint":
		return index.ConstraintIDs
	case "handoff":
		return index.HandoffIDs
	case "review-axis":
		return index.AxisIDs
	case "severity":
		return index.SeverityIDs
	case "vote":
		return index.EnumValueIDs["VoteType"]
	case "document":
		return index.DocumentIDs
	case "team":
		return index.TeamIDs
	case "workflow":
		return index.WorkflowIDs
	}
	return nil
}

func checkRefs(root *XMLNode, index SchemaIndex) []ValidationError {
	var errors []ValidationError

	// Labels: phase-ref, substep-ref, severity-ref
	for _, label := range root.Iter("label") {
		desc := elemDesc(label)
		checkRef(&errors, desc, "phase-ref", label.Attr("phase-ref"), index.PhaseIDs, "phase")
		checkRef(&errors, desc, "substep-ref", label.Attr("substep-ref"), index.SubstepIDs, "substep")
		checkRef(&errors, desc, "severity-ref", label.Attr("severity-ref"), index.SeverityIDs, "severity")
	}

	// Substeps: label-ref
	for _, substep := range root.Iter("substep") {
		desc := elemDesc(substep)
		checkRef(&errors, desc, "label-ref", substep.Attr("label-ref"), index.LabelIDs, "label")
	}

	// Extra-labels: ref → label_ids
	for _, el := range root.Iter("extra-label") {
		desc := elemDesc(el)
		checkRef(&errors, desc, "ref", el.Attr("ref"), index.LabelIDs, "label")
	}

	// Commands: role-ref (scoped to <commands> section)
	commandsSection := root.Find("commands")
	if commandsSection != nil {
		for _, cmd := range commandsSection.FindAll("command") {
			desc := elemDesc(cmd)
			checkRef(&errors, desc, "role-ref", cmd.Attr("role-ref"), index.RoleIDs, "role")
		}
	}

	// phase-ref child elements: ref → phase_ids
	for _, el := range root.Iter("phase-ref") {
		ref := el.Attr("ref")
		if ref != "" {
			checkRef(&errors, fmt.Sprintf("phase-ref[@ref='%s']", ref), "ref", ref, index.PhaseIDs, "phase")
		}
	}

	// label-ref child elements: ref → label_ids
	for _, el := range root.Iter("label-ref") {
		ref := el.Attr("ref")
		if ref != "" {
			checkRef(&errors, fmt.Sprintf("label-ref[@ref='%s']", ref), "ref", ref, index.LabelIDs, "label")
		}
	}

	// axis-ref child elements: ref → axis_ids
	for _, el := range root.Iter("axis-ref") {
		ref := el.Attr("ref")
		if ref != "" {
			checkRef(&errors, fmt.Sprintf("axis-ref[@ref='%s']", ref), "ref", ref, index.AxisIDs, "axis")
		}
	}

	// Handoffs: source-role, target-role, at-phase
	for _, handoff := range root.Iter("handoff") {
		desc := elemDesc(handoff)
		checkRef(&errors, desc, "source-role", handoff.Attr("source-role"), index.RoleIDs, "role")
		checkRef(&errors, desc, "target-role", handoff.Attr("target-role"), index.RoleIDs, "role")
		checkRef(&errors, desc, "at-phase", handoff.Attr("at-phase"), index.PhaseIDs, "phase")
	}

	// Transitions: to-phase (skip "complete" as terminal sentinel)
	for _, t := range root.Iter("transition") {
		toPhase := t.Attr("to-phase")
		if toPhase != "" && toPhase != "complete" {
			checkRef(
				&errors,
				fmt.Sprintf("transition[@to-phase='%s']", toPhase),
				"to-phase", toPhase, index.PhaseIDs, "phase",
			)
		}
	}

	// same-actor-as: phase-ref
	for _, el := range root.Iter("same-actor-as") {
		checkRef(&errors, "same-actor-as", "phase-ref", el.Attr("phase-ref"), index.PhaseIDs, "phase")
	}

	// Title conventions: label-ref, phase-ref, extra-label-ref
	for _, tc := range root.Iter("title-convention") {
		desc := elemDesc(tc)
		checkRef(&errors, desc, "label-ref", tc.Attr("label-ref"), index.LabelIDs, "label")
		checkRef(&errors, desc, "phase-ref", tc.Attr("phase-ref"), index.PhaseIDs, "phase")
		checkRef(&errors, desc, "extra-label-ref", tc.Attr("extra-label-ref"), index.LabelIDs, "label")
	}

	// severity-tree groups: severity-ref, label-ref
	for _, st := range root.Iter("severity-tree") {
		for _, g := range st.FindAll("group") {
			gDesc := fmt.Sprintf("severity-tree/group[@severity-ref='%s']", g.Attr("severity-ref"))
			checkRef(&errors, gDesc, "severity-ref", g.Attr("severity-ref"), index.SeverityIDs, "severity")
			checkRef(&errors, gDesc, "label-ref", g.Attr("label-ref"), index.LabelIDs, "label")
		}
	}

	// followup-epic: label-ref
	for _, fe := range root.Iter("followup-epic") {
		checkRef(&errors, "followup-epic", "label-ref", fe.Attr("label-ref"), index.LabelIDs, "label")
	}

	// delegate: to-role, phases (comma-separated)
	for _, d := range root.Iter("delegate") {
		dDesc := fmt.Sprintf("delegate[@to-role='%s']", d.Attr("to-role"))
		checkRef(&errors, dDesc, "to-role", d.Attr("to-role"), index.RoleIDs, "role")
		phasesStr := d.Attr("phases")
		if phasesStr != "" {
			for _, p := range strings.Split(phasesStr, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					checkRef(&errors, dDesc, "phases", p, index.PhaseIDs, "phase")
				}
			}
		}
	}

	// Skill invocations: command-ref → command_ids
	for _, si := range root.Iter("skill-invocation") {
		cmdRef := si.Attr("command-ref")
		siDesc := "skill-invocation"
		if cmdRef != "" {
			siDesc = fmt.Sprintf("skill-invocation[@command-ref='%s']", cmdRef)
		}
		checkRef(&errors, siDesc, "command-ref", cmdRef, index.CommandIDs, "command")
	}

	// Agent templates: skill-ref → command_ids
	for _, at := range root.Iter("agent-template") {
		atDesc := elemDesc(at)
		checkRef(&errors, atDesc, "skill-ref", at.Attr("skill-ref"), index.CommandIDs, "command")
	}

	// Document entities: refs (comma-separated or wildcard)
	for _, doc := range root.Iter("document") {
		docDesc := elemDesc(doc)
		for _, entity := range doc.Iter("entity") {
			refs := entity.Attr("refs")
			if refs == "all" || refs == "all-protocol" || refs == "" {
				continue
			}
			typeAttr := entity.Attr("type")
			if typeAttr == "all" {
				continue
			}
			targetSet := entityTypeToSet(typeAttr, index)
			if targetSet == nil {
				continue
			}
			eDesc := fmt.Sprintf("%s/entity[@type='%s']", docDesc, typeAttr)
			for _, ref := range strings.Split(refs, ",") {
				ref = strings.TrimSpace(ref)
				if ref != "" {
					checkRef(&errors, eDesc, "refs", ref, targetSet, typeAttr)
				}
			}
		}
	}

	return errors
}

// ── Layer 3: checkSemantics ────────────────────────────────────────────────────

// expectedDomains mirrors Python _EXPECTED_DOMAINS.
var expectedDomains = map[int]string{
	1: "user", 2: "user", 3: "plan", 4: "plan",
	5: "user", 6: "plan", 7: "plan", 8: "impl",
	9: "impl", 10: "impl", 11: "user", 12: "impl",
}

func checkSemantics(root *XMLNode, index SchemaIndex) []ValidationError {
	var errors []ValidationError

	// 1. Phase numbers sequential (contiguous 1..N)
	var numbers []int
	for _, n := range index.PhaseNumbers {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	if len(numbers) > 0 {
		expected := make([]int, len(numbers))
		for i := range expected {
			expected[i] = i + 1
		}
		if !intsEqual(numbers, expected) {
			missing := missingInts(numbers)
			msg := fmt.Sprintf("Phase numbers not sequential: found %v", numbers)
			if len(missing) > 0 {
				msg += fmt.Sprintf(" (missing %v)", missing)
			}
			errors = append(errors, ValidationError{
				Layer:       LayerSemantic,
				ElementPath: "phases",
				Message:     msg,
			})
		}
	}

	// 2. Phase domain consistency
	// Sort phase IDs so output is deterministic regardless of map iteration order.
	{
		sortedPhaseIDs2 := make([]string, 0, len(index.PhaseNumbers))
		for pid := range index.PhaseNumbers {
			sortedPhaseIDs2 = append(sortedPhaseIDs2, pid)
		}
		sort.Strings(sortedPhaseIDs2)
		for _, pid := range sortedPhaseIDs2 {
			num := index.PhaseNumbers[pid]
			domain := index.PhaseDomains[pid]
			expectedDomain := expectedDomains[num]
			if domain != "" && expectedDomain != "" && domain != expectedDomain {
				errors = append(errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("phase[@id='%s']", pid),
					Message:     fmt.Sprintf("domain='%s' but phase %d should be '%s'", domain, num, expectedDomain),
				})
			}
		}
	}

	// 3. Each phase has >= 1 substep
	for pid := range index.PhaseIDs {
		substeps := index.PhaseSubstepOrders[pid]
		if len(substeps) == 0 {
			errors = append(errors, ValidationError{
				Layer:       LayerSemantic,
				ElementPath: fmt.Sprintf("phase[@id='%s']", pid),
				Message:     "phase has no substeps",
			})
		}
	}

	// 4. Substep order sequential within phase (starting from 1)
	for pid, substeps := range index.PhaseSubstepOrders {
		if len(substeps) == 0 {
			continue
		}
		orderSet := make(map[int]bool)
		for _, s := range substeps {
			orderSet[s.Order] = true
		}
		var orders []int
		for o := range orderSet {
			orders = append(orders, o)
		}
		sort.Ints(orders)
		maxOrder := 0
		if len(orders) > 0 {
			maxOrder = orders[len(orders)-1]
		}
		var expectedOrders []int
		if maxOrder > 0 {
			for i := 1; i <= maxOrder; i++ {
				expectedOrders = append(expectedOrders, i)
			}
		}
		if !intsEqual(orders, expectedOrders) {
			errors = append(errors, ValidationError{
				Layer:       LayerSemantic,
				ElementPath: fmt.Sprintf("phase[@id='%s']", pid),
				Message:     fmt.Sprintf("substep orders not sequential: found %v, expected %v", orders, expectedOrders),
			})
		}
	}

	// 5. Parallel substeps must have parallel-group or instances
	for _, phase := range root.Iter("phase") {
		pid := phase.Attr("id")
		for _, substep := range phase.Iter("substep") {
			if substep.Attr("execution") == "parallel" &&
				substep.Attr("parallel-group") == "" &&
				substep.Find("instances") == nil {
				errors = append(errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("phase[@id='%s']/%s", pid, elemDesc(substep)),
					Message:     "execution='parallel' but missing 'parallel-group' attribute",
				})
			}
		}
	}

	// 6. Label value uniqueness
	// Iterate label IDs in sorted order so that "first seen" attribution and
	// the resulting error slice are deterministic regardless of map iteration
	// order.
	{
		sortedLabelIDs := make([]string, 0, len(index.LabelValues))
		for lid := range index.LabelValues {
			sortedLabelIDs = append(sortedLabelIDs, lid)
		}
		sort.Strings(sortedLabelIDs)
		seenValues := make(map[string]string)
		var rule6Errors []ValidationError
		for _, lid := range sortedLabelIDs {
			val := index.LabelValues[lid]
			if first, exists := seenValues[val]; exists {
				rule6Errors = append(rule6Errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("label[@id='%s']", lid),
					Message:     fmt.Sprintf("duplicate value '%s' (first seen on label[@id='%s'])", val, first),
				})
			} else {
				seenValues[val] = lid
			}
		}
		sort.Slice(rule6Errors, func(i, j int) bool {
			return rule6Errors[i].ElementPath < rule6Errors[j].ElementPath
		})
		errors = append(errors, rule6Errors...)
	}

	// Rules 7 and 8 are absent from the Python validate_schema.py and are not ported here.

	// 9. Each role owns >= 1 phase
	// Sort role IDs so output is deterministic regardless of map iteration order.
	{
		sortedRoleIDs9 := make([]string, 0, len(index.RolePhaseRefs))
		for rid := range index.RolePhaseRefs {
			sortedRoleIDs9 = append(sortedRoleIDs9, rid)
		}
		sort.Strings(sortedRoleIDs9)
		var rule9Errors []ValidationError
		for _, rid := range sortedRoleIDs9 {
			phases := index.RolePhaseRefs[rid]
			if len(phases) == 0 {
				rule9Errors = append(rule9Errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("role[@id='%s']", rid),
					Message:     "role owns no phases",
				})
			}
		}
		sort.Slice(rule9Errors, func(i, j int) bool {
			return rule9Errors[i].ElementPath < rule9Errors[j].ElementPath
		})
		errors = append(errors, rule9Errors...)
	}

	// 10. Each command with <phases> must have a <file> child
	commandsSection := root.Find("commands")
	if commandsSection != nil {
		for _, cmd := range commandsSection.FindAll("command") {
			if cmd.Find("phases") != nil && cmd.Find("file") == nil {
				errors = append(errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: elemDesc(cmd),
					Message:     "command has <phases> but no <file> child",
				})
			}
		}
	}

	// 11. Review axis letters unique
	// Sort axis IDs so output is deterministic regardless of map iteration order.
	{
		sortedAxisIDs11 := make([]string, 0, len(index.AxisLetters))
		for aid := range index.AxisLetters {
			sortedAxisIDs11 = append(sortedAxisIDs11, aid)
		}
		sort.Strings(sortedAxisIDs11)
		seenLetters := make(map[string]string)
		var rule11Errors []ValidationError
		for _, aid := range sortedAxisIDs11 {
			letter := index.AxisLetters[aid]
			if firstAid, exists := seenLetters[letter]; exists {
				rule11Errors = append(rule11Errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("axis[@id='%s']", aid),
					Message:     fmt.Sprintf("duplicate letter '%s' (first seen on axis[@id='%s'])", letter, firstAid),
				})
			} else {
				seenLetters[letter] = aid
			}
		}
		sort.Slice(rule11Errors, func(i, j int) bool {
			return rule11Errors[i].ElementPath < rule11Errors[j].ElementPath
		})
		errors = append(errors, rule11Errors...)
	}

	// 12. Startup sequence step orders sequential
	// Sort substep IDs so output is deterministic regardless of map iteration order.
	{
		sortedSubstepIDs12 := make([]string, 0, len(index.StartupStepOrders))
		for sid := range index.StartupStepOrders {
			sortedSubstepIDs12 = append(sortedSubstepIDs12, sid)
		}
		sort.Strings(sortedSubstepIDs12)
		var rule12Errors []ValidationError
		for _, sid := range sortedSubstepIDs12 {
			orders := index.StartupStepOrders[sid]
			if len(orders) == 0 {
				continue
			}
			sorted := make([]int, len(orders))
			copy(sorted, orders)
			sort.Ints(sorted)
			expected := make([]int, len(orders))
			for i := range expected {
				expected[i] = i + 1
			}
			if !intsEqual(sorted, expected) {
				rule12Errors = append(rule12Errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("substep[@id='%s']/startup-sequence", sid),
					Message:     fmt.Sprintf("step orders not sequential: found %v, expected %v", sorted, expected),
				})
			}
		}
		sort.Slice(rule12Errors, func(i, j int) bool {
			return rule12Errors[i].ElementPath < rule12Errors[j].ElementPath
		})
		errors = append(errors, rule12Errors...)
	}

	// 13. Agent template min-count <= max-count
	for _, at := range root.Iter("agent-template") {
		minStr := at.Attr("min-count")
		maxStr := at.Attr("max-count")
		if minStr != "" && maxStr != "" {
			minVal, err1 := strconv.Atoi(minStr)
			maxVal, err2 := strconv.Atoi(maxStr)
			if err1 == nil && err2 == nil && minVal > maxVal {
				atDesc := elemDesc(at)
				if atDesc == "" {
					atDesc = "agent-template"
				}
				errors = append(errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: atDesc,
					Message:     fmt.Sprintf("min-count (%s) > max-count (%s)", minStr, maxStr),
				})
			}
		}
	}

	// 14. Domain enum values match phase domains
	// Sort both the phase IDs being iterated and the resulting error slice so
	// that output is deterministic regardless of map iteration order.
	domainEnumValues := index.EnumValueIDs["DomainType"]
	if len(domainEnumValues) > 0 {
		var sortedDomainEnumKeys []string
		for k := range domainEnumValues {
			sortedDomainEnumKeys = append(sortedDomainEnumKeys, k)
		}
		sort.Strings(sortedDomainEnumKeys)

		sortedPhaseIDs := make([]string, 0, len(index.PhaseDomains))
		for pid := range index.PhaseDomains {
			sortedPhaseIDs = append(sortedPhaseIDs, pid)
		}
		sort.Strings(sortedPhaseIDs)

		var rule14Errors []ValidationError
		for _, pid := range sortedPhaseIDs {
			domain := index.PhaseDomains[pid]
			if !domainEnumValues[domain] {
				rule14Errors = append(rule14Errors, ValidationError{
					Layer:       LayerSemantic,
					ElementPath: fmt.Sprintf("phase[@id='%s']", pid),
					Message:     fmt.Sprintf("domain='%s' not in DomainType enum %v", domain, sortedDomainEnumKeys),
				})
			}
		}
		sort.Slice(rule14Errors, func(i, j int) bool {
			return rule14Errors[i].ElementPath < rule14Errors[j].ElementPath
		})
		errors = append(errors, rule14Errors...)
	}

	return errors
}

// ── Integer slice helpers ──────────────────────────────────────────────────────

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// missingInts returns integers in range [1, max(numbers)] not present in numbers.
// numbers must be sorted.
func missingInts(numbers []int) []int {
	if len(numbers) == 0 {
		return nil
	}
	maxN := numbers[len(numbers)-1]
	present := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		present[n] = true
	}
	var missing []int
	for i := 1; i <= maxN; i++ {
		if !present[i] {
			missing = append(missing, i)
		}
	}
	return missing
}

// ── IO helpers ────────────────────────────────────────────────────────────────

// ParseXMLNode decodes a single XML document from r into out using
// the standard xml.Decoder. This is the same decode step used by
// ValidateSchema and is exported primarily for testing purposes.
func ParseXMLNode(r io.Reader, out *XMLNode) error {
	return xml.NewDecoder(r).Decode(out)
}

// ── Orchestration ──────────────────────────────────────────────────────────────

// ValidateTree validates a parsed XMLNode tree against the 3-layer Aura
// Protocol schema rules and returns all violations found.
//
// Returns nil when the tree is valid. A nil root is treated as an empty
// document with no violations.
func ValidateTree(root *XMLNode) []ValidationError {
	if root == nil {
		return nil
	}
	index, structural := buildIndex(root)
	referential := checkRefs(root, index)
	semantic := checkSemantics(root, index)

	var all []ValidationError
	all = append(all, structural...)
	all = append(all, referential...)
	all = append(all, semantic...)
	if len(all) == 0 {
		return nil
	}
	return all
}

// ValidateSchema reads XML from r and validates it against the 3-layer
// Aura Protocol schema rules.
//
// Error contract:
//   - io.Reader read failure → (nil, error)
//   - XML parse failure → ([]ValidationError{{Layer: LayerStructural, ...}}, nil)
//   - Violations found → ([]ValidationError{...}, nil) with len > 0
//   - Valid schema → (nil, nil)
func ValidateSchema(r io.Reader) ([]ValidationError, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("ValidateSchema: failed to read input: %w", err)
	}

	var root XMLNode
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&root); err != nil {
		return []ValidationError{
			{
				Layer:       LayerStructural,
				ElementPath: "document",
				Message:     fmt.Sprintf("XML parse error: %s", err.Error()),
			},
		}, nil
	}

	errs := ValidateTree(&root)
	if len(errs) == 0 {
		return nil, nil
	}
	return errs, nil
}

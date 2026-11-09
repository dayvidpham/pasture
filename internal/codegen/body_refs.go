package codegen

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Body-content markers are the only authoring surface for a harness tool, an
// orchestration operation, and the tracker entry point inside any rendered
// prose.
//
// An author writes a marker, never a harness-specific spelling. The generator
// resolves each marker against the generation target's pinned lowering table,
// so one authored text still produces the correct native name for every
// harness. This is the prose-level form of tenet 4 (name it once): the native
// spelling has exactly one home, internal/runtime/profiles.go, and authored
// text cannot copy it.
//
// Marker grammar (the payload is a closed-set value, never free prose):
//
//	[[tool:<ir.HarnessTool>]]   a native file or shell capability
//	[[op:<ir.OperationKind>]]   an orchestration intent
//	[[rec]]                     the tracker entry point
//
// An unknown marker payload fails generation. Every authored string that
// reaches generated output must carry a typed marker instead of a native call
// prefix or the tracker command prefix; renderedSources enumerates that corpus
// and the literal guards iterate it.
const (
	bodyRecMarker = "[[rec]]"

	bodyMarkerOpen  = "[["
	bodyMarkerClose = "]]"
)

// trackerEntryPoint is the one home of the tracker command prefix in generated
// prose. Tracker backends are selected at run time (amendment A-3): the
// closed tracker operation vocabulary and the runtime adapter that replaces
// this prefix are owned by the tracker-contract wave. Until then the prefix is
// rendered exactly as before, so this change renames the literal without
// changing generated meaning.
const trackerEntryPoint = "pasture task"

// ResolveBodyMarkers replaces every body marker in content with the string the
// generation target's pinned lowering table assigns to it.
//
// A native harness tool resolves to the target's native name. A harness that
// declares no per-agent tool list (Codex) resolves to the neutral capability
// spelling, which is not a harness name. A native operation resolves to the
// target's native call name; a mediated, semantic, or unsupported operation
// resolves to the neutral kind spelling, which names the intent rather than a
// foreign tool. The tracker entry point resolves through the single
// trackerEntryPoint constant.
//
// Resolution fails closed. An unknown marker, an unregistered tool, or an
// unregistered operation kind returns an actionable error instead of leaving
// an unresolved marker in generated output.
func ResolveBodyMarkers(content string, target HarnessName) (string, error) {
	if !strings.Contains(content, bodyMarkerOpen) {
		return content, nil
	}
	resolvedTarget, err := roleTargetFor(target)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: cannot resolve target %q — "+
				"why: body markers resolve through the target's pinned lowering table; "+
				"where: internal/codegen/body_refs.go ResolveBodyMarkers; "+
				"fix: pass a registered generation target: %w",
			target, err,
		)
	}
	return walkBodyMarkers(content, func(payload string) (string, error) {
		return resolveBodyMarker(resolvedTarget, payload)
	})
}

// neutralMarkerPattern matches one well-formed marker. schema.xml wraps code in
// CDATA, whose closing "[" can sit directly before a marker ("<![CDATA[[[rec]]"),
// so the neutral resolver matches the marker token instead of scanning for the
// first "[[".
var neutralMarkerPattern = regexp.MustCompile(`\[\[(?:rec|tool:[A-Za-z0-9_]+|op:[A-Za-z0-9_]+)\]\]`)

// ResolveBodyMarkersNeutral replaces every marker with the target-neutral
// spelling. It is the rendering rule for a generated artifact that names no
// single harness (schema.xml): the operation is written, not any harness's
// tool (tenet 3).
func ResolveBodyMarkersNeutral(content string) (string, error) {
	if !strings.Contains(content, bodyMarkerOpen) {
		return content, nil
	}
	var firstErr error
	out := neutralMarkerPattern.ReplaceAllStringFunc(content, func(marker string) string {
		payload := strings.TrimSuffix(strings.TrimPrefix(marker, bodyMarkerOpen), bodyMarkerClose)
		replacement, err := resolveNeutralMarker(payload)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return marker
		}
		return replacement
	})
	if firstErr != nil {
		return "", firstErr
	}
	// Fail closed on a marker-shaped token the pattern could not resolve, so
	// an unclosed or unknown marker never reaches a generated artifact.
	if idx := strings.Index(out, bodyMarkerOpen); idx >= 0 {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkersNeutral: unclosed or unknown marker at byte %d — "+
				"why: every [[...]] marker must be well formed and registered; "+
				"where: an authored rendering source in internal/codegen/specs_data*.go; "+
				"fix: close the marker and use a registered payload",
			idx,
		)
	}
	return out, nil
}

// walkBodyMarkers is the one marker scanner shared by every resolver.
func walkBodyMarkers(content string, resolve func(payload string) (string, error)) (string, error) {
	var out strings.Builder
	out.Grow(len(content))
	rest := content
	for {
		start := strings.Index(rest, bodyMarkerOpen)
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:start])
		afterOpen := rest[start+len(bodyMarkerOpen):]
		stop := strings.Index(afterOpen, bodyMarkerClose)
		if stop < 0 {
			return "", fmt.Errorf(
				"codegen.ResolveBodyMarkers: unclosed body marker at byte %d — "+
					"why: every [[...]] marker must close with ]]; "+
					"where: an authored rendering source in internal/codegen/specs_data*.go; "+
					"fix: close the marker (an unclosed [[ is not valid prose)",
				len(content)-len(rest)+start,
			)
		}
		payload := afterOpen[:stop]
		replacement, err := resolve(payload)
		if err != nil {
			contextStart := start - 40
			if contextStart < 0 {
				contextStart = 0
			}
			contextEnd := start + len(bodyMarkerOpen) + stop + len(bodyMarkerClose) + 40
			if contextEnd > len(rest) {
				contextEnd = len(rest)
			}
			return "", fmt.Errorf("%w\n  source context: %q", err, rest[contextStart:contextEnd])
		}
		out.WriteString(replacement)
		rest = afterOpen[stop+len(bodyMarkerClose):]
	}
}

func resolveBodyMarker(target roleTarget, payload string) (string, error) {
	switch {
	case payload == "rec":
		return trackerEntryPoint, nil
	case strings.HasPrefix(payload, "tool:"):
		return resolveToolMarker(target, strings.TrimPrefix(payload, "tool:"))
	case strings.HasPrefix(payload, "op:"):
		return resolveOperationMarker(target, strings.TrimPrefix(payload, "op:"))
	default:
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: unknown body marker %q — "+
				"why: the body-marker grammar is closed to tool, op and the tracker entry point; "+
				"where: an authored rendering source in internal/codegen/specs_data*.go; "+
				"fix: use [[tool:<ir.HarnessTool>]], [[op:<ir.OperationKind>]] or [[rec]]",
			bodyMarkerOpen+payload+bodyMarkerClose,
		)
	}
}

func resolveNeutralMarker(payload string) (string, error) {
	switch {
	case payload == "rec":
		return trackerEntryPoint, nil
	case strings.HasPrefix(payload, "tool:"):
		name := strings.TrimPrefix(payload, "tool:")
		tool := ir.HarnessTool(name)
		if !tool.IsValid() {
			return "", fmt.Errorf(
				"codegen.ResolveBodyMarkersNeutral: unregistered harness tool %q — "+
					"why: the harness-tool set is closed; "+
					"where: a rendering-source tool marker; "+
					"fix: use one of ir.AllHarnessTools",
				name,
			)
		}
		return name, nil
	case strings.HasPrefix(payload, "op:"):
		name := strings.TrimPrefix(payload, "op:")
		if _, registered := runtime.CoreOperationDescriptorFor(ir.OperationKind(name)); !registered {
			return "", fmt.Errorf(
				"codegen.ResolveBodyMarkersNeutral: unregistered operation kind %q — "+
					"why: a rendering source may only name a closed orchestration kind; "+
					"where: a rendering-source op marker; "+
					"fix: use one of ir.AllOperationKinds",
				name,
			)
		}
		return name, nil
	default:
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkersNeutral: unknown body marker %q — "+
				"why: the body-marker grammar is closed to tool, op and the tracker entry point; "+
				"where: an authored rendering source in internal/codegen/specs_data*.go; "+
				"fix: use [[tool:<ir.HarnessTool>]], [[op:<ir.OperationKind>]] or [[rec]]",
			bodyMarkerOpen+payload+bodyMarkerClose,
		)
	}
}

func resolveToolMarker(target roleTarget, name string) (string, error) {
	tool := ir.HarnessTool(name)
	if !tool.IsValid() {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: unregistered harness tool %q — "+
				"why: the harness-tool set is closed; "+
				"where: a rendering-source tool marker; "+
				"fix: use one of ir.AllHarnessTools",
			name,
		)
	}
	native, ok, err := nativeToolName(target.harness, tool)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: cannot lower harness tool %q for %q: %w",
			tool, target.name, err,
		)
	}
	if ok {
		return native, nil
	}
	// The target declares no per-agent tool list. Render the neutral
	// capability name: it is not a harness spelling, so it cannot be mistaken
	// for a tool the host does not expose.
	return string(tool), nil
}

func resolveOperationMarker(target roleTarget, name string) (string, error) {
	kind := ir.OperationKind(name)
	native, ok, err := nativeOperationName(target.contract, kind)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: unregistered operation kind %q — "+
				"why: a rendering source may only name a closed orchestration kind; "+
				"where: a rendering-source op marker; "+
				"fix: use one of ir.AllOperationKinds: %w",
			name, err,
		)
	}
	if ok {
		return native, nil
	}
	// Unsupported on this contract: the operation has no native call. Render
	// the neutral kind so the prose names the intent, not a tool.
	return string(kind), nil
}

// renderingSource is one authored string that the generators render into a
// generated skill, sub-skill, or agent file. Corpus and Owner name where the
// string lives, so a guard failure points at the exact source field.
type renderingSource struct {
	Corpus string
	Owner  string
	Text   string
}

// renderedSources returns every authored string that reaches generated skill,
// sub-skill, or agent output. It is the ONE enumeration the literal guards
// iterate: a new rendering corpus is added here once, and every guard sees it
// (tenets 4, 6 and 8).
//
// The enumeration is deliberately closed over the template inputs in
// skills.go, agents.go and opencode_agent.go. Figures load their content from
// skills/protocol/figures at generation time, so they are read here too.
func renderedSources() ([]renderingSource, error) {
	var sources []renderingSource
	add := func(corpus, owner, text string) {
		if text == "" {
			return
		}
		sources = append(sources, renderingSource{Corpus: corpus, Owner: owner, Text: text})
	}

	for skill, body := range SkillBodySpecs {
		corpus := fmt.Sprintf("SkillBodySpecs[%q]", skill)
		for _, entry := range walkBodyStrings(body) {
			add(corpus, entry.owner, entry.text)
		}
	}

	for id, fragment := range SharedFragmentSpecs {
		corpus := fmt.Sprintf("SharedFragmentSpecs[%q]", string(id))
		if fragment.Prose != nil {
			for _, entry := range walkProseSection("Prose", *fragment.Prose) {
				add(corpus, entry.owner, entry.text)
			}
		}
		if fragment.Behavior != nil {
			for _, entry := range walkBehavior("Behavior", *fragment.Behavior) {
				add(corpus, entry.owner, entry.text)
			}
		}
	}

	for roleID, spec := range RoleSpecs {
		corpus := fmt.Sprintf("RoleSpecs[%q]", string(roleID))
		add(corpus, "Name", spec.Name)
		add(corpus, "Description", spec.Description)
		add(corpus, "Introduction", spec.Introduction)
		add(corpus, "OwnershipNarrative", spec.OwnershipNarrative)
		for i, behavior := range spec.Behaviors {
			owner := fmt.Sprintf("Behaviors[%d](%s)", i, behavior.Id)
			for _, entry := range walkBehavior(owner, behavior) {
				add(corpus, entry.owner, entry.text)
			}
		}
	}

	for id, constraint := range ConstraintSpecs {
		corpus := fmt.Sprintf("ConstraintSpecs[%q]", id)
		add(corpus, "Given", constraint.Given)
		add(corpus, "When", constraint.When)
		add(corpus, "Then", constraint.Then)
		add(corpus, "ShouldNot", constraint.ShouldNot)
		add(corpus, "Command", constraint.Command)
		for i, example := range constraint.Examples {
			add(corpus, fmt.Sprintf("Examples[%d].Code", i), example.Code)
		}
	}

	for id, command := range CommandSpecs {
		corpus := fmt.Sprintf("CommandSpecs[%q]", id)
		add(corpus, "Title", command.Title)
		add(corpus, "Description", command.Description)
	}

	for id, handoff := range HandoffSpecs {
		corpus := fmt.Sprintf("HandoffSpecs[%q]", id)
		add(corpus, "ContentLevel", handoff.ContentLevel)
		for i, field := range handoff.RequiredFields {
			add(corpus, fmt.Sprintf("RequiredFields[%d]", i), field)
		}
	}

	for id, checklist := range ChecklistSpecs {
		corpus := fmt.Sprintf("ChecklistSpecs[%q]", id)
		add(corpus, "Gate", checklist.Gate)
		for i, item := range checklist.Items {
			add(corpus, fmt.Sprintf("Items[%d].Text", i), item.Text)
		}
	}

	for id, command := range CoordinationCommands {
		corpus := fmt.Sprintf("CoordinationCommands[%q]", id)
		add(corpus, "Action", command.Action)
		add(corpus, "Template", command.Template)
	}

	for id, workflow := range WorkflowSpecs {
		corpus := fmt.Sprintf("WorkflowSpecs[%q]", id)
		add(corpus, "Name", workflow.Name)
		add(corpus, "Description", workflow.Description)
		for i, stage := range workflow.Stages {
			stageOwner := fmt.Sprintf("Stages[%d](%s)", i, stage.Id)
			add(corpus, stageOwner+".Name", stage.Name)
			add(corpus, stageOwner+".OperationalDetail", stage.OperationalDetail)
			for j, action := range stage.Actions {
				actionOwner := fmt.Sprintf("%s.Actions[%d]", stageOwner, j)
				add(corpus, actionOwner+".Instruction", action.Instruction)
				add(corpus, actionOwner+".Command", action.Command)
			}
			for j, condition := range stage.ExitConditions {
				add(corpus, fmt.Sprintf("%s.ExitConditions[%d].Condition", stageOwner, j), condition.Condition)
			}
		}
	}

	for id, axis := range ReviewAxisSpecs {
		corpus := fmt.Sprintf("ReviewAxisSpecs[%q]", id)
		add(corpus, "Name", axis.Name)
		add(corpus, "Short", axis.Short)
		for i, question := range axis.KeyQuestions {
			add(corpus, fmt.Sprintf("KeyQuestions[%d]", i), question)
		}
	}

	for roleID, steps := range ProcedureSteps {
		corpus := fmt.Sprintf("ProcedureSteps[%q]", string(roleID))
		for i, step := range steps {
			owner := fmt.Sprintf("Steps[%d](%s)", i, step.Id)
			add(corpus, owner+".Instruction", step.Instruction)
			add(corpus, owner+".Command", step.Command)
			add(corpus, owner+".Context", step.Context)
		}
	}

	for phaseID, spec := range PhaseSpecs {
		corpus := fmt.Sprintf("PhaseSpecs[%q]", string(phaseID))
		add(corpus, "Name", spec.Name)
		for i, transition := range spec.Transitions {
			add(corpus, fmt.Sprintf("Transitions[%d].Condition", i), transition.Condition)
			add(corpus, fmt.Sprintf("Transitions[%d].Action", i), transition.Action)
		}
	}

	for id, spec := range LabelSpecs {
		corpus := fmt.Sprintf("LabelSpecs[%q]", id)
		add(corpus, "Value", spec.Value)
		add(corpus, "Description", spec.Description)
	}

	for i, convention := range TitleConventions {
		corpus := fmt.Sprintf("TitleConventions[%d]", i)
		add(corpus, "Pattern", convention.Pattern)
		add(corpus, "CreatedBy", convention.CreatedBy)
		add(corpus, "Note", convention.Note)
	}

	for phaseID, substeps := range SubstepDataMap {
		corpus := fmt.Sprintf("SubstepDataMap[%q]", phaseID)
		for i, substep := range substeps {
			owner := fmt.Sprintf("Substeps[%d](%s)", i, substep.Id)
			add(corpus, owner+".Type", substep.Type)
			add(corpus, owner+".Execution", substep.Execution)
			add(corpus, owner+".ParallelGroup", substep.ParallelGroup)
			add(corpus, owner+".Description", substep.Description)
			add(corpus, owner+".ExtraLabel", substep.ExtraLabel)
			if substep.Instances != nil {
				add(corpus, owner+".Instances.Count", substep.Instances.Count)
				add(corpus, owner+".Instances.Per", substep.Instances.Per)
			}
		}
	}

	// Figures are rendered into generated prose; their body lives in a YAML
	// file on disk rather than in the static registry.
	root, err := scan.ModuleRoot()
	if err != nil {
		return nil, fmt.Errorf(
			"codegen.renderedSources: cannot locate the module root for figure content — "+
				"why: figures render into generated prose and their content is loaded from "+
				"skills/protocol/figures; where: internal/codegen/body_refs.go renderedSources; "+
				"fix: run from inside the pasture module: %w",
			err,
		)
	}
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	for id, figure := range FigureSpecs {
		corpus := fmt.Sprintf("FigureSpecs[%q]", id)
		add(corpus, "Title", figure.Title)
		content, err := loadFigureContent(id, figuresDir)
		if err != nil {
			// Generation treats an unloadable figure as empty content, so the
			// guard matches generation instead of inventing a stricter rule.
			continue
		}
		add(corpus, "Content", content)
	}

	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Corpus != sources[j].Corpus {
			return sources[i].Corpus < sources[j].Corpus
		}
		return sources[i].Owner < sources[j].Owner
	})
	return sources, nil
}

// renderedString is one named authored string produced by a body walk.
type renderedString struct {
	owner string
	text  string
}

// walkBodyStrings is the one walk of the SkillBody shape. It is used by the
// rendering-source enumeration so a field added to SkillBody is seen by every
// guard in the same edit.
func walkBodyStrings(body SkillBody) []renderedString {
	out := []renderedString{{owner: "Preamble", text: body.Preamble}}
	out = append(out, walkProseSections("Sections", body.Sections)...)
	for i, behavior := range body.Behaviors {
		out = append(out, walkBehavior(fmt.Sprintf("Behaviors[%d](%s)", i, behavior.Id), behavior)...)
	}
	for i, recipe := range body.Recipes {
		owner := fmt.Sprintf("Recipes[%d](%s)", i, recipe.Id)
		out = append(out,
			renderedString{owner: owner + ".Title", text: recipe.Title},
			renderedString{owner: owner + ".Description", text: recipe.Description},
			renderedString{owner: owner + ".Code", text: recipe.Code},
		)
	}
	return out
}

func walkProseSections(prefix string, sections []ProseSection) []renderedString {
	var out []renderedString
	for i, section := range sections {
		owner := fmt.Sprintf("%s[%d](%s)", prefix, i, section.Id)
		out = append(out,
			renderedString{owner: owner + ".Title", text: section.Title},
			renderedString{owner: owner + ".Content", text: section.Content},
		)
		out = append(out, walkProseSections(owner+".Subsections", section.Subsections)...)
	}
	return out
}

func walkProseSection(prefix string, section ProseSection) []renderedString {
	return walkProseSections(prefix, []ProseSection{section})
}

func walkBehavior(prefix string, behavior BehaviorSpec) []renderedString {
	return []renderedString{
		{owner: prefix + ".Given", text: behavior.Given},
		{owner: prefix + ".When", text: behavior.When},
		{owner: prefix + ".Then", text: behavior.Then},
		{owner: prefix + ".ShouldNot", text: behavior.ShouldNot},
	}
}

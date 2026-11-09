package codegen

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Body-content markers are the only authoring surface for a harness tool, an
// orchestration operation, and the tracker entry point inside SkillBody prose.
//
// A body author writes a marker, never a harness-specific spelling. The
// generator resolves each marker against the generation target's pinned
// lowering table, so one authored body still produces the correct native name
// for every harness. This is the body-level form of tenet 4 (name it once):
// the native spelling has exactly one home, internal/runtime/profiles.go, and
// a body cannot copy it.
//
// Marker grammar (the payload is a closed-set value, never free prose):
//
//	[[tool:<ir.HarnessTool>]]   a native file or shell capability
//	[[op:<ir.OperationKind>]]   an orchestration intent
//	[[rec]]                     the tracker entry point
//
// An unknown marker payload fails generation. A body that still carries a
// literal harness call prefix or the tracker command prefix fails the build in
// TestBodySpecsCarryNoRawHarnessLiterals.
const (
	bodyToolMarkerPrefix = "[[tool:"
	bodyOpMarkerPrefix   = "[[op:"
	bodyRecMarker        = "[[rec]]"

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

// ToolRef returns the body marker for one native harness tool. It is exported
// so a body can name the capability with the typed closed set instead of a
// harness spelling; the payload is validated again at resolution time.
func ToolRef(tool ir.HarnessTool) string {
	if !tool.IsValid() {
		panic(fmt.Sprintf(
			"codegen.ToolRef(%q): unregistered harness tool — "+
				"why: the harness-tool set is closed; "+
				"where: a SkillBody declares a tool marker; "+
				"fix: use one of ir.AllHarnessTools",
			tool,
		))
	}
	return bodyToolMarkerPrefix + string(tool) + bodyMarkerClose
}

// OpRef returns the body marker for one orchestration operation kind. The
// kind is validated at resolution time against the pinned contract, so an
// unsupported value cannot slip through a hand-written body.
func OpRef(kind ir.OperationKind) string {
	return bodyOpMarkerPrefix + string(kind) + bodyMarkerClose
}

// TrackerRef returns the body marker for the tracker entry point. A tracker
// command is authored as TrackerRef() followed by the operation and its
// arguments; the entry point itself is never spelled in a body.
func TrackerRef() string { return bodyRecMarker }

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
					"where: a SkillBody in internal/codegen/specs_data_body_*.go; "+
					"fix: close the marker or escape it as plain prose",
				len(content)-len(rest)+start,
			)
		}
		payload := afterOpen[:stop]
		replacement, err := resolveBodyMarker(resolvedTarget, payload)
		if err != nil {
			return "", err
		}
		out.WriteString(replacement)
		rest = afterOpen[stop+len(bodyMarkerClose):]
	}
}

// resolveBodyTargetMarkers applies ResolveBodyMarkers to every authored body
// string of one skill: the preamble, every prose section (recursively through
// subsections), every behavior, and every recipe. Fragment payloads have
// already been merged into the sections by the caller, so a marker that lives
// in a shared fragment is resolved once, for this target.
func resolveBodyTargetMarkers(
	preamble string,
	sections []ProseSection,
	behaviors []BehaviorSpec,
	recipes []RecipeBlock,
	target HarnessName,
) (string, []ProseSection, []BehaviorSpec, []RecipeBlock, error) {
	resolvedPreamble, err := ResolveBodyMarkers(preamble, target)
	if err != nil {
		return "", nil, nil, nil, err
	}
	resolvedSections, err := resolveSectionMarkers(sections, target)
	if err != nil {
		return "", nil, nil, nil, err
	}
	resolvedBehaviors := make([]BehaviorSpec, len(behaviors))
	for i, behavior := range behaviors {
		resolvedBehaviors[i] = behavior
		if resolvedBehaviors[i].Given, err = ResolveBodyMarkers(behavior.Given, target); err != nil {
			return "", nil, nil, nil, err
		}
		if resolvedBehaviors[i].When, err = ResolveBodyMarkers(behavior.When, target); err != nil {
			return "", nil, nil, nil, err
		}
		if resolvedBehaviors[i].Then, err = ResolveBodyMarkers(behavior.Then, target); err != nil {
			return "", nil, nil, nil, err
		}
		if resolvedBehaviors[i].ShouldNot, err = ResolveBodyMarkers(behavior.ShouldNot, target); err != nil {
			return "", nil, nil, nil, err
		}
	}
	resolvedRecipes := make([]RecipeBlock, len(recipes))
	for i, recipe := range recipes {
		resolvedRecipes[i] = recipe
		if resolvedRecipes[i].Title, err = ResolveBodyMarkers(recipe.Title, target); err != nil {
			return "", nil, nil, nil, err
		}
		if resolvedRecipes[i].Description, err = ResolveBodyMarkers(recipe.Description, target); err != nil {
			return "", nil, nil, nil, err
		}
		if resolvedRecipes[i].Code, err = ResolveBodyMarkers(recipe.Code, target); err != nil {
			return "", nil, nil, nil, err
		}
	}
	return resolvedPreamble, resolvedSections, resolvedBehaviors, resolvedRecipes, nil
}

func resolveSectionMarkers(sections []ProseSection, target HarnessName) ([]ProseSection, error) {
	resolved := make([]ProseSection, len(sections))
	for i, section := range sections {
		resolved[i] = section
		var err error
		if resolved[i].Title, err = ResolveBodyMarkers(section.Title, target); err != nil {
			return nil, err
		}
		if resolved[i].Content, err = ResolveBodyMarkers(section.Content, target); err != nil {
			return nil, err
		}
		if resolved[i].Subsections, err = resolveSectionMarkers(section.Subsections, target); err != nil {
			return nil, err
		}
	}
	return resolved, nil
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
				"where: a SkillBody in internal/codegen/specs_data_body_*.go; "+
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
				"where: a SkillBody tool marker; "+
				"fix: use one of ir.AllHarnessTools",
			name,
		)
	}
	lowering, err := runtime.LookupHarnessTool(target.harness, tool)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: cannot lower harness tool %q for %q: %w",
			tool, target.name, err,
		)
	}
	if lowering.Available() {
		return lowering.NativeName(), nil
	}
	// The target declares no per-agent tool list. Render the neutral
	// capability name: it is not a harness spelling, so it cannot be mistaken
	// for a tool the host does not expose.
	return string(tool), nil
}

func resolveOperationMarker(target roleTarget, name string) (string, error) {
	kind := ir.OperationKind(name)
	descriptor, registered := runtime.CoreOperationDescriptorFor(kind)
	if !registered {
		return "", fmt.Errorf(
			"codegen.ResolveBodyMarkers: unregistered operation kind %q — "+
				"why: a body may only name a closed orchestration kind; "+
				"where: a SkillBody op marker; "+
				"fix: use one of ir.AllOperationKinds",
			name,
		)
	}
	binding, err := runtime.LookupOperationBinding(target.contract, descriptor)
	if err != nil {
		// Unsupported on this contract: the operation has no native call.
		// Render the neutral kind so the body names the intent, not a tool.
		return string(kind), nil
	}
	if call, native := binding.Native(); native {
		return call.CallName(), nil
	}
	return string(kind), nil
}

// Package codegen — SKILL.md header generation via text/template.
//
// This file ports gen_skills.py to Go. It provides GenerateSkill and
// GenerateSubSkill which render the generated section of SKILL.md files
// using Go text/template with Option("missingkey=error") for StrictUndefined
// parity.
//
// Templates are embedded via go:embed so the binary is fully self-contained.
// The template context is built from GetRoleContext() + spec data lookups.
package codegen

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/pmezard/go-difflib/difflib"
	"gopkg.in/yaml.v3"
)

// ─── GenerateOptions ──────────────────────────────────────────────────────────

// GenerateOptions controls the behaviour of GenerateSkill and GenerateSubSkill.
type GenerateOptions struct {
	// Diff prints a unified diff of old vs new content to stdout (default: true).
	Diff bool

	// Write writes the new content to the skill file (default: true).
	Write bool

	// Init prepends BEGIN/END markers to files that lack them before generating.
	// When false (default), missing markers return a *MarkerError.
	Init bool
}

// DefaultOptions is the default GenerateOptions used when the caller does not
// need to override any field.
var DefaultOptions = GenerateOptions{Diff: true, Write: true}

// ─── Template context types ───────────────────────────────────────────────────

// skillContext is the unified data passed to skill.go.tmpl.
// It merges the former skillHeaderContext and skillBodyContext into a single
// struct for the single-pass pipeline. All fields are exported so
// text/template can access them.
type skillContext struct {
	// Header fields (from role context and spec lookups)
	Role                 RoleSpec
	Commands             []CommandSpec
	Constraints          []ConstraintSpec
	Handoffs             []HandoffSpec
	OwnedPhases          []protocol.PhaseId
	PhasesDetail         []PhaseSpec
	Steps                []ProcedureStep
	PhaseSlug            map[protocol.PhaseId]string
	SubSkills            []string
	Introduction         string
	OwnershipNarrative   string
	Behaviors            []BehaviorSpec
	Checklists           []Checklist
	CoordinationCommands []CoordinationCommand
	Workflows            []Workflow
	FiguresByWorkflow    map[string][]FigureSpec
	ReviewAxes           []ReviewAxisSpec

	// Body fields (from SkillBody, nil/empty if no SkillBody)
	Preamble      string
	BodySections  []ProseSection
	BodyRecipes   []RecipeBlock
	BodyBehaviors []BehaviorSpec
}

// skillSubContext is the unified data passed to skill_sub.go.tmpl.
// It merges the former skillSubFigureContext with body fields.
//
// SubSkillName and CommandTitle drive the YAML frontmatter + curated H1 that
// skill_sub.go.tmpl now emits ABOVE the BEGIN marker (dropPrefix=true). The
// frontmatter `name` is SubSkillName (the skill directory, e.g. "user-uat") so
// the skill registers as an invocable /pasture:<name> command; the H1 heading
// is CommandTitle (CommandSpec.Title, the curated on-disk H1) so the
// hand-authored title is preserved verbatim.
type skillSubContext struct {
	SubSkillName       string
	CommandTitle       string
	CommandName        string
	CommandDescription string
	Figures            []FigureSpec

	// Body fields (from SkillBody, empty if no SkillBody)
	Preamble      string
	BodySections  []ProseSection
	BodyRecipes   []RecipeBlock
	BodyBehaviors []BehaviorSpec
}

// ─── FuncMap ─────────────────────────────────────────────────────────────────

// buildFuncMap builds the template.FuncMap used by both templates.
//
//   - join(items []string, sep string) → strings.Join
//   - lower(s string) → strings.ToLower
//   - last(i, length int) → bool (true when i == length-1)
//   - not(b bool) → bool
func buildFuncMap() template.FuncMap {
	return template.FuncMap{
		"join":  strings.Join,
		"lower": strings.ToLower,
		"last": func(i, length int) bool {
			return i == length-1
		},
		"not": func(b bool) bool {
			return !b
		},
	}
}

// ─── Template loading ─────────────────────────────────────────────────────────

const (
	TemplateSkill    = "templates/skill.go.tmpl"
	TemplateSubSkill = "templates/skill_sub.go.tmpl"
)

// mustParseTemplateFS parses a named template from the shared embedded FS
// (templatesFS, declared in embed.go) with the shared FuncMap and
// missingkey=error option. The template is named by the base filename of the
// pattern (e.g. "templates/skill.go.tmpl" → "skill.go.tmpl")
// so callers can Execute it directly. Panics on parse error — templates are
// embedded compile-time constants and a parse error is a programming error.
func mustParseTemplateFS(pattern string) *template.Template {
	// ParseFS names templates by their base filename, so we must use the same
	// name in template.New for Execute() to find the right template.
	base := pattern
	if i := strings.LastIndex(pattern, "/"); i >= 0 {
		base = pattern[i+1:]
	}
	t, err := template.New(base).
		Option("missingkey=error").
		Funcs(buildFuncMap()).
		ParseFS(templatesFS, pattern)
	if err != nil {
		panic(fmt.Sprintf("codegen: failed to parse embedded template %q: %v", pattern, err))
	}
	return t
}

// ─── Phase slug helper ────────────────────────────────────────────────────────

// buildPhaseSlug returns the PhaseId → "p9-worker-slices" display slug map.
//
// The slug format is "p{number}-{name-kebab-case}" using the phase number from
// PhaseSpecs and the name lowercased with spaces replaced by hyphens.
// Falls back to the bare PhaseId string for phases not in PhaseSpecs.
func buildPhaseSlug() map[protocol.PhaseId]string {
	result := make(map[protocol.PhaseId]string, len(PhaseSpecs)+1)
	// Build slugs for all known phases from PhaseSpecs.
	for phaseId, spec := range PhaseSpecs {
		// e.g. PhaseId("worker-slices") with Number=9 and Name="Worker Slices"
		// → "p9-worker-slices"
		namePart := strings.ToLower(strings.ReplaceAll(spec.Name, " ", "-"))
		slug := fmt.Sprintf("p%d-%s", spec.Number, namePart)
		result[phaseId] = slug
	}
	// Add fallback for any PhaseId not in PhaseSpecs (e.g. PhaseComplete terminal state).
	for _, p := range protocol.AllPhaseIds {
		if _, ok := result[p]; !ok {
			result[p] = string(p)
		}
	}
	return result
}

// ─── Context builders ─────────────────────────────────────────────────────────

// commandsForRole returns all CommandSpec entries whose RoleRef matches roleId,
// sorted by Name for deterministic output.
func commandsForRole(roleId protocol.RoleId) []CommandSpec {
	var result []CommandSpec
	for _, cmd := range CommandSpecs {
		if cmd.RoleRef == roleId {
			result = append(result, cmd)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// subSkillsForRole returns skill invocation names for a role's sub-commands.
// Converts pasture:a:b → pasture:a-b. Skips the main role command (e.g. pasture:worker).
func subSkillsForRole(roleId protocol.RoleId) []string {
	mainCmd := fmt.Sprintf("pasture:%s", roleId)
	var result []string
	for _, cmd := range CommandSpecs {
		if cmd.RoleRef != roleId {
			continue
		}
		if cmd.Name == mainCmd {
			continue
		}
		parts := strings.Split(cmd.Name, ":")
		var skillName string
		if len(parts) >= 3 {
			skillName = fmt.Sprintf("%s:%s-%s", parts[0], parts[1], strings.Join(parts[2:], "-"))
		} else {
			skillName = cmd.Name
		}
		result = append(result, skillName)
	}
	sort.Strings(result)
	return result
}

// constraintsFromRoleContext extracts full ConstraintSpec objects from the
// constraint IDs present in the RoleContext, sorted by ID.
func constraintsFromRoleContext(ctx RoleContext) []ConstraintSpec {
	seen := make(map[string]bool, len(ctx.Constraints))
	for _, cc := range ctx.Constraints {
		seen[cc.Id] = true
	}
	var result []ConstraintSpec
	for id, spec := range ConstraintSpecs {
		if seen[id] {
			result = append(result, spec)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})
	return result
}

// handoffsForRole returns HandoffSpec entries where the role is source or target,
// sorted by ID.
func handoffsForRole(roleId protocol.RoleId) []HandoffSpec {
	var result []HandoffSpec
	for _, h := range HandoffSpecs {
		if h.SourceRole == roleId || h.TargetRole == roleId {
			result = append(result, h)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})
	return result
}

// ownedPhaseDetails returns PhaseSpec objects for phases owned by the role,
// in protocol.PhaseId declaration order (by phase number).
func ownedPhaseDetails(roleSpec RoleSpec) []PhaseSpec {
	var result []PhaseSpec
	for _, phaseId := range roleSpec.OwnedPhases {
		if spec, ok := PhaseSpecs[phaseId]; ok {
			result = append(result, spec)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Number < result[j].Number
	})
	return result
}

// ownedPhasesOrdered returns owned phases sorted by PhaseId declaration order
// (by phase number in PhaseSpecs).
func ownedPhasesOrdered(roleSpec RoleSpec) []protocol.PhaseId {
	// Sort by phase number using PhaseSpecs lookup.
	phases := make([]protocol.PhaseId, len(roleSpec.OwnedPhases))
	copy(phases, roleSpec.OwnedPhases)
	sort.Slice(phases, func(i, j int) bool {
		ni := PhaseSpecs[phases[i]].Number
		nj := PhaseSpecs[phases[j]].Number
		return ni < nj
	})
	return phases
}

// figuresByWorkflow groups figures (with content loaded) by their workflow refs.
func figuresByWorkflow(figures []FigureSpec) map[string][]FigureSpec {
	result := make(map[string][]FigureSpec)
	for _, fig := range figures {
		for _, wfRef := range fig.WorkflowRefs {
			result[wfRef] = append(result[wfRef], fig)
		}
	}
	return result
}

// ─── Figure content loading ───────────────────────────────────────────────────

// figureYAML is the expected schema of a figure YAML file.
type figureYAML struct {
	Content string `yaml:"content"`
}

// loadFigureContent reads skills/protocol/figures/{id}.yaml and returns the
// content field. figuresDir must point to the directory containing these files.
//
// Returns an error if the file is missing, malformed, or has no content.
func loadFigureContent(figureId, figuresDir string) (string, error) {
	path := fmt.Sprintf("%s/%s.yaml", figuresDir, figureId)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.loadFigureContent: figure YAML not found at %q — "+
				"where: figure ID %q, figures dir %q — "+
				"fix: create %s with id, title, type, content fields: %w",
			path, figureId, figuresDir, path, err,
		)
	}
	var fig figureYAML
	if err := yaml.Unmarshal(data, &fig); err != nil {
		return "", fmt.Errorf(
			"codegen.loadFigureContent: malformed YAML in %q — "+
				"fix: ensure valid YAML syntax in the figure file: %w",
			path, err,
		)
	}
	if strings.TrimSpace(fig.Content) == "" {
		return "", fmt.Errorf(
			"codegen.loadFigureContent: empty or missing 'content' key in %q — "+
				"fix: add a 'content' field with the figure's ASCII diagram",
			path,
		)
	}
	return fig.Content, nil
}

// loadFiguresForRole loads figure content from disk for all FigureSpecs
// associated with the given role. Figures without content (not loadable)
// are included with an empty Content field (non-fatal for generation).
// figuresDir is the path to the directory containing figure YAML files.
func loadFiguresForRole(roleId protocol.RoleId, figuresDir string) []FigureSpec {
	var result []FigureSpec
	for _, fig := range FigureSpecs {
		for _, ref := range fig.RoleRefs {
			if ref == roleId {
				content, err := loadFigureContent(fig.Id, figuresDir)
				if err != nil {
					// Non-fatal: include with empty content.
					content = ""
				}
				withContent := fig
				withContent.Content = content
				result = append(result, withContent)
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})
	return result
}

// loadFiguresForCommand loads figure content from disk for FigureSpecs
// associated with the given command ID.
func loadFiguresForCommand(commandId, figuresDir string) []FigureSpec {
	var result []FigureSpec
	for _, fig := range FigureSpecs {
		for _, ref := range fig.CommandRefs {
			if ref == commandId {
				content, err := loadFigureContent(fig.Id, figuresDir)
				if err != nil {
					content = ""
				}
				withContent := fig
				withContent.Content = content
				result = append(result, withContent)
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})
	return result
}

// ─── Diff helper ─────────────────────────────────────────────────────────────

// unifiedDiff returns a contextual unified diff between old and new content.
// Returns an empty string when the contents are identical.
// Uses go-difflib to produce a proper unified diff with 3 lines of context.
func unifiedDiff(fromFile, toFile, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: fromFile,
		ToFile:   toFile + " (generated)",
		Context:  3,
	})
	if err != nil {
		// Fallback: return a simple message if diff fails
		return fmt.Sprintf("--- %s\n+++ %s (generated)\n(diff generation failed: %v)\n", fromFile, toFile, err)
	}
	return diff
}

// ─── Render functions ─────────────────────────────────────────────────────────

// renderSkill renders the complete generated content for a role SKILL.md file,
// including both the header and body content inside BEGIN/END markers.
// This is the single-pass replacement for the former renderHeader + renderBody
// two-pass pipeline.
func renderSkill(roleId protocol.RoleId, figuresDir string, tmplName string) (string, error) {
	if tmplName == "" {
		tmplName = TemplateSkill
	}
	roleSpec, ok := RoleSpecs[roleId]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderSkill: role %q not found in RoleSpecs — "+
				"where: GenerateSkill called with unknown role ID — "+
				"fix: add the role to RoleSpecs in specs_data.go",
			roleId,
		)
	}

	roleCtx := GetRoleContext(roleId)

	phaseSlug := buildPhaseSlug()
	ownedPhases := ownedPhasesOrdered(roleSpec)
	phasesDetail := ownedPhaseDetails(roleSpec)
	commands := commandsForRole(roleId)
	constraints := constraintsFromRoleContext(roleCtx)
	handoffs := handoffsForRole(roleId)
	steps := ProcedureSteps[roleId]
	subSkills := subSkillsForRole(roleId)

	// Load figures with content from disk.
	figures := loadFiguresForRole(roleId, figuresDir)
	fbw := figuresByWorkflow(figures)

	ctx := skillContext{
		Role:                 roleSpec,
		Commands:             commands,
		Constraints:          constraints,
		Handoffs:             handoffs,
		OwnedPhases:          ownedPhases,
		PhasesDetail:         phasesDetail,
		Steps:                steps,
		PhaseSlug:            phaseSlug,
		SubSkills:            subSkills,
		Introduction:         roleSpec.Introduction,
		OwnershipNarrative:   roleSpec.OwnershipNarrative,
		Behaviors:            roleSpec.Behaviors,
		Checklists:           roleCtx.Checklists,
		CoordinationCommands: roleCtx.CoordinationCommands,
		Workflows:            roleCtx.Workflows,
		FiguresByWorkflow:    fbw,
		ReviewAxes:           roleCtx.ReviewAxes,
	}

	// Merge body content if a SkillBody entry exists.
	if body, ok := SkillBodySpecs[string(roleId)]; ok {
		ctx.Preamble = body.Preamble
		ctx.BodySections = body.Sections
		ctx.BodyRecipes = body.Recipes
		ctx.BodyBehaviors = body.Behaviors
	}

	// Pre-render fragment resolution pass: replace placement markers with
	// shared fragment payloads from SharedFragmentSpecs. Templates are unchanged
	// (D5); this pass operates on the context before template execution.
	// When SharedFragmentSpecs is empty (SLICE-1), this is a no-op.
	skillFile := fmt.Sprintf("skills/%s/SKILL.md", roleId)
	resolvedSections, resolvedBehaviors, err := resolveBodyFragments(
		ctx.BodySections, ctx.BodyBehaviors, SharedFragmentSpecs, string(roleId), skillFile,
	)
	if err != nil {
		return "", fmt.Errorf("codegen.renderSkill: fragment resolution failed for role %q: %w", roleId, err)
	}
	ctx.BodySections = resolvedSections
	ctx.BodyBehaviors = resolvedBehaviors

	tmpl := mustParseTemplateFS(tmplName)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf(
			"codegen.renderSkill: template execution failed for role %q — "+
				"where: skill.go.tmpl — "+
				"when: rendering SKILL.md — "+
				"fix: check that the template context matches the template variables: %w",
			roleId, err,
		)
	}
	return buf.String(), nil
}

// renderSubSkill renders the complete generated content for a sub-skill
// SKILL.md file, including both figures and body content inside BEGIN/END
// markers. This is the single-pass replacement for the former
// renderSubSkillHeader + renderBody two-pass pipeline.
func renderSubSkill(commandId, figuresDir string, tmplName string) (string, error) {
	if tmplName == "" {
		tmplName = TemplateSubSkill
	}
	cmdSpec, ok := CommandSpecs[commandId]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderSubSkill: command %q not found in CommandSpecs — "+
				"where: GenerateSubSkill called with unknown command ID — "+
				"fix: add the command to CommandSpecs in specs_data.go",
			commandId,
		)
	}

	figures := loadFiguresForCommand(commandId, figuresDir)

	// skillDirKey is the sub-skill directory name (e.g. "user-uat"), used both
	// as the frontmatter `name` (so the skill registers as /pasture:<name>) and
	// to look up the SkillBody entry below.
	skillDirKey := subSkillDirKey(cmdSpec.File)

	// CommandSpec.Title carries the curated on-disk H1 (captured statically in
	// specs_data.go) so the generator preserves it verbatim above the markers.
	// Guard against an unpopulated Title — every sub-skill in commandSkillDirs
	// must define one, so a zero value is a programming error in specs_data.go.
	if strings.TrimSpace(cmdSpec.Title) == "" {
		return "", fmt.Errorf(
			"codegen.renderSubSkill: command %q (dir %q) has no Title — "+
				"where: CommandSpecs entry in internal/codegen/specs_data.go — "+
				"when: rendering sub-skill frontmatter+H1 (skill_sub.go.tmpl) — "+
				"why: skill_sub.go.tmpl emits '# {{ .CommandTitle }}' above the BEGIN marker and an empty title would drop the curated heading — "+
				"fix: set Title to the sub-skill's curated H1 (without the leading '# ') for command %q",
			commandId, skillDirKey, commandId,
		)
	}

	ctx := skillSubContext{
		SubSkillName:       skillDirKey,
		CommandTitle:       cmdSpec.Title,
		CommandName:        cmdSpec.Name,
		CommandDescription: cmdSpec.Description,
		Figures:            figures,
	}

	// Merge body content if a SkillBody entry exists for this sub-skill directory.
	if body, ok := SkillBodySpecs[skillDirKey]; ok {
		ctx.Preamble = body.Preamble
		ctx.BodySections = body.Sections
		ctx.BodyRecipes = body.Recipes
		ctx.BodyBehaviors = body.Behaviors
	}

	// Pre-render fragment resolution pass: replace placement markers with
	// shared fragment payloads from SharedFragmentSpecs. Templates are unchanged
	// (D5); this pass operates on the context before template execution.
	// When SharedFragmentSpecs is empty (SLICE-1), this is a no-op.
	skillFile := cmdSpec.File
	resolvedSections, resolvedBehaviors, err := resolveBodyFragments(
		ctx.BodySections, ctx.BodyBehaviors, SharedFragmentSpecs, skillDirKey, skillFile,
	)
	if err != nil {
		return "", fmt.Errorf("codegen.renderSubSkill: fragment resolution failed for command %q: %w", commandId, err)
	}
	ctx.BodySections = resolvedSections
	ctx.BodyBehaviors = resolvedBehaviors

	tmpl := mustParseTemplateFS(tmplName)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf(
			"codegen.renderSubSkill: template execution failed for command %q — "+
				"where: skill_sub.go.tmpl — "+
				"when: rendering sub-skill SKILL.md — "+
				"fix: check that the template context matches the template variables: %w",
			commandId, err,
		)
	}
	return buf.String(), nil
}

// ─── Public API ───────────────────────────────────────────────────────────────

// GenerateSkill generates the SKILL.md for a role and optionally writes it.
//
// It uses a single-pass pipeline: the unified template (skill.go.tmpl) renders
// ALL content (header + body) inside the BEGIN/END markers. After marker
// replacement, any content after the END marker is explicitly truncated when
// a SkillBody entry exists (R3: nothing after END).
//
// ReplaceMarkerRegion is called with dropPrefix=true — the template owns the
// full frontmatter and heading (everything before BEGIN is dropped and replaced
// by the rendered template output which starts with YAML frontmatter).
//
// figuresDir is an optional path to the directory containing figure YAML files.
// When empty, figures will have no content (useful for testing without figure files).
//
// Returns the complete new file content.
//
// Returns a *MarkerError if skillPath is missing the BEGIN/END marker pair (and
// Init is false), or if the markers are malformed.
func GenerateSkill(roleId protocol.RoleId, skillPath string, figuresDir string, opts GenerateOptions) (string, error) {
	// Read existing file.
	oldContent, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSkill: cannot read skill file %q — "+
				"where: role %q — "+
				"fix: ensure the file exists before calling GenerateSkill: %w",
			skillPath, roleId, err,
		)
	}
	content := string(oldContent)

	// In Init mode, prepend markers if missing.
	if opts.Init && !HasMarkers(content) {
		content = PrependMarkers(content)
		if opts.Write {
			if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf(
					"codegen.GenerateSkill: cannot write marker-prepended file %q: %w",
					skillPath, err,
				)
			}
		}
	}

	// Single-pass render: template produces all content inside BEGIN/END.
	rendered, err := renderSkill(roleId, figuresDir, TemplateSkill)
	if err != nil {
		return "", err
	}

	// Replace the marker region (drop prefix — template owns frontmatter).
	newContent, err := ReplaceMarkerRegion(content, rendered, true)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSkill: marker replacement failed for %q (role %q): %w",
			skillPath, roleId, err,
		)
	}

	// Explicit truncation: when a SkillBody exists, strip everything after END
	// marker (R3: nothing after END). This handles the transition from the old
	// two-pass pipeline where body content lived after END.
	if _, ok := SkillBodySpecs[string(roleId)]; ok {
		if endIdx := strings.Index(newContent, GeneratedEnd); endIdx >= 0 {
			newContent = newContent[:endIdx+len(GeneratedEnd)] + "\n"
		}
	}

	// Validate the generated markdown structure.
	if err := ValidateSkillStructure([]byte(newContent)); err != nil {
		return "", fmt.Errorf("codegen.GenerateSkill: validate skill %q: %w", roleId, err)
	}

	// Print diff if requested and content changed.
	if opts.Diff && newContent != content {
		fmt.Print(unifiedDiff(skillPath, skillPath, content, newContent))
	}

	// Write to file if requested.
	if opts.Write {
		if err := os.WriteFile(skillPath, []byte(newContent), 0o644); err != nil {
			return "", fmt.Errorf(
				"codegen.GenerateSkill: cannot write skill file %q: %w",
				skillPath, err,
			)
		}
	}

	return newContent, nil
}

// GenerateSubSkill generates the SKILL.md for a sub-skill command.
//
// It uses a single-pass pipeline: the unified template (skill_sub.go.tmpl)
// renders ALL content (YAML frontmatter + curated H1 + figures + body) starting
// with the frontmatter ABOVE the BEGIN marker. After marker replacement, any
// content after END is truncated when a SkillBody entry exists (R3: nothing
// after END).
//
// ReplaceMarkerRegion is called with dropPrefix=true — the template owns the
// full file header (frontmatter + H1), mirroring GenerateSkill for roles. The
// frontmatter `name` is the sub-skill directory key (so the skill registers as
// /pasture:<name>) and `description` is CommandSpec.Description; the curated H1
// is CommandSpec.Title, captured statically so the hand-authored title is
// preserved.
//
// figuresDir is an optional path to the directory containing figure YAML files.
// When empty, figures will have no content (useful for testing without figure files).
//
// Returns the complete new file content.
//
// Returns a *MarkerError if skillPath is missing the BEGIN/END marker pair (and
// Init is false), or if the markers are malformed.
func GenerateSubSkill(commandId string, skillPath string, figuresDir string, opts GenerateOptions) (string, error) {
	// Read existing file.
	oldContent, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSubSkill: cannot read skill file %q — "+
				"where: command %q — "+
				"fix: ensure the file exists before calling GenerateSubSkill: %w",
			skillPath, commandId, err,
		)
	}
	content := string(oldContent)

	// In Init mode, prepend markers if missing. The template now owns the full
	// header (frontmatter + H1) via dropPrefix=true, so markers are prepended —
	// the rendered output replaces everything before END, exactly as for role
	// skills (GenerateSkill). Any pre-existing hand-authored prefix is dropped by
	// the subsequent dropPrefix=true ReplaceMarkerRegion call.
	if opts.Init && !HasMarkers(content) {
		content = PrependMarkers(content)
		if opts.Write {
			if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf(
					"codegen.GenerateSubSkill: cannot write marker-prepended file %q: %w",
					skillPath, err,
				)
			}
		}
	}

	// Single-pass render: template produces frontmatter + H1 + BEGIN/END content.
	rendered, err := renderSubSkill(commandId, figuresDir, TemplateSubSkill)
	if err != nil {
		return "", err
	}

	// Replace the marker region (drop prefix — template owns frontmatter + H1).
	newContent, err := ReplaceMarkerRegion(content, rendered, true)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSubSkill: marker replacement failed for %q (command %q): %w",
			skillPath, commandId, err,
		)
	}

	// Explicit truncation: when a SkillBody exists, strip everything after END.
	cmdSpecForBody := CommandSpecs[commandId]
	skillDirKey := subSkillDirKey(cmdSpecForBody.File)
	if _, ok := SkillBodySpecs[skillDirKey]; ok {
		if endIdx := strings.Index(newContent, GeneratedEnd); endIdx >= 0 {
			newContent = newContent[:endIdx+len(GeneratedEnd)] + "\n"
		}
	}

	// Validate the generated markdown structure.
	if err := ValidateSkillStructure([]byte(newContent)); err != nil {
		return "", fmt.Errorf("codegen.GenerateSubSkill: validate sub-skill %q: %w", commandId, err)
	}

	// Print diff if requested and content changed.
	if opts.Diff && newContent != content {
		fmt.Print(unifiedDiff(skillPath, skillPath, content, newContent))
	}

	// Write to file if requested.
	if opts.Write {
		if err := os.WriteFile(skillPath, []byte(newContent), 0o644); err != nil {
			return "", fmt.Errorf(
				"codegen.GenerateSubSkill: cannot write skill file %q: %w",
				skillPath, err,
			)
		}
	}

	return newContent, nil
}

// subSkillDirKey extracts the skill directory name from a command's File path.
// For example, "skills/supervisor-plan-tasks/SKILL.md" → "supervisor-plan-tasks".
// Returns an empty string if the path does not match the expected format.
func subSkillDirKey(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
}

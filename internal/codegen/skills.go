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

	"github.com/dayvidpham/pasture/internal/types"
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
	Preamble       string
	BodySections   []ProseSection
	BodyRecipes    []RecipeBlock
	BodyBehaviors  []BehaviorSpec
}

// skillSubContext is the unified data passed to skill_sub.go.tmpl.
// It merges the former skillSubFigureContext with body fields.
type skillSubContext struct {
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
	for phaseID, spec := range PhaseSpecs {
		// e.g. PhaseId("worker-slices") with Number=9 and Name="Worker Slices"
		// → "p9-worker-slices"
		namePart := strings.ToLower(strings.ReplaceAll(spec.Name, " ", "-"))
		slug := fmt.Sprintf("p%d-%s", spec.Number, namePart)
		result[phaseID] = slug
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

// commandsForRole returns all CommandSpec entries whose RoleRef matches roleID,
// sorted by Name for deterministic output.
func commandsForRole(roleID types.RoleId) []CommandSpec {
	var result []CommandSpec
	for _, cmd := range CommandSpecs {
		if cmd.RoleRef == roleID {
			result = append(result, cmd)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// subSkillsForRole returns skill invocation names for a role's sub-commands.
// Converts aura:a:b → aura:a-b. Skips the main role command (e.g. aura:worker).
func subSkillsForRole(roleID types.RoleId) []string {
	mainCmd := fmt.Sprintf("aura:%s", roleID)
	var result []string
	for _, cmd := range CommandSpecs {
		if cmd.RoleRef != roleID {
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
		seen[cc.ID] = true
	}
	var result []ConstraintSpec
	for id, spec := range ConstraintSpecs {
		if seen[id] {
			result = append(result, spec)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// handoffsForRole returns HandoffSpec entries where the role is source or target,
// sorted by ID.
func handoffsForRole(roleID types.RoleId) []HandoffSpec {
	var result []HandoffSpec
	for _, h := range HandoffSpecs {
		if h.SourceRole == roleID || h.TargetRole == roleID {
			result = append(result, h)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// ownedPhaseDetails returns PhaseSpec objects for phases owned by the role,
// in protocol.PhaseId declaration order (by phase number).
func ownedPhaseDetails(roleSpec RoleSpec) []PhaseSpec {
	var result []PhaseSpec
	for _, phaseID := range roleSpec.OwnedPhases {
		if spec, ok := PhaseSpecs[phaseID]; ok {
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
func loadFigureContent(figureID, figuresDir string) (string, error) {
	path := fmt.Sprintf("%s/%s.yaml", figuresDir, figureID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.loadFigureContent: figure YAML not found at %q — "+
				"where: figure ID %q, figures dir %q — "+
				"fix: create %s with id, title, type, content fields: %w",
			path, figureID, figuresDir, path, err,
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
func loadFiguresForRole(roleID types.RoleId, figuresDir string) []FigureSpec {
	var result []FigureSpec
	for _, fig := range FigureSpecs {
		for _, ref := range fig.RoleRefs {
			if ref == roleID {
				content, err := loadFigureContent(fig.ID, figuresDir)
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
		return result[i].ID < result[j].ID
	})
	return result
}

// loadFiguresForCommand loads figure content from disk for FigureSpecs
// associated with the given command ID.
func loadFiguresForCommand(commandID, figuresDir string) []FigureSpec {
	var result []FigureSpec
	for _, fig := range FigureSpecs {
		for _, ref := range fig.CommandRefs {
			if ref == commandID {
				content, err := loadFigureContent(fig.ID, figuresDir)
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
		return result[i].ID < result[j].ID
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
func renderSkill(roleID types.RoleId, figuresDir string) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderSkill: role %q not found in RoleSpecs — "+
				"where: GenerateSkill called with unknown role ID — "+
				"fix: add the role to RoleSpecs in specs_data.go",
			roleID,
		)
	}

	roleCtx := GetRoleContext(roleID)

	phaseSlug := buildPhaseSlug()
	ownedPhases := ownedPhasesOrdered(roleSpec)
	phasesDetail := ownedPhaseDetails(roleSpec)
	commands := commandsForRole(roleID)
	constraints := constraintsFromRoleContext(roleCtx)
	handoffs := handoffsForRole(roleID)
	steps := ProcedureSteps[roleID]
	subSkills := subSkillsForRole(roleID)

	// Load figures with content from disk.
	figures := loadFiguresForRole(roleID, figuresDir)
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
	if body, ok := SkillBodySpecs[string(roleID)]; ok {
		ctx.Preamble = body.Preamble
		ctx.BodySections = body.Sections
		ctx.BodyRecipes = body.Recipes
		ctx.BodyBehaviors = body.Behaviors
	}

	tmpl := mustParseTemplateFS("templates/skill.go.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf(
			"codegen.renderSkill: template execution failed for role %q — "+
				"where: skill.go.tmpl — "+
				"when: rendering SKILL.md — "+
				"fix: check that the template context matches the template variables: %w",
			roleID, err,
		)
	}
	return buf.String(), nil
}

// renderSubSkill renders the complete generated content for a sub-skill
// SKILL.md file, including both figures and body content inside BEGIN/END
// markers. This is the single-pass replacement for the former
// renderSubSkillHeader + renderBody two-pass pipeline.
func renderSubSkill(commandID, figuresDir string) (string, error) {
	cmdSpec, ok := CommandSpecs[commandID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderSubSkill: command %q not found in CommandSpecs — "+
				"where: GenerateSubSkill called with unknown command ID — "+
				"fix: add the command to CommandSpecs in specs_data.go",
			commandID,
		)
	}

	figures := loadFiguresForCommand(commandID, figuresDir)

	ctx := skillSubContext{
		CommandName:        cmdSpec.Name,
		CommandDescription: cmdSpec.Description,
		Figures:            figures,
	}

	// Merge body content if a SkillBody entry exists for this sub-skill directory.
	skillDirKey := subSkillDirKey(cmdSpec.File)
	if body, ok := SkillBodySpecs[skillDirKey]; ok {
		ctx.Preamble = body.Preamble
		ctx.BodySections = body.Sections
		ctx.BodyRecipes = body.Recipes
		ctx.BodyBehaviors = body.Behaviors
	}

	tmpl := mustParseTemplateFS("templates/skill_sub.go.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf(
			"codegen.renderSubSkill: template execution failed for command %q — "+
				"where: skill_sub.go.tmpl — "+
				"when: rendering sub-skill SKILL.md — "+
				"fix: check that the template context matches the template variables: %w",
			commandID, err,
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
func GenerateSkill(roleID types.RoleId, skillPath string, figuresDir string, opts GenerateOptions) (string, error) {
	// Read existing file.
	oldContent, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSkill: cannot read skill file %q — "+
				"where: role %q — "+
				"fix: ensure the file exists before calling GenerateSkill: %w",
			skillPath, roleID, err,
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
	rendered, err := renderSkill(roleID, figuresDir)
	if err != nil {
		return "", err
	}

	// Replace the marker region (drop prefix — template owns frontmatter).
	newContent, err := ReplaceMarkerRegion(content, rendered, true)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSkill: marker replacement failed for %q (role %q): %w",
			skillPath, roleID, err,
		)
	}

	// Explicit truncation: when a SkillBody exists, strip everything after END
	// marker (R3: nothing after END). This handles the transition from the old
	// two-pass pipeline where body content lived after END.
	if _, ok := SkillBodySpecs[string(roleID)]; ok {
		if endIdx := strings.Index(newContent, GeneratedEnd); endIdx >= 0 {
			newContent = newContent[:endIdx+len(GeneratedEnd)] + "\n"
		}
	}

	// Validate the generated markdown structure.
	if err := ValidateSkillStructure([]byte(newContent)); err != nil {
		return "", fmt.Errorf("codegen.GenerateSkill: validate skill %q: %w", roleID, err)
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
// renders ALL content (figures + body) inside the BEGIN/END markers. After
// marker replacement, any content after END is truncated when a SkillBody
// entry exists (R3: nothing after END).
//
// ReplaceMarkerRegion is called with dropPrefix=false — the h1 heading before
// the BEGIN marker is hand-authored and preserved.
//
// figuresDir is an optional path to the directory containing figure YAML files.
// When empty, figures will have no content (useful for testing without figure files).
//
// Returns the complete new file content.
//
// Returns a *MarkerError if skillPath is missing the BEGIN/END marker pair (and
// Init is false), or if the markers are malformed.
func GenerateSubSkill(commandID string, skillPath string, figuresDir string, opts GenerateOptions) (string, error) {
	// Read existing file.
	oldContent, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSubSkill: cannot read skill file %q — "+
				"where: command %q — "+
				"fix: ensure the file exists before calling GenerateSubSkill: %w",
			skillPath, commandID, err,
		)
	}
	content := string(oldContent)

	// In Init mode, append markers if missing. For sub-skills the hand-authored
	// H1 heading is the prefix (preserved by dropPrefix=false), so markers must
	// be appended after the existing content, not prepended, to keep the heading
	// before the generated section and maintain valid heading nesting.
	if opts.Init && !HasMarkers(content) {
		content = AppendMarkers(content)
		if opts.Write {
			if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
				return "", fmt.Errorf(
					"codegen.GenerateSubSkill: cannot write marker-appended file %q: %w",
					skillPath, err,
				)
			}
		}
	}

	// Single-pass render: template produces all content inside BEGIN/END.
	rendered, err := renderSubSkill(commandID, figuresDir)
	if err != nil {
		return "", err
	}

	// Replace the marker region (preserve prefix — h1 heading is hand-authored).
	newContent, err := ReplaceMarkerRegion(content, rendered, false)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.GenerateSubSkill: marker replacement failed for %q (command %q): %w",
			skillPath, commandID, err,
		)
	}

	// Explicit truncation: when a SkillBody exists, strip everything after END.
	cmdSpecForBody := CommandSpecs[commandID]
	skillDirKey := subSkillDirKey(cmdSpecForBody.File)
	if _, ok := SkillBodySpecs[skillDirKey]; ok {
		if endIdx := strings.Index(newContent, GeneratedEnd); endIdx >= 0 {
			newContent = newContent[:endIdx+len(GeneratedEnd)] + "\n"
		}
	}

	// Validate the generated markdown structure.
	if err := ValidateSkillStructure([]byte(newContent)); err != nil {
		return "", fmt.Errorf("codegen.GenerateSubSkill: validate sub-skill %q: %w", commandID, err)
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

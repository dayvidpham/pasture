// Package codegen — OpenCode agent definition generation.
//
// The OpenCode harness discovers agents from `.opencode/agent/*.md`.
// Each tool-bearing role keeps its legacy file and receives selectable default
// and provider-variant files. Every file carries OpenCode-flavoured
// YAML frontmatter (description / mode / model / permission) followed by the
// SAME agent body the Claude Code harness emits.
//
// Body reuse (define-once): the agent body is NOT re-authored here. We call
// renderAgent (the single source of the full Claude agent file: Claude YAML
// frontmatter + body), strip the leading Claude frontmatter block, and wrap
// the remaining body in the OpenCode frontmatter via opencode_agent.go.tmpl.
// renderAgent / agents.go / agent_definition.go.tmpl stay untouched.
package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Generator-local lookup tables (kept out of specs_data.go so the shared
// RoleSpec data stays harness-neutral) ─────────────────────────────────────────

// openCodeMode maps each protocol role to its OpenCode agent mode.
//
// OpenCode distinguishes two agent modes:
//   - "primary"  — directly invocable / top-level agents (the orchestration and
//     planning roles a user drives interactively).
//   - "subagent" — agents invoked by other agents (the implementation/review
//     roles that run under a primary's direction).
var openCodeMode = map[protocol.RoleId]string{
	protocol.RoleEpoch:      "primary",
	protocol.RoleSupervisor: "primary",
	protocol.RoleArchitect:  "primary",
	protocol.RoleWorker:     "subagent",
	protocol.RoleReviewer:   "subagent",
}

// openCodeModel maps the harness-neutral RoleSpec.Model nickname to the fully
// qualified OpenCode model id ("<provider>/<model-id>").
//
// Source: the LIVE models.dev catalog (https://models.dev/api.json), anthropic
// provider entries. A committed snapshot of the relevant entries lives at
// internal/codegen/testdata/opencode_models.json; opencode_models_test.go
// asserts each id below EXISTS in that snapshot (catalog-existence, not merely
// map-key presence). Refresh both together when the catalog changes.
var openCodeModel = map[string]string{
	"opus":   "anthropic/claude-opus-4-8",
	"sonnet": "anthropic/claude-sonnet-4-6",
}

// openCodeToolPermission maps each Claude-side tool name to the OpenCode
// permission key it grants. Tools with no OpenCode analog (Agent, SendMessage)
// are intentionally absent and therefore omitted from the emitted permission
// map. Both Edit and Write map to the single OpenCode "edit" permission.
var openCodeToolPermission = map[string]string{
	"Read":  "read",
	"Glob":  "glob",
	"Grep":  "grep",
	"Bash":  "bash",
	"Skill": "skill",
	"Edit":  "edit",
	"Write": "edit",
	"Task":  "task",
}

// ─── Template context ─────────────────────────────────────────────────────────

// openCodePermissionEntry is a single ordered permission rule. The emitter
// builds an ordered slice (rather than a Go map) so the rendered YAML is
// deterministic: the "*": deny seed first, then the granted keys sorted.
type openCodePermissionEntry struct {
	Key   string
	Value string
}

// openCodeAgentTemplateData is the data passed to opencode_agent.go.tmpl.
type openCodeAgentTemplateData struct {
	Description string
	Mode        string
	Model       string
	Permissions []openCodePermissionEntry
	Body        string
}

// OpenCodeProviderID identifies one OpenCode model provider.
type OpenCodeProviderID string

// OpenCodeModelID identifies a model within an OpenCode provider catalog.
type OpenCodeModelID string

// OpenCodeVariantSlug is the filename-safe selectable name of a model variant.
type OpenCodeVariantSlug string

// OpenCodeProviderVariant is the extension record provider catalogs contribute.
// The emitter derives both model and filename fields so catalogs cannot replace
// core frontmatter, least-privilege permissions, or the shared agent body.
type OpenCodeProviderVariant struct {
	Provider OpenCodeProviderID
	Model    OpenCodeModelID
	Slug     OpenCodeVariantSlug
}

var (
	openCodeProviderPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	openCodeModelPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	openCodeSlugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

func (v OpenCodeProviderVariant) qualifiedModel() string {
	return string(v.Provider) + "/" + string(v.Model)
}

func (v OpenCodeProviderVariant) filename(roleID protocol.RoleId) string {
	return fmt.Sprintf("%s--%s--%s.md", roleID, v.Provider, v.Slug)
}

// ─── Emitter ───────────────────────────────────────────────────────────────────

type openCodeAgentEmitter struct {
	Variants []OpenCodeProviderVariant
}

// Emit renders legacy and selectable .opencode/agent definitions for every
// role that has tools (the same role set the Claude Code emitter covers).
func (e openCodeAgentEmitter) Emit(root string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
	variants, err := validateOpenCodeProviderVariants(e.Variants)
	if err != nil {
		return nil, fmt.Errorf("codegen.openCodeAgentEmitter.Emit: provider variant validation failed before generation: %w", err)
	}

	var out []GeneratedFile
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		content, err := renderOpenCodeAgent(roleID, figuresDir)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.openCodeAgentEmitter.Emit: render OpenCode agent for role %q failed: %w",
				roleID, err,
			)
		}
		path := filepath.Join(root, ".opencode", "agent", fmt.Sprintf("%s.md", roleID))
		generated, err := writeFullGeneratedFile(path, content, opts)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.openCodeAgentEmitter.Emit: write OpenCode agent for role %q to %q failed: %w",
				roleID, path, err,
			)
		}
		out = append(out, generated)

		defaultContent, err := renderOpenCodeAgentWithModel(roleID, figuresDir, "")
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.openCodeAgentEmitter.Emit: render default OpenCode agent for role %q failed: %w",
				roleID, err,
			)
		}
		defaultPath := filepath.Join(root, ".opencode", "agent", fmt.Sprintf("%s--default.md", roleID))
		defaultGenerated, err := writeFullGeneratedFile(defaultPath, defaultContent, opts)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.openCodeAgentEmitter.Emit: write default OpenCode agent for role %q to %q failed: %w",
				roleID, defaultPath, err,
			)
		}
		out = append(out, defaultGenerated)

		for _, variant := range variants {
			variantContent, err := renderOpenCodeAgentWithModel(roleID, figuresDir, variant.qualifiedModel())
			if err != nil {
				return nil, fmt.Errorf(
					"codegen.openCodeAgentEmitter.Emit: render OpenCode agent variant %q for role %q failed: %w",
					variant.Slug, roleID, err,
				)
			}
			variantPath := filepath.Join(root, ".opencode", "agent", variant.filename(roleID))
			variantGenerated, err := writeFullGeneratedFile(variantPath, variantContent, opts)
			if err != nil {
				return nil, fmt.Errorf(
					"codegen.openCodeAgentEmitter.Emit: write OpenCode agent variant %q for role %q to %q failed: %w",
					variant.Slug, roleID, variantPath, err,
				)
			}
			out = append(out, variantGenerated)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func validateOpenCodeProviderVariants(variants []OpenCodeProviderVariant) ([]OpenCodeProviderVariant, error) {
	validated := append([]OpenCodeProviderVariant(nil), variants...)
	slugs := make(map[OpenCodeVariantSlug]struct{}, len(validated))
	models := make(map[string]struct{}, len(validated))
	filenames := make(map[string]struct{}, len(validated))

	for index, variant := range validated {
		provider := string(variant.Provider)
		model := string(variant.Model)
		slug := string(variant.Slug)
		if !openCodeProviderPattern.MatchString(provider) || provider == "default" {
			return nil, fmt.Errorf(
				"variant %d has invalid provider ID %q; provider IDs must be lowercase ASCII letters, digits, or single hyphens and cannot be %q — fix the provider catalog entry",
				index, provider, "default",
			)
		}
		if !openCodeModelPattern.MatchString(model) || strings.Contains(model, "..") {
			return nil, fmt.Errorf(
				"variant %d for provider %q has invalid model ID %q; model IDs must be one safe path segment using lowercase ASCII letters, digits, dots, underscores, or hyphens — fix the provider catalog entry",
				index, provider, model,
			)
		}
		if !openCodeSlugPattern.MatchString(slug) || slug == "default" {
			return nil, fmt.Errorf(
				"variant %d for provider %q has unsafe or reserved filename slug %q; slugs must be lowercase ASCII kebab-case and cannot be %q — choose a unique safe slug",
				index, provider, slug, "default",
			)
		}
		if _, exists := slugs[variant.Slug]; exists {
			return nil, fmt.Errorf("variant %d repeats slug %q; duplicate slugs would make selection ambiguous — assign every provider variant a unique slug", index, slug)
		}
		slugs[variant.Slug] = struct{}{}

		qualifiedModel := variant.qualifiedModel()
		if _, exists := models[qualifiedModel]; exists {
			return nil, fmt.Errorf("variant %d repeats qualified model ID %q; conflicting aliases would emit redundant agent definitions — keep one variant per provider model", index, qualifiedModel)
		}
		models[qualifiedModel] = struct{}{}

		filename := variant.filename(protocol.RoleWorker)
		if filepath.Base(filename) != filename || strings.Contains(filename, string(filepath.Separator)) {
			return nil, fmt.Errorf("variant %d derives unsafe filename %q; generation must remain inside .opencode/agent — use safe provider and slug IDs", index, filename)
		}
		if _, exists := filenames[filename]; exists {
			return nil, fmt.Errorf("variant %d conflicts on generated filename %q; choose a distinct provider and slug", index, filename)
		}
		filenames[filename] = struct{}{}
	}

	sort.Slice(validated, func(i, j int) bool {
		if validated[i].Provider != validated[j].Provider {
			return validated[i].Provider < validated[j].Provider
		}
		return validated[i].Slug < validated[j].Slug
	})
	return validated, nil
}

// renderOpenCodeAgent builds the full .opencode/agent/<role>.md content: it
// reuses the Claude agent body verbatim (via renderAgent + frontmatter strip)
// and wraps it in OpenCode frontmatter resolved from the generator-local
// mode/model/permission lookup tables.
func renderOpenCodeAgent(roleID protocol.RoleId, figuresDir string) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: role %q not found in RoleSpecs — "+
				"verify the role ID is defined in specs_data.go",
			roleID,
		)
	}

	model, ok := openCodeModel[roleSpec.Model]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: role %q model nickname %q has no OpenCode model "+
				"mapping — add it to openCodeModel in internal/codegen/opencode_agent.go and to "+
				"the testdata/opencode_models.json snapshot",
			roleID, roleSpec.Model,
		)
	}
	return renderOpenCodeAgentWithModel(roleID, figuresDir, model)
}

// renderOpenCodeAgentWithModel renders one selectable definition. An empty
// model omits the frontmatter key so OpenCode inherits the configured model.
func renderOpenCodeAgentWithModel(roleID protocol.RoleId, figuresDir string, model string) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgentWithModel: role %q not found in RoleSpecs — verify the role ID is defined in specs_data.go",
			roleID,
		)
	}
	mode, ok := openCodeMode[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgentWithModel: role %q has no OpenCode mode mapping — add an entry to openCodeMode in internal/codegen/opencode_agent.go",
			roleID,
		)
	}

	permissions := buildOpenCodePermissions(roleSpec.Tools)

	// Reuse the Claude agent body verbatim (no re-authoring): render the full
	// Claude agent file, then strip its leading YAML frontmatter block.
	claudeAgent, err := renderAgent(roleID, figuresDir)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: renderAgent for role %q failed: %w",
			roleID, err,
		)
	}
	body, err := stripFrontmatter(claudeAgent)
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: strip Claude frontmatter from rendered agent for role %q "+
				"failed: %w — renderAgent output must start with a `---`-delimited YAML frontmatter block",
			roleID, err,
		)
	}

	tmpl, err := template.New("opencode_agent.go.tmpl").
		Option("missingkey=error").
		Funcs(buildFuncMap()).
		ParseFS(templatesFS, "templates/opencode_agent.go.tmpl")
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: parse templates/opencode_agent.go.tmpl failed — "+
				"check the file exists in the embedded FS and has valid Go template syntax: %w",
			err,
		)
	}

	data := openCodeAgentTemplateData{
		Description: roleSpec.Description,
		Mode:        mode,
		Model:       model,
		Permissions: permissions,
		Body:        body,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: execute opencode_agent.go.tmpl for role %q failed: %w",
			roleID, err,
		)
	}

	// Normalize to exactly one trailing newline (the reused Claude body already
	// ends in "\n" and the template appends another after {{ .Body }}, which
	// would double it). This mirrors renderAgent's single-trailing-newline rule.
	content := strings.TrimRight(buf.String(), "\n") + "\n"
	return content, nil
}

// buildOpenCodePermissions produces the least-privilege, deterministically
// ordered permission slice for a role's tools: a leading "*": deny seed, then
// each granted permission key sorted ascending and de-duplicated (Edit + Write
// both map to "edit"). Tools with no OpenCode analog (Agent, SendMessage) are
// omitted because they are absent from openCodeToolPermission.
func buildOpenCodePermissions(tools []string) []openCodePermissionEntry {
	granted := make(map[string]struct{})
	for _, tool := range tools {
		if perm, ok := openCodeToolPermission[tool]; ok {
			granted[perm] = struct{}{}
		}
	}
	keys := make([]string, 0, len(granted))
	for perm := range granted {
		keys = append(keys, perm)
	}
	sort.Strings(keys)

	entries := make([]openCodePermissionEntry, 0, len(keys)+1)
	entries = append(entries, openCodePermissionEntry{Key: `"*"`, Value: "deny"})
	for _, perm := range keys {
		entries = append(entries, openCodePermissionEntry{Key: perm, Value: "allow"})
	}
	return entries
}

// stripFrontmatter removes the leading YAML frontmatter block (the content
// between the first pair of "---" fences, inclusive) from a rendered agent
// file and returns the remaining body. It returns an error if the input does
// not begin with a "---\n" fence or has no closing fence.
func stripFrontmatter(content string) (string, error) {
	const fence = "---\n"
	if !strings.HasPrefix(content, fence) {
		return "", fmt.Errorf("content does not start with a `---` frontmatter fence")
	}
	rest := content[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return "", fmt.Errorf("content has no closing `---` frontmatter fence")
	}
	body := rest[end+len("\n"+fence):]
	return strings.TrimLeft(body, "\n"), nil
}

// Package codegen — OpenCode agent definition generation.
//
// The OpenCode harness discovers agents from `.opencode/agent/<role>.md`
// (one file per role that has tools). Each file carries OpenCode-flavoured
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
	"haiku":  "anthropic/claude-haiku-4-5",
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

// ─── Emitter ───────────────────────────────────────────────────────────────────

type openCodeAgentEmitter struct{}

// Emit renders .opencode/agent/<role>.md for every role that has tools (the
// same role set the Claude Code agent emitter covers).
func (openCodeAgentEmitter) Emit(root string, figuresDir string, opts GenerateOptions) ([]GeneratedFile, error) {
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
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out, nil
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

	mode, ok := openCodeMode[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderOpenCodeAgent: role %q has no OpenCode mode mapping — "+
				"add an entry to openCodeMode in internal/codegen/opencode_agent.go",
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

	content := buf.String()
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
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

// Package codegen — agent definition generation.
//
// This file ports gen_agents.py to Go. It generates agents/{role}.md files
// from schema data for roles that have tools defined. Output files are fully
// generated (no marker-based partial replacement) and overwritten on each run.
//
// GenerateOptions is declared in skills.go and shared with GenerateSkill.
// ownedPhaseDetails, buildPhaseSlug, buildFuncMap, and unifiedDiff are
// declared in skills.go and reused here.
package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Template context ─────────────────────────────────────────────────────────

// agentTemplateData is the data passed to agent_definition.go.tmpl.
type agentTemplateData struct {
	// Role is the full RoleSpec for the role being generated.
	Role RoleSpec

	// PhasesDetail holds PhaseSpec entries for the role's owned phases,
	// sorted by phase number ascending.
	PhasesDetail []PhaseSpec

	// PhaseSlug maps each PhaseId to its slug string ("p9-worker-slices").
	PhaseSlug map[protocol.PhaseId]string

	// Constraints holds the resolved ConstraintContext objects for this role,
	// sorted by ID. These are sourced from GetRoleContext().
	Constraints []ConstraintContext

	// Behaviors holds the role's tactical behaviors from RoleSpec.
	Behaviors []BehaviorSpec

	// Checklists holds the role's completion checklists from RoleContext.
	Checklists []Checklist

	// Workflows holds the role's workflow specifications from RoleContext.
	Workflows []Workflow

	// Figures holds the figure specs for this role (ID + Title references only).
	// Full figure content is NOT included in agent definitions; it is loaded
	// only during SKILL.md generation (see skills.go).
	Figures []FigureSpec
}

// ─── Template rendering ───────────────────────────────────────────────────────

// renderAgent renders the agent definition markdown for the given role.
//
// It loads agent_definition.go.tmpl from the embedded FS (templatesFS,
// declared in embed.go), builds the template context from RoleSpecs and
// GetRoleContext, and executes the template.
// Returns the rendered string (including a trailing newline).
//
// Returns an error if:
//   - The role has no entry in RoleSpecs (programming error or invalid role ID).
//   - The template file cannot be read from the embedded FS.
//   - Template execution fails (e.g., missing key, rendering error).
func renderAgent(roleID types.RoleId) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderAgent: role %q not found in RoleSpecs — "+
				"verify the role ID is defined in specs_data.go",
			roleID,
		)
	}

	// Parse the embedded template, reusing the shared FuncMap from skills.go.
	tmpl, err := template.New("agent_definition.go.tmpl").
		Option("missingkey=error").
		Funcs(buildFuncMap()).
		ParseFS(templatesFS, "templates/agent_definition.go.tmpl")
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderAgent: failed to parse template templates/agent_definition.go.tmpl — "+
				"check that the file exists in the embedded FS and has valid Go template syntax: %w",
			err,
		)
	}

	roleCtx := GetRoleContext(roleID)

	data := agentTemplateData{
		Role:         roleSpec,
		PhasesDetail: ownedPhaseDetails(roleSpec),
		PhaseSlug:    buildPhaseSlug(),
		Constraints:  roleCtx.Constraints,
		Behaviors:    roleSpec.Behaviors,
		Checklists:   roleCtx.Checklists,
		Workflows:    roleCtx.Workflows,
		Figures:      roleCtx.Figures,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf(
			"codegen.renderAgent: template execution failed for role %q — "+
				"check agent_definition.go.tmpl for undefined variables or type mismatches: %w",
			roleID, err,
		)
	}

	content := buf.String()
	// Ensure content always ends with a single newline (mirrors Python behaviour).
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
}

// ─── Public API ───────────────────────────────────────────────────────────────

// GenerateAgent generates agents/{role}.md for a role.
//
// Only generates for roles that have Tools defined (non-empty slice). If the
// role has no tools, GenerateAgent returns an empty string and a nil error —
// the caller should check the returned string before acting on it.
//
// The output file is fully generated — no marker-based partial replacement.
//
// Parameters:
//   - roleID:    The role to generate for (must be in RoleSpecs).
//   - agentPath: Path to write the generated .md file to.
//   - opts:      Controls diff output and whether to write to disk.
//     Note: opts.Init is not used by GenerateAgent (agents are fully generated).
//
// Returns:
//   - The rendered agent definition content (empty string if role has no tools).
//   - An error if rendering or file I/O fails.
//
// Error conditions:
//   - Role not found in RoleSpecs → error with diagnostic message.
//   - Template parse/execution failure → error with template context.
//   - Parent directory creation failure → error with OS error.
//   - File write failure → error with OS error and path.
func GenerateAgent(roleID types.RoleId, agentPath string, opts GenerateOptions) (string, error) {
	roleSpec, ok := RoleSpecs[roleID]
	if !ok {
		return "", fmt.Errorf(
			"codegen.GenerateAgent: role %q not found in RoleSpecs — "+
				"verify the role ID is defined in specs_data.go",
			roleID,
		)
	}

	// Only generate for roles with tools defined (non-empty slice).
	if len(roleSpec.Tools) == 0 {
		return "", nil
	}

	// Read old content for diffing (best-effort; ignore errors if file absent).
	oldContent := ""
	if data, err := os.ReadFile(agentPath); err == nil {
		oldContent = string(data)
	}

	newContent, err := renderAgent(roleID)
	if err != nil {
		return "", err
	}

	// Print diff when content changes and Diff is enabled.
	if opts.Diff && newContent != oldContent {
		fmt.Print(unifiedDiff(agentPath, agentPath, oldContent, newContent))
	}

	// Write to disk when Write is enabled.
	if opts.Write {
		if dir := filepath.Dir(agentPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf(
					"codegen.GenerateAgent: failed to create parent directory %q for role %q — "+
						"check filesystem permissions: %w",
					dir, roleID, err,
				)
			}
		}
		if err := os.WriteFile(agentPath, []byte(newContent), 0o644); err != nil {
			return "", fmt.Errorf(
				"codegen.GenerateAgent: failed to write agent definition to %q for role %q — "+
					"check filesystem permissions: %w",
				agentPath, roleID, err,
			)
		}
	}

	return newContent, nil
}

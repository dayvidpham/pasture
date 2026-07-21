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

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Template context ─────────────────────────────────────────────────────────

// agentHarness identifies the target whose wrapper and instruction-source
// guidance surround the canonical role body.
type agentHarness string

const (
	agentHarnessClaudeCode agentHarness = "claude-code"
	agentHarnessOpenCode   agentHarness = "opencode"
)

// agentRenderContext contains the only harness-specific prose permitted in an
// agent body. Role semantics remain in RoleSpec and the shared body template.
type agentRenderContext struct {
	Harness                   agentHarness
	InstructionSourceGuidance string
	RoleSkillInvocation       string
	ExploreDelegation         string
	TrivialWorkerSelection    string
	TrivialWorkerAvoid        string
	NontrivialWorkerSelection string
	NontrivialWorkerAvoid     string
	WorkflowExplore           string
	WorkflowWorker            string
	WorkflowReviewer          string
}

var agentRenderContexts = map[agentHarness]agentRenderContext{
	agentHarnessClaudeCode: {
		Harness:                   agentHarnessClaudeCode,
		InstructionSourceGuidance: "Follow the project's AGENTS.md and the active Claude Code instructions, including ~/.claude/CLAUDE.md when present.",
	},
	agentHarnessOpenCode: {
		Harness:                   agentHarnessOpenCode,
		InstructionSourceGuidance: "Follow the project's AGENTS.md and the active OpenCode instructions and configuration.",
		RoleSkillInvocation:       "prompt MUST start by invoking the matching `pasture:{role}` skill through the native skill interface so the agent loads its role instructions",
		ExploreDelegation:         "delegate scoped codebase queries to short-lived Explore agents through the native task interface; each delegated agent returns findings and terminates, with no standing team overhead",
		TrivialWorkerSelection:    "select the lowest-cost, lowest-latency available agent definition that is adequate for the work",
		TrivialWorkerAvoid:        "select a high-cost agent definition when a lower-cost definition is adequate",
		NontrivialWorkerSelection: "select an available agent definition with sufficient capability for multi-file, architectural, or logic-heavy work",
		NontrivialWorkerAvoid:     "select a low-capability agent definition for complex work",
		WorkflowExplore:           "Delegate scoped codebase queries to short-lived Explore agents through the native task interface - do not maintain a standing exploration team",
		WorkflowWorker:            "Delegate each slice to a worker through the native task interface. Select an available agent definition whose capability and reasoning effort match the slice complexity.",
		WorkflowReviewer:          "Delegate each per-slice code review to a short-lived reviewer through the native task interface",
	},
}

// agentTemplateData is the data passed to agent_body.go.tmpl.
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

	// Figures holds the figure specs for this role, including full ASCII diagram
	// content when figuresDir is provided to GenerateAgent. When figuresDir is
	// empty, Content fields will be empty (ID + Title only).
	Figures []FigureSpec

	RenderContext agentRenderContext
}

type claudeAgentTemplateData struct {
	Role RoleSpec
	Body string
}

// ─── Template rendering ───────────────────────────────────────────────────────

// renderAgent renders the agent definition markdown for the given role.
//
// It renders the canonical body through a typed Claude Code context, then
// applies Claude Code frontmatter. OpenCode calls the body renderer directly
// with its own context rather than projecting this completed file.
// Returns the rendered string (including a trailing newline).
//
// figuresDir is the path to the directory containing figure YAML files
// (e.g. skills/protocol/figures). When non-empty, figure content is loaded
// from disk so the agent definition includes full ASCII diagram content.
// When empty, figures are included as ID + Title references only.
//
// Returns an error if:
//   - The role has no entry in RoleSpecs (programming error or invalid role ID).
//   - The template file cannot be read from the embedded FS.
//   - Template execution fails (e.g., missing key, rendering error).
func renderAgent(roleId protocol.RoleId, figuresDir string) (string, error) {
	return renderClaudeAgent(roleId, figuresDir)
}

func renderClaudeAgent(roleID protocol.RoleId, figuresDir string) (string, error) {
	body, err := renderAgentBody(roleID, figuresDir, agentHarnessClaudeCode)
	if err != nil {
		return "", err
	}
	roleSpec := RoleSpecs[roleID]
	tmpl, err := template.New("claude_agent.go.tmpl").
		Option("missingkey=error").
		Funcs(buildFuncMap()).
		ParseFS(templatesFS, "templates/claude_agent.go.tmpl")
	if err != nil {
		return "", fmt.Errorf("codegen.renderClaudeAgent: failed to parse templates/claude_agent.go.tmpl — check that the file exists and has valid Go template syntax: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, claudeAgentTemplateData{Role: roleSpec, Body: body}); err != nil {
		return "", fmt.Errorf("codegen.renderClaudeAgent: template execution failed for role %q — check claude_agent.go.tmpl for undefined variables or type mismatches: %w", roleID, err)
	}
	return normalizeTrailingNewline(buf.String()), nil
}

func renderAgentBody(roleId protocol.RoleId, figuresDir string, harness agentHarness) (string, error) {
	roleSpec, ok := RoleSpecs[roleId]
	if !ok {
		return "", fmt.Errorf(
			"codegen.renderAgent: role %q not found in RoleSpecs — "+
				"verify the role ID is defined in specs_data.go",
			roleId,
		)
	}
	renderContext, ok := agentRenderContexts[harness]
	if !ok {
		return "", fmt.Errorf("codegen.renderAgentBody: harness %q has no render context — define its typed instruction-source guidance before rendering role %q", harness, roleId)
	}

	// Parse the embedded template, reusing the shared FuncMap from skills.go.
	tmpl, err := template.New("agent_body.go.tmpl").
		Option("missingkey=error").
		Funcs(buildFuncMap()).
		ParseFS(templatesFS, "templates/agent_body.go.tmpl")
	if err != nil {
		return "", fmt.Errorf(
			"codegen.renderAgentBody: failed to parse template templates/agent_body.go.tmpl — "+
				"check that the file exists in the embedded FS and has valid Go template syntax: %w",
			err,
		)
	}

	roleCtx := GetRoleContext(roleId)

	// Load figure content from disk when figuresDir is provided; otherwise
	// fall back to the context figures (Content fields empty).
	figures := roleCtx.Figures
	if figuresDir != "" {
		figures = loadFiguresForRole(roleId, figuresDir)
	}

	data := agentTemplateData{
		Role:          roleSpec,
		PhasesDetail:  ownedPhaseDetails(roleSpec),
		PhaseSlug:     buildPhaseSlug(),
		Constraints:   roleCtx.Constraints,
		Behaviors:     roleSpec.Behaviors,
		Checklists:    roleCtx.Checklists,
		Workflows:     roleCtx.Workflows,
		Figures:       figures,
		RenderContext: renderContext,
	}
	data = projectAgentTemplateData(data)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf(
			"codegen.renderAgentBody: template execution failed for role %q — "+
				"check agent_body.go.tmpl for undefined variables or type mismatches: %w",
			roleId, err,
		)
	}

	return normalizeTrailingNewline(buf.String()), nil
}

func projectAgentTemplateData(data agentTemplateData) agentTemplateData {
	ctx := data.RenderContext
	data.Constraints = append([]ConstraintContext(nil), data.Constraints...)
	for index := range data.Constraints {
		switch data.Constraints[index].Id {
		case "C-handoff-skill-invocation":
			if ctx.RoleSkillInvocation != "" {
				data.Constraints[index].Then = ctx.RoleSkillInvocation
			}
		case "C-supervisor-explore-ephemeral":
			if ctx.ExploreDelegation != "" {
				data.Constraints[index].Then = ctx.ExploreDelegation
			}
		}
	}

	data.Behaviors = append([]BehaviorSpec(nil), data.Behaviors...)
	for index := range data.Behaviors {
		switch data.Behaviors[index].Id {
		case "B-sup-model-trivial":
			if ctx.TrivialWorkerSelection != "" {
				data.Behaviors[index].Then = ctx.TrivialWorkerSelection
				data.Behaviors[index].ShouldNot = ctx.TrivialWorkerAvoid
			}
		case "B-sup-model-nontrivial":
			if ctx.NontrivialWorkerSelection != "" {
				data.Behaviors[index].Then = ctx.NontrivialWorkerSelection
				data.Behaviors[index].ShouldNot = ctx.NontrivialWorkerAvoid
			}
		}
	}

	data.Workflows = append([]Workflow(nil), data.Workflows...)
	for workflowIndex := range data.Workflows {
		data.Workflows[workflowIndex].Stages = append([]WorkflowStage(nil), data.Workflows[workflowIndex].Stages...)
		for stageIndex := range data.Workflows[workflowIndex].Stages {
			stage := &data.Workflows[workflowIndex].Stages[stageIndex]
			stage.Actions = append([]WorkflowAction(nil), stage.Actions...)
			for actionIndex := range stage.Actions {
				action := &stage.Actions[actionIndex]
				switch action.Id {
				case "rtw-plan-explore":
					if ctx.WorkflowExplore != "" {
						action.Instruction = ctx.WorkflowExplore
					}
				case "rtw-build-spawn":
					if ctx.WorkflowWorker != "" {
						action.Instruction = ctx.WorkflowWorker
					}
				case "rtw-review-spawn":
					if ctx.WorkflowReviewer != "" {
						action.Instruction = ctx.WorkflowReviewer
					}
				}
			}
		}
	}
	return data
}

func normalizeTrailingNewline(content string) string {
	return strings.TrimRight(content, "\n") + "\n"
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
//   - roleId:     The role to generate for (must be in RoleSpecs).
//   - agentPath:  Path to write the generated .md file to.
//   - figuresDir: Path to the directory containing figure YAML files (e.g.
//     skills/protocol/figures). When non-empty, figure content is loaded from
//     disk and embedded in the output. When empty, figures are rendered as
//     ID + Title references only.
//   - opts:       Controls diff output and whether to write to disk.
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
func GenerateAgent(roleId protocol.RoleId, agentPath string, figuresDir string, opts GenerateOptions) (string, error) {
	roleSpec, ok := RoleSpecs[roleId]
	if !ok {
		return "", fmt.Errorf(
			"codegen.GenerateAgent: role %q not found in RoleSpecs — "+
				"verify the role ID is defined in specs_data.go",
			roleId,
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

	newContent, err := renderAgent(roleId, figuresDir)
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
					dir, roleId, err,
				)
			}
		}
		if err := os.WriteFile(agentPath, []byte(newContent), 0o644); err != nil {
			return "", fmt.Errorf(
				"codegen.GenerateAgent: failed to write agent definition to %q for role %q — "+
					"check filesystem permissions: %w",
				agentPath, roleId, err,
			)
		}
	}

	return newContent, nil
}

package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fixture types ────────────────────────────────────────────────────────────

// agentCheck mirrors one entry in testdata/agents.yaml agent_checks.
type agentCheck struct {
	Role                 string   `yaml:"role"`
	MustContain          []string `yaml:"must_contain"`
	MustHaveFigureBlocks bool     `yaml:"must_have_figure_blocks"`
}

// agentSuite is the top-level structure of testdata/agents.yaml.
type agentSuite struct {
	AgentChecks       []agentCheck `yaml:"agent_checks"`
	RolesWithTools    []string     `yaml:"roles_with_tools"`
	RolesWithoutTools []string     `yaml:"roles_without_tools"`
}

// testFiguresDir returns the path to the figures directory used by agent tests.
// Go tests run with the package directory as the working directory, so this
// resolves to <module_root>/skills/protocol/figures from internal/codegen/.
func testFiguresDir() string {
	return filepath.Join("..", "..", "skills", "protocol", "figures")
}

// ─── TestGenerateAgent_SectionChecks ─────────────────────────────────────────

// TestGenerateAgent_SectionChecks verifies that each role listed in the YAML
// fixture produces output containing all expected sections. This is the
// contains-expected-sections test strategy: we do not snapshot the full output
// but verify structural invariants that must hold for every generated agent.
//
// Uses the actual figures directory so that full ASCII diagram content is
// embedded and must_have_figure_blocks assertions can pass.
func TestGenerateAgent_SectionChecks(t *testing.T) {
	var suite agentSuite
	testutil.LoadFixtures(t, testutil.CodegenAgents, &suite)
	require.NotEmpty(t, suite.AgentChecks, "agents.yaml must have agent_checks")

	figuresDir := testFiguresDir()

	for _, check := range suite.AgentChecks {
		check := check
		t.Run(check.Role, func(t *testing.T) {
			role := types.RoleId(check.Role)
			require.True(t, role.IsValid(),
				"fixture role %q is not a valid RoleId — update agents.yaml to use a valid role", check.Role)

			tmpDir := t.TempDir()
			agentPath := filepath.Join(tmpDir, check.Role+".md")

			got, err := codegen.GenerateAgent(role, agentPath, figuresDir, codegen.GenerateOptions{
				Diff:  false,
				Write: false,
			})
			require.NoError(t, err, "GenerateAgent(%q) returned unexpected error", check.Role)
			require.NotEmpty(t, got,
				"GenerateAgent(%q) returned empty content — role may have no tools defined", check.Role)

			for _, expected := range check.MustContain {
				assert.True(t, strings.Contains(got, expected),
					"generated agent for role %q must contain %q\n\nGenerated content:\n%s",
					check.Role, expected, got)
			}

			if check.MustHaveFigureBlocks {
				doc, src := parseMD(t, got)
				assertSectionExists(t, doc, src, 2, "Figures")
				assert.True(t, hasCodeBlock(doc, src, 2, "Figures"),
					"generated agent for role %q must contain code fence blocks inside ## Figures\n\nGenerated content:\n%s",
					check.Role, got)
				figureChildren := sectionChildren(doc, src, 2, "Figures")
				assert.NotEmpty(t, figureChildren,
					"generated agent for role %q must contain at least one figure title (H3 heading inside ## Figures)\n\nGenerated content:\n%s",
					check.Role, got)
			}
		})
	}
}

// ─── TestGenerateAgent_OnlyRolesWithTools ─────────────────────────────────────

// TestGenerateAgent_OnlyRolesWithTools verifies that GenerateAgent only
// produces output for roles that have Tools defined in RoleSpecs. Roles with
// empty Tools must return an empty string (and nil error).
func TestGenerateAgent_OnlyRolesWithTools(t *testing.T) {
	var suite agentSuite
	testutil.LoadFixtures(t, testutil.CodegenAgents, &suite)

	tmpDir := t.TempDir()

	// Roles that should produce output.
	for _, roleStr := range suite.RolesWithTools {
		roleStr := roleStr
		t.Run("has_tools/"+roleStr, func(t *testing.T) {
			role := types.RoleId(roleStr)
			require.True(t, role.IsValid(),
				"fixture role %q is not a valid RoleId", roleStr)

			agentPath := filepath.Join(tmpDir, roleStr+".md")
			got, err := codegen.GenerateAgent(role, agentPath, "", codegen.GenerateOptions{})
			require.NoError(t, err)
			assert.NotEmpty(t, got,
				"GenerateAgent(%q) returned empty content — expected non-empty for role with tools", roleStr)
		})
	}

	// Roles that should produce empty output (no tools).
	for _, roleStr := range suite.RolesWithoutTools {
		roleStr := roleStr
		t.Run("no_tools/"+roleStr, func(t *testing.T) {
			role := types.RoleId(roleStr)
			require.True(t, role.IsValid(),
				"fixture role %q is not a valid RoleId", roleStr)

			agentPath := filepath.Join(tmpDir, roleStr+"-no-tools.md")
			got, err := codegen.GenerateAgent(role, agentPath, "", codegen.GenerateOptions{})
			require.NoError(t, err)
			assert.Empty(t, got,
				"GenerateAgent(%q) returned non-empty content — expected empty for role without tools", roleStr)
		})
	}
}

// ─── TestGenerateAgent_WorkerContent ─────────────────────────────────────────

// TestGenerateAgent_WorkerContent verifies that the worker agent definition
// contains the key constraint IDs that are worker-specific:
// C-worker-gates and C-agent-commit.
func TestGenerateAgent_WorkerContent(t *testing.T) {
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "worker.md")

	got, err := codegen.GenerateAgent(types.RoleWorker, agentPath, "", codegen.GenerateOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, got)

	doc, src := parseMD(t, got)
	assertSectionContains(t, doc, src, 2, "Constraints", "C-worker-gates")
	assertSectionContains(t, doc, src, 2, "Constraints", "C-agent-commit")
}

// ─── TestGenerateAgent_FrontmatterFormat ─────────────────────────────────────

// TestGenerateAgent_FrontmatterFormat verifies that the generated agent
// definition starts with "---" (YAML frontmatter open) and has a second "---"
// closing the frontmatter block before the H1 heading.
func TestGenerateAgent_FrontmatterFormat(t *testing.T) {
	roles := []types.RoleId{
		types.RoleWorker,
		types.RoleSupervisor,
		types.RoleReviewer,
		types.RoleArchitect,
		types.RoleEpoch,
	}

	tmpDir := t.TempDir()

	for _, role := range roles {
		role := role
		t.Run(string(role), func(t *testing.T) {
			agentPath := filepath.Join(tmpDir, string(role)+".md")
			got, err := codegen.GenerateAgent(role, agentPath, "", codegen.GenerateOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, got)

			assert.True(t, strings.HasPrefix(got, "---\n"),
				"generated agent for %q must start with frontmatter opening '---'\n\nContent:\n%s", role, got)

			// Find the closing "---" after the opening one.
			afterOpen := got[4:] // skip "---\n"
			closeIdx := strings.Index(afterOpen, "\n---\n")
			assert.Greater(t, closeIdx, 0,
				"generated agent for %q must have a closing '---' frontmatter delimiter\n\nContent:\n%s", role, got)

			// Verify the H1 heading appears after frontmatter.
			docH1, srcH1 := parseMD(t, got)
			assert.Greater(t, countHeadings(docH1, srcH1, 1), 0,
				"generated agent for %q must contain an H1 heading after frontmatter\n\nContent:\n%s", role, got)
		})
	}
}

// ─── TestGenerateAgent_SupervisorContainsSections ────────────────────────────

// TestGenerateAgent_SupervisorContainsSections verifies that the supervisor
// agent definition contains all required top-level sections with the correct
// content. This is a comprehensive structural test for a single role.
func TestGenerateAgent_SupervisorContainsSections(t *testing.T) {
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "supervisor.md")

	got, err := codegen.GenerateAgent(types.RoleSupervisor, agentPath, "", codegen.GenerateOptions{
		Diff:  false,
		Write: false,
	})
	require.NoError(t, err)
	require.NotEmpty(t, got)

	// Frontmatter fields.
	assert.True(t, strings.Contains(got, "name: supervisor"), "must have name: supervisor")
	assert.True(t, strings.Contains(got, "model: opus"), "must have model: opus")
	assert.True(t, strings.Contains(got, "thinking: medium"), "must have thinking: medium")
	assert.True(t, strings.Contains(got, "tools: Read, Glob, Grep, Bash, Skill, Agent, Task"),
		"must have correct tools list")

	// Parse document for structural assertions.
	doc, src := parseMD(t, got)

	// H1 heading.
	assert.Greater(t, countHeadings(doc, src, 1), 0, "must have H1 heading")
	assertSectionContains(t, doc, src, 1, "Supervisor Agent", "Supervisor")

	// Protocol identity line (bold markers stripped by AST text extraction).
	assertSectionContains(t, doc, src, 1, "Supervisor Agent",
		"You are a Supervisor agent in the Aura Protocol.")

	// Required sections.
	assertSectionExists(t, doc, src, 2, "Owned Phases")
	assertHasTable(t, doc, src, 2, "Owned Phases")
	assertSectionExists(t, doc, src, 2, "Constraints")
	assertSectionExists(t, doc, src, 2, "Behaviors")
	assertSectionExists(t, doc, src, 2, "Completion Checklist")
	assertSectionExists(t, doc, src, 2, "Workflows")
}

// ─── TestGenerateAgent_WritesToDisk ──────────────────────────────────────────

// TestGenerateAgent_WritesToDisk verifies that when opts.Write is true, the
// generated content is written to the specified path on disk.
func TestGenerateAgent_WritesToDisk(t *testing.T) {
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "agents", "worker.md")

	got, err := codegen.GenerateAgent(types.RoleWorker, agentPath, "", codegen.GenerateOptions{
		Write: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, got)

	// Verify file was created.
	data, err := os.ReadFile(agentPath)
	require.NoError(t, err, "file must exist after Write=true")
	assert.Equal(t, got, string(data),
		"file content must match returned content")
}

// ─── TestGenerateAgent_TrailingNewline ────────────────────────────────────────

// TestGenerateAgent_TrailingNewline verifies that all generated agent
// definitions end with a single trailing newline, consistent with the
// Python generate_agent() behaviour.
func TestGenerateAgent_TrailingNewline(t *testing.T) {
	roles := []types.RoleId{
		types.RoleWorker,
		types.RoleSupervisor,
		types.RoleReviewer,
	}

	tmpDir := t.TempDir()

	for _, role := range roles {
		role := role
		t.Run(string(role), func(t *testing.T) {
			agentPath := filepath.Join(tmpDir, string(role)+".md")
			got, err := codegen.GenerateAgent(role, agentPath, "", codegen.GenerateOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, got)
			assert.True(t, strings.HasSuffix(got, "\n"),
				"generated agent for %q must end with a trailing newline", role)
		})
	}
}

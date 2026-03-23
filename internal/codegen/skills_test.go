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

// skillRoleCase mirrors one entry in testdata/skills.yaml role_cases.
type skillRoleCase struct {
	Role               string   `yaml:"role"`
	MustContain        []string `yaml:"must_contain"`
	MustContainHeaders []string `yaml:"must_contain_headers"`
}

// skillSubSkillCase mirrors one entry in testdata/skills.yaml sub_skill_cases.
type skillSubSkillCase struct {
	CommandID   string   `yaml:"command_id"`
	MustContain []string `yaml:"must_contain"`
}

// skillsSuite is the top-level structure of testdata/skills.yaml.
type skillsSuite struct {
	RoleCases    []skillRoleCase    `yaml:"role_cases"`
	SubSkillCases []skillSubSkillCase `yaml:"sub_skill_cases"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// writeSkillFile writes content to a temp file and returns the path.
// The file is removed at the end of the test.
func writeSkillFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "SKILL-*.md")
	require.NoError(t, err, "cannot create temp skill file")
	_, err = f.WriteString(content)
	require.NoError(t, err, "cannot write temp skill file")
	require.NoError(t, f.Close())
	return f.Name()
}

// skillFileWithMarkers returns a minimal SKILL.md content with BEGIN/END markers.
func skillFileWithMarkers() string {
	return codegen.GeneratedBegin + "\n" + codegen.GeneratedEnd + "\n"
}

// ─── TestGenerateSkill_ContainsSections ───────────────────────────────────────

// TestGenerateSkill_ContainsSections verifies that GenerateSkill produces
// output containing the expected sections for each role, as declared in
// testdata/skills.yaml. Uses contains-expected-sections strategy (not exact
// string matching) to allow template evolution without fixture churn.
func TestGenerateSkill_ContainsSections(t *testing.T) {
	var suite skillsSuite
	testutil.LoadFixtures(t, testutil.CodegenSkills, &suite)
	require.NotEmpty(t, suite.RoleCases, "skills.yaml must have role_cases")

	for _, tc := range suite.RoleCases {
		tc := tc
		t.Run(tc.Role, func(t *testing.T) {
			t.Parallel()

			skillPath := writeSkillFile(t, skillFileWithMarkers())
			opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

			result, err := codegen.GenerateSkill(types.RoleId(tc.Role), skillPath, "", opts)
			require.NoError(t, err, "GenerateSkill should not error for role %q", tc.Role)
			require.NotEmpty(t, result, "GenerateSkill should produce non-empty output")

			for _, expected := range tc.MustContain {
				assert.True(t,
					strings.Contains(result, expected),
					"output for role %q should contain %q\n\nActual output (first 1000 chars):\n%s",
					tc.Role, expected, truncate(result, 1000),
				)
			}

			doc, src := parseMD(t, result)
			for _, header := range tc.MustContainHeaders {
				level, title := parseHeaderString(header)
				assertSectionExists(t, doc, src, level, title)
				// Verify H3 sections are nested under the H1 role heading.
				// The skill template places all H3 sections inside ## Protocol Context,
				// which is itself nested inside the H1 role heading.
				if level == 3 {
					// Role heading: "Worker Agent", "Supervisor Agent", etc.
					roleHeading := strings.ToUpper(tc.Role[:1]) + tc.Role[1:] + " Agent"
					assertIsNestedUnder(t, doc, src, roleHeading, title)
				}
			}
		})
	}
}

// ─── TestGenerateSkill_MissingMarkersError ────────────────────────────────────

// TestGenerateSkill_MissingMarkersError verifies that GenerateSkill returns a
// *MarkerError when the skill file is missing the BEGIN/END marker pair.
func TestGenerateSkill_MissingMarkersError(t *testing.T) {
	// Write a file without markers.
	skillPath := writeSkillFile(t, "# Worker Agent\n\nHand-authored content only.\n")
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	_, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.Error(t, err, "GenerateSkill should error when markers are missing")

	var markerErr *codegen.MarkerError
	require.ErrorAs(t, err, &markerErr,
		"error should be a *MarkerError; got: %T: %v", err, err)
	assert.Contains(t, markerErr.Problem, "missing",
		"MarkerError.Problem should describe the missing markers")
}

// ─── TestGenerateSkill_InitMode ───────────────────────────────────────────────

// TestGenerateSkill_InitMode verifies that Init=true prepends markers to a
// file that lacks them, then generates the header successfully.
func TestGenerateSkill_InitMode(t *testing.T) {
	// Write a file without markers.
	skillPath := writeSkillFile(t, "# Worker Agent\n\nHand-authored content.\n")
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: true}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err, "GenerateSkill with Init=true should not error")
	require.NotEmpty(t, result, "GenerateSkill with Init=true should produce non-empty output")

	// The generated output should contain the worker role ID.
	assert.Contains(t, result, "worker",
		"generated output should contain the role name")
	// The hand-authored content below the markers should be preserved.
	assert.Contains(t, result, "Hand-authored content.",
		"generated output should preserve hand-authored body below END marker")
}

// ─── TestGenerateSkill_WriteMode ──────────────────────────────────────────────

// TestGenerateSkill_WriteMode verifies that Write=true actually writes the
// new content to the file on disk.
func TestGenerateSkill_WriteMode(t *testing.T) {
	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	// Read back from disk and verify it matches the returned content.
	diskContent, err := os.ReadFile(skillPath)
	require.NoError(t, err, "should be able to read written file")
	assert.Equal(t, result, string(diskContent),
		"written file content should match returned content")
}

// ─── TestGenerateSkill_NoDiff_NoWrite ────────────────────────────────────────

// TestGenerateSkill_NoDiff_NoWrite verifies that Diff=false, Write=false
// leaves the file unchanged on disk (dry-run mode).
func TestGenerateSkill_NoDiff_NoWrite(t *testing.T) {
	originalContent := skillFileWithMarkers()
	skillPath := writeSkillFile(t, originalContent)
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	_, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	// File on disk should be unchanged.
	diskContent, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	assert.Equal(t, originalContent, string(diskContent),
		"dry-run should not modify file on disk")
}

// ─── TestGenerateSkill_UnknownRole ────────────────────────────────────────────

// TestGenerateSkill_UnknownRole verifies that GenerateSkill returns an error
// for a role ID not present in RoleSpecs.
func TestGenerateSkill_UnknownRole(t *testing.T) {
	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	_, err := codegen.GenerateSkill(types.RoleId("nonexistent-role"), skillPath, "", opts)
	require.Error(t, err, "GenerateSkill should error for unknown role")
	assert.Contains(t, strings.ToLower(err.Error()), "not found",
		"error should mention that the role was not found")
}

// ─── TestGenerateSubSkill_ContainsSections ────────────────────────────────────

// TestGenerateSubSkill_ContainsSections verifies that GenerateSubSkill produces
// output containing the expected sections for each sub-skill command.
func TestGenerateSubSkill_ContainsSections(t *testing.T) {
	var suite skillsSuite
	testutil.LoadFixtures(t, testutil.CodegenSkills, &suite)
	require.NotEmpty(t, suite.SubSkillCases, "skills.yaml must have sub_skill_cases")

	for _, tc := range suite.SubSkillCases {
		tc := tc
		t.Run(tc.CommandID, func(t *testing.T) {
			t.Parallel()

			// Sub-skill files preserve the prefix; write file with a heading before markers.
			content := "# Supervisor Plan Tasks\n\n" + skillFileWithMarkers()
			skillPath := writeSkillFile(t, content)
			opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

			result, err := codegen.GenerateSubSkill(tc.CommandID, skillPath, "", opts)
			require.NoError(t, err,
				"GenerateSubSkill should not error for command %q", tc.CommandID)
			require.NotEmpty(t, result)

			for _, expected := range tc.MustContain {
				assert.True(t,
					strings.Contains(result, expected),
					"output for command %q should contain %q\n\nActual output:\n%s",
					tc.CommandID, expected, truncate(result, 1000),
				)
			}
		})
	}
}

// ─── TestGenerateSubSkill_PreservesPrefix ─────────────────────────────────────

// TestGenerateSubSkill_PreservesPrefix verifies that GenerateSubSkill preserves
// the hand-authored content before the BEGIN marker (the h1 heading).
func TestGenerateSubSkill_PreservesPrefix(t *testing.T) {
	heading := "# My Sub-Skill Command\n\n"
	content := heading + skillFileWithMarkers()
	skillPath := writeSkillFile(t, content)
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", opts)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(result, heading),
		"GenerateSubSkill should preserve the hand-authored heading prefix\n"+
			"Expected prefix: %q\nActual start: %q",
		heading, result[:minInt(len(result), 60)])
}

// ─── TestGenerateSubSkill_MissingMarkersError ─────────────────────────────────

// TestGenerateSubSkill_MissingMarkersError verifies that GenerateSubSkill
// returns a *MarkerError when the skill file has no markers.
func TestGenerateSubSkill_MissingMarkersError(t *testing.T) {
	skillPath := writeSkillFile(t, "# Plan Tasks\n\nHand-authored only.\n")
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	_, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", opts)
	require.Error(t, err)

	var markerErr *codegen.MarkerError
	require.ErrorAs(t, err, &markerErr,
		"error should be a *MarkerError; got: %T: %v", err, err)
}

// ─── TestGenerateSubSkill_InitMode ─────────────────────────────────────────────

// TestGenerateSubSkill_InitMode verifies that Init=true prepends markers to a
// sub-skill file that lacks them, then generates the header successfully while
// preserving the hand-authored heading prefix.
func TestGenerateSubSkill_InitMode(t *testing.T) {
	heading := "# Plan Tasks\n\nHand-authored body.\n"
	skillPath := writeSkillFile(t, heading)
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: true}

	result, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", opts)
	require.NoError(t, err, "GenerateSubSkill with Init=true should not error")
	require.NotEmpty(t, result, "GenerateSubSkill with Init=true should produce non-empty output")

	// The hand-authored heading should be preserved (dropPrefix=false for sub-skills).
	doc, src := parseMD(t, result)
	assertSectionExists(t, doc, src, 1, "Plan Tasks")
	assertSectionContains(t, doc, src, 1, "Plan Tasks", "Hand-authored body.")
	// The generated section should contain the markers (template markers, not markdown structure).
	assert.Contains(t, result, codegen.GeneratedBegin,
		"generated output should contain BEGIN marker")
	assert.Contains(t, result, codegen.GeneratedEnd,
		"generated output should contain END marker")
	// cmd-sup-plan has the Layer Cake figure: verify it is nested under the page H1.
	// The sub-skill template renders figure headings at H3 (skipping H2), which is
	// valid as a relative parent-child relationship under the H1 page title.
	assertIsNestedUnder(t, doc, src, "Plan Tasks", "Layer Cake — TDD Parallelism Within Vertical Slices")
}

// ─── TestGenerateSubSkill_UnknownCommand ──────────────────────────────────────

// TestGenerateSubSkill_UnknownCommand verifies that GenerateSubSkill returns an
// error for a command ID not present in CommandSpecs.
func TestGenerateSubSkill_UnknownCommand(t *testing.T) {
	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	_, err := codegen.GenerateSubSkill("cmd-nonexistent", skillPath, "", opts)
	require.Error(t, err, "GenerateSubSkill should error for unknown command")
	assert.Contains(t, strings.ToLower(err.Error()), "not found",
		"error should mention that the command was not found")
}

// ─── TestGenerateSkill_WithFiguresDir ─────────────────────────────────────────

// TestGenerateSkill_WithFiguresDir verifies that GenerateSkill works when
// a real figuresDir is provided that contains figure YAML files.
// This test creates temporary figure files and verifies content appears in output.
func TestGenerateSkill_WithFiguresDir(t *testing.T) {
	// Create a temporary figures directory with a minimal figure YAML.
	figuresDir := t.TempDir()
	figureYAML := "id: layer-cake\ntitle: Layer Cake\ntype: ascii-diagram\ncontent: |\n  test figure content here\n"
	err := os.WriteFile(filepath.Join(figuresDir, "layer-cake.yaml"), []byte(figureYAML), 0o644)
	require.NoError(t, err, "cannot write test figure YAML")

	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, figuresDir, opts)
	require.NoError(t, err)

	// The figure content should appear in the generated output.
	assert.Contains(t, result, "test figure content here",
		"generated output should contain loaded figure content")
}

// ─── TestGenerateSkill_TemplateOwnsBeginMarker ────────────────────────────────

// TestGenerateSkill_TemplateOwnsBeginMarker verifies that the BEGIN marker
// appears in the generated output (template renders it).
func TestGenerateSkill_TemplateOwnsBeginMarker(t *testing.T) {
	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	assert.Contains(t, result, codegen.GeneratedBegin)
	assert.Contains(t, result, codegen.GeneratedEnd)
}

// ─── TestGenerateSkill_BodyPreserved ─────────────────────────────────────────

// TestGenerateSkill_BodyPreserved verifies that hand-authored content below
// the END marker is preserved after generation.
func TestGenerateSkill_BodyPreserved(t *testing.T) {
	body := "\n\n## My Custom Section\n\nThis is hand-authored.\n"
	content := skillFileWithMarkers() + body
	skillPath := writeSkillFile(t, content)
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	doc, src := parseMD(t, result)
	assertSectionExists(t, doc, src, 2, "My Custom Section")
	assertSectionContains(t, doc, src, 2, "My Custom Section", "This is hand-authored.")
	// Verify H3 generated sections are nested under the role H1 heading.
	assertIsNestedUnder(t, doc, src, "Worker Agent", "Constraints (Given/When/Then/Should Not)")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// truncate returns the first n runes of s, for use in error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// minInt returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseHeaderString parses a markdown header string like "## My Section" into
// its level (number of leading '#' characters) and trimmed title text.
// Panics if the string does not start with at least one '#'.
func parseHeaderString(header string) (level int, title string) {
	i := 0
	for i < len(header) && header[i] == '#' {
		i++
	}
	if i == 0 {
		panic("parseHeaderString: header does not start with '#': " + header)
	}
	return i, strings.TrimSpace(header[i:])
}

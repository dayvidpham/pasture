package codegen_test

import (
	"os"
	"path/filepath"
	"runtime"
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

// suppressBodySpec removes a SkillBodySpecs entry for the duration of the test,
// restoring it via t.Cleanup.
func suppressBodySpec(t *testing.T, key string) {
	t.Helper()
	prev, had := codegen.SkillBodySpecs[key]
	delete(codegen.SkillBodySpecs, key)
	t.Cleanup(func() {
		if had {
			codegen.SkillBodySpecs[key] = prev
		} else {
			delete(codegen.SkillBodySpecs, key)
		}
	})
}

// withBodySpec temporarily sets a SkillBodySpecs entry for the test duration,
// restoring the previous value (or deleting the key) via t.Cleanup.
func withBodySpec(t *testing.T, key string, body codegen.SkillBody) {
	t.Helper()
	prev, had := codegen.SkillBodySpecs[key]
	codegen.SkillBodySpecs[key] = body
	t.Cleanup(func() {
		if had {
			codegen.SkillBodySpecs[key] = prev
		} else {
			delete(codegen.SkillBodySpecs, key)
		}
	})
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
// The body pass is suppressed (no body spec for the role) so this test
// focuses on marker initialization and header generation.
func TestGenerateSkill_InitMode(t *testing.T) {
	// Suppress any body spec for worker to isolate header-only Init behaviour.
	suppressBodySpec(t, string(types.RoleWorker))

	// Write a file without markers.
	skillPath := writeSkillFile(t, "# Worker Agent\n\nHand-authored content.\n")
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: true}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err, "GenerateSkill with Init=true should not error")
	require.NotEmpty(t, result, "GenerateSkill with Init=true should produce non-empty output")

	// The generated output should contain the worker role ID.
	assert.Contains(t, result, "worker",
		"generated output should contain the role name")
	// Without a body spec, hand-authored content below END must be preserved.
	assert.Contains(t, result, "Hand-authored content.",
		"generated output should preserve hand-authored body below END marker when no body spec exists")
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
		heading, result[:min(len(result), 60)])
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
// the END marker is preserved after generation when no body spec is registered.
// When a body spec exists, the body pass intentionally replaces that content.
func TestGenerateSkill_BodyPreserved(t *testing.T) {
	// Suppress body spec so this test isolates header-pass preservation.
	suppressBodySpec(t, string(types.RoleWorker))

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

// ─── TestGenerateIdempotent ──────────────────────────────────────────────────

// repoRoot returns the absolute path to the repository root by navigating
// up from the test file's directory (internal/codegen/) to the module root.
// Panics if runtime.Caller fails — this is a programming error in tests.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repoRoot: runtime.Caller(0) failed — cannot determine test file location")
	}
	// thisFile is .../internal/codegen/skills_test.go
	// Navigate up two directories to reach the repo root.
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestGenerateIdempotent verifies that running the code generator on the
// checked-in SKILL.md files produces identical output (zero diff).
//
// If this test fails, it means the code generator output has drifted from
// the on-disk files — run `go generate ./internal/codegen/...` (or the
// equivalent make target) to regenerate, then commit the updated files.
func TestGenerateIdempotent(t *testing.T) {
	root := repoRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")

	// Verify figures directory exists.
	_, err := os.Stat(figuresDir)
	require.NoError(t, err,
		"figures directory not found at %q — "+
			"ensure the test is run from the correct working directory", figuresDir)

	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	// ─── Role skills ────────────────────────────────────────────────────

	roleTests := []struct {
		name   string
		roleID types.RoleId
		file   string // relative path from repo root
	}{
		{name: "supervisor", roleID: types.RoleSupervisor, file: "skills/supervisor/SKILL.md"},
		{name: "architect", roleID: types.RoleArchitect, file: "skills/architect/SKILL.md"},
		{name: "worker", roleID: types.RoleWorker, file: "skills/worker/SKILL.md"},
		{name: "reviewer", roleID: types.RoleReviewer, file: "skills/reviewer/SKILL.md"},
	}

	for _, tc := range roleTests {
		tc := tc
		t.Run("role/"+tc.name, func(t *testing.T) {
			t.Parallel()

			skillPath := filepath.Join(root, tc.file)
			onDisk, err := os.ReadFile(skillPath)
			require.NoError(t, err,
				"cannot read on-disk SKILL.md at %q — "+
					"ensure the file exists in the repository", skillPath)

			generated, err := codegen.GenerateSkill(tc.roleID, skillPath, figuresDir, opts)
			require.NoError(t, err,
				"GenerateSkill failed for role %q with skill file %q", tc.roleID, skillPath)

			assert.Equal(t, string(onDisk), generated,
				"GenerateSkill output for role %q differs from on-disk %q — "+
					"run 'go generate ./internal/codegen/...' to regenerate, then commit the updated file",
				tc.roleID, tc.file)
		})
	}

	// ─── Sub-skills ─────────────────────────────────────────────────────

	subSkillTests := []struct {
		name      string
		commandID string
		file      string // relative path from repo root
	}{
		{name: "cmd-sup-plan", commandID: "cmd-sup-plan", file: "skills/supervisor-plan-tasks/SKILL.md"},
		{name: "cmd-sup-spawn", commandID: "cmd-sup-spawn", file: "skills/supervisor-spawn-worker/SKILL.md"},
		{name: "cmd-impl-review", commandID: "cmd-impl-review", file: "skills/impl-review/SKILL.md"},
	}

	for _, tc := range subSkillTests {
		tc := tc
		t.Run("sub-skill/"+tc.name, func(t *testing.T) {
			t.Parallel()

			skillPath := filepath.Join(root, tc.file)
			onDisk, err := os.ReadFile(skillPath)
			require.NoError(t, err,
				"cannot read on-disk SKILL.md at %q — "+
					"ensure the file exists in the repository", skillPath)

			generated, err := codegen.GenerateSubSkill(tc.commandID, skillPath, figuresDir, opts)
			require.NoError(t, err,
				"GenerateSubSkill failed for command %q with skill file %q", tc.commandID, skillPath)

			assert.Equal(t, string(onDisk), generated,
				"GenerateSubSkill output for command %q differs from on-disk %q — "+
					"run 'go generate ./internal/codegen/...' to regenerate, then commit the updated file",
				tc.commandID, tc.file)
		})
	}
}

// ─── TestTwoPass_HeaderThenBody ───────────────────────────────────────────────

// TestTwoPass_HeaderRegionUnchangedByBodyPass verifies the two-pass pipeline
// interaction: after GenerateSkill runs (header pass + body pass), the
// generated header region (BEGIN..END block) is untouched by the body pass.
//
// This test registers a temporary SkillBody entry so it exercises the body
// path without depending on real content in SkillBodySpecs.
func TestTwoPass_HeaderRegionUnchangedByBodyPass(t *testing.T) {
	// Register a test body under a sentinel key that matches the worker role.
	testBody := codegen.SkillBody{
		Preamble: "Test preamble paragraph.",
		Sections: []codegen.ProseSection{
			{
				ID:      "test-section",
				Title:   "Test Section",
				Content: "Test section content.",
			},
		},
	}
	withBodySpec(t, string(types.RoleWorker), testBody)

	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err, "GenerateSkill with registered body should not error")

	// The header region (BEGIN..END) must still be present and contain the
	// role-specific generated content (not overwritten by the body pass).
	assert.Contains(t, result, codegen.GeneratedBegin,
		"BEGIN marker must be present after two-pass generation")
	assert.Contains(t, result, codegen.GeneratedEnd,
		"END marker must be present after two-pass generation")

	// The generated header content (role ID) must be inside the marker region.
	assert.Contains(t, result, "worker",
		"generated header content (role ID) must survive the body pass")

	// The body content must appear after the END marker.
	endIdx := strings.Index(result, codegen.GeneratedEnd)
	require.Greater(t, endIdx, 0, "END marker must be in the result")
	afterEnd := result[endIdx+len(codegen.GeneratedEnd):]
	assert.Contains(t, afterEnd, "Test Section",
		"body section title must appear after the END marker")
	assert.Contains(t, afterEnd, "Test preamble paragraph.",
		"body preamble must appear after the END marker")
}

// TestTwoPass_NoBodySpec_HeaderOnly verifies that when no SkillBody is
// registered for a role, GenerateSkill produces output identical to the
// header-only result (body pass is a no-op).
func TestTwoPass_NoBodySpec_HeaderOnly(t *testing.T) {
	// Suppress body spec for worker to test header-only path.
	suppressBodySpec(t, string(types.RoleWorker))

	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(types.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	// Without a body spec the output ends at (or just after) the END marker.
	// It must not contain any body-injected content.
	endIdx := strings.Index(result, codegen.GeneratedEnd)
	require.Greater(t, endIdx, 0)
	afterEnd := result[endIdx+len(codegen.GeneratedEnd):]
	// Only trailing newlines/whitespace should follow END — no body sections.
	assert.Equal(t, strings.TrimSpace(afterEnd), "",
		"without a body spec, nothing should appear after the END marker")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// truncate returns the first n runes of s, for use in error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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

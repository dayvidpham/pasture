package codegen_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	CommandId   string   `yaml:"command_id"`
	MustContain []string `yaml:"must_contain"`
}

// skillsSuite is the top-level structure of testdata/skills.yaml.
type skillsSuite struct {
	RoleCases     []skillRoleCase     `yaml:"role_cases"`
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

			result, err := codegen.GenerateSkill(protocol.RoleId(tc.Role), skillPath, "", opts)
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

	_, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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
	suppressBodySpec(t, string(protocol.RoleWorker))

	// Write a file without markers.
	skillPath := writeSkillFile(t, "# Worker Agent\n\nHand-authored content.\n")
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: true}

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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

	_, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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

	_, err := codegen.GenerateSkill(protocol.RoleId("nonexistent-role"), skillPath, "", opts)
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
		t.Run(tc.CommandId, func(t *testing.T) {
			t.Parallel()

			// Sub-skill files preserve the prefix; write file with a heading before markers.
			content := "# Supervisor Plan Tasks\n\n" + skillFileWithMarkers()
			skillPath := writeSkillFile(t, content)
			opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

			result, err := codegen.GenerateSubSkill(tc.CommandId, skillPath, "", opts)
			require.NoError(t, err,
				"GenerateSubSkill should not error for command %q", tc.CommandId)
			require.NotEmpty(t, result)

			for _, expected := range tc.MustContain {
				assert.True(t,
					strings.Contains(result, expected),
					"output for command %q should contain %q\n\nActual output:\n%s",
					tc.CommandId, expected, truncate(result, 1000),
				)
			}
		})
	}
}

// ─── TestGenerateSubSkill_OwnsHeader ──────────────────────────────────────────

// TestGenerateSubSkill_OwnsHeader verifies the D5/SLICE-3 contract: the
// generator OWNS the sub-skill header (dropPrefix=true). Any pre-existing
// hand-authored prefix above the BEGIN marker is dropped and replaced by the
// rendered YAML frontmatter (`name` = sub-skill dir key, `description` =
// CommandSpec.Description) followed by the curated H1 (CommandSpec.Title),
// mirroring GenerateSkill for roles. This is what makes the sub-skill register
// as an invocable /pasture:<name> command.
func TestGenerateSubSkill_OwnsHeader(t *testing.T) {
	// A pre-existing hand-authored prefix that must be REPLACED, not preserved.
	prefix := "# Stale Hand-Authored Title\n\n"
	content := prefix + skillFileWithMarkers()
	skillPath := writeSkillFile(t, content)
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", opts)
	require.NoError(t, err)

	// The stale prefix must be gone (dropPrefix=true drops everything before BEGIN).
	assert.False(t, strings.Contains(result, "Stale Hand-Authored Title"),
		"GenerateSubSkill (dropPrefix=true) should drop the pre-existing prefix\n"+
			"Actual output:\n%s", truncate(result, 400))

	// The output must START with the generated YAML frontmatter (name = dir key,
	// description = CommandSpec.Description), exactly mirroring role skills.
	wantFrontmatter := "---\nname: supervisor-plan-tasks\n" +
		"description: Decompose ratified plan into vertical slices (SLICE-N)\n---\n"
	assert.True(t, strings.HasPrefix(result, wantFrontmatter),
		"GenerateSubSkill should emit YAML frontmatter at the top of the file\n"+
			"Expected prefix: %q\nActual start: %q",
		wantFrontmatter, result[:min(len(result), 120)])

	// The curated H1 (CommandSpec.Title) must appear ABOVE the BEGIN marker.
	h1Idx := strings.Index(result, "# Supervisor Plan Tasks")
	beginIdx := strings.Index(result, codegen.GeneratedBegin)
	require.GreaterOrEqual(t, h1Idx, 0,
		"curated H1 '# Supervisor Plan Tasks' (CommandSpec.Title) should be present")
	require.GreaterOrEqual(t, beginIdx, 0, "BEGIN marker should be present")
	assert.Less(t, h1Idx, beginIdx,
		"curated H1 must appear ABOVE the BEGIN marker, not below it")
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
// sub-skill file that lacks them, then generates the header successfully.
//
// Under the D5/SLICE-3 contract (dropPrefix=true) the generator OWNS the header:
// the hand-authored input prefix is dropped and replaced by the generated YAML
// frontmatter + curated H1 (CommandSpec.Title), exactly like role skills.
func TestGenerateSubSkill_InitMode(t *testing.T) {
	// Hand-authored input whose title differs from the curated CommandSpec.Title.
	heading := "# Stale Title\n\nHand-authored body.\n"
	skillPath := writeSkillFile(t, heading)
	opts := codegen.GenerateOptions{Diff: false, Write: true, Init: true}

	result, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", opts)
	require.NoError(t, err, "GenerateSubSkill with Init=true should not error")
	require.NotEmpty(t, result, "GenerateSubSkill with Init=true should produce non-empty output")

	// The generated frontmatter must appear at the top (name = dir key).
	assert.True(t, strings.HasPrefix(result, "---\nname: supervisor-plan-tasks\n"),
		"Init-mode output should start with generated YAML frontmatter\nActual start: %q",
		result[:min(len(result), 120)])

	// The curated H1 (CommandSpec.Title) replaces the stale hand-authored title.
	doc, src := parseMD(t, result)
	assertSectionExists(t, doc, src, 1, "Supervisor Plan Tasks")
	assert.False(t, strings.Contains(result, "Stale Title"),
		"dropPrefix=true should drop the hand-authored input prefix/body")
	// The generated section should contain the markers.
	assert.Contains(t, result, codegen.GeneratedBegin,
		"generated output should contain BEGIN marker")
	assert.Contains(t, result, codegen.GeneratedEnd,
		"generated output should contain END marker")
	// cmd-sup-plan has the Layer Cake figure: verify it is nested under the curated H1.
	// The sub-skill template renders figure headings at H3 (skipping H2), which is
	// valid as a relative parent-child relationship under the H1 page title.
	assertIsNestedUnder(t, doc, src, "Supervisor Plan Tasks", "Layer Cake — TDD Parallelism Within Vertical Slices")
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

// ─── TestSubSkillDirKey ───────────────────────────────────────────────────────

// TestSubSkillDirKey verifies that SubSkillDirKey extracts the skill directory
// name from various path formats, including edge cases like single-component
// paths, empty strings, and trailing slashes.
func TestSubSkillDirKey(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"normal path", "skills/supervisor-plan-tasks/SKILL.md", "supervisor-plan-tasks"},
		{"impl-review", "skills/impl-review/SKILL.md", "impl-review"},
		{"single component", "SKILL.md", ""},
		{"empty string", "", ""},
		{"trailing slash", "skills/foo/", "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codegen.SubSkillDirKey(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
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

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, figuresDir, opts)
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

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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
	suppressBodySpec(t, string(protocol.RoleWorker))

	body := "\n\n## My Custom Section\n\nThis is hand-authored.\n"
	content := skillFileWithMarkers() + body
	skillPath := writeSkillFile(t, content)
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
	require.NoError(t, err)

	doc, src := parseMD(t, result)
	assertSectionExists(t, doc, src, 2, "My Custom Section")
	assertSectionContains(t, doc, src, 2, "My Custom Section", "This is hand-authored.")
	// Verify H3 generated sections are nested under the role H1 heading.
	assertIsNestedUnder(t, doc, src, "Worker Agent", "General Constraints")
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

// ─── Generated-skill enumeration (single source of truth for L3 tables) ──────

// idempotentRoleSkill describes one role-level SKILL.md driven through
// GenerateSkill. These 5 roles are exactly the keys of roleSkillDirs in
// harness.go; the generator writes them on every `go generate`.
type idempotentRoleSkill struct {
	name   string
	roleId protocol.RoleId
}

// allRoleSkills is the full set of generator-driven role SKILL.md files (5).
// It mirrors roleSkillDirs in harness.go.
var allRoleSkills = []idempotentRoleSkill{
	{name: "supervisor", roleId: protocol.RoleSupervisor},
	{name: "architect", roleId: protocol.RoleArchitect},
	{name: "worker", roleId: protocol.RoleWorker},
	{name: "reviewer", roleId: protocol.RoleReviewer},
	{name: "epoch", roleId: protocol.RoleEpoch},
}

// allSubSkillCommandIds is the full set of generator-driven sub-skill command
// IDs (24). It mirrors commandSkillDirs in harness.go exactly; these
// are the commands GenerateSubSkill renders on every `go generate`. Each gains
// YAML frontmatter (name = dir key, description = CommandSpec.Description) above
// its curated H1 (CommandSpec.Title) per D5/SLICE-3.
//
// Together with allRoleSkills (5) this enumerates all 29 generator-managed
// SKILL.md files. The remaining 2 on-disk skills (protocol, install-cli) are
// hand-authored and carry no BEGIN/END markers, so they are NOT generated and
// are intentionally excluded from these tables.
var allSubSkillCommandIds = []string{
	"cmd-sup-plan",
	"cmd-sup-spawn",
	"cmd-impl-review",
	"cmd-arch-handoff",
	"cmd-arch-propose",
	"cmd-arch-ratify",
	"cmd-arch-review",
	"cmd-explore",
	"cmd-impl-slice",
	"cmd-research",
	"cmd-rev-comment",
	"cmd-rev-code",
	"cmd-rev-plan",
	"cmd-rev-vote",
	"cmd-status",
	"cmd-sup-commit",
	"cmd-sup-track",
	"cmd-swarm",
	"cmd-user-elicit",
	"cmd-user-request",
	"cmd-user-uat",
	"cmd-work-blocked",
	"cmd-work-complete",
	"cmd-work-impl",
}

// subSkillCommand resolves a sub-skill command ID to its CommandSpec, the
// derived skill directory key (e.g. "user-uat"), and the repo-root-relative
// SKILL.md path. It t.Fatal()s if the command is unknown so a typo in the table
// surfaces as a clear failure rather than a silent skip.
func subSkillCommand(t *testing.T, commandId string) (spec codegen.CommandSpec, dirKey, relPath string) {
	t.Helper()
	spec, ok := codegen.CommandSpecs[commandId]
	require.True(t, ok,
		"sub-skill command %q not found in CommandSpecs — "+
			"the L3 table in skills_test.go is out of sync with specs_data.go", commandId)
	dirKey = codegen.SubSkillDirKey(spec.File)
	require.NotEmpty(t, dirKey,
		"sub-skill command %q has an unparseable File %q — expected skills/<dir>/SKILL.md",
		commandId, spec.File)
	return spec, dirKey, spec.File
}

// ─── TestSubSkillFrontmatter ─────────────────────────────────────────────────

// subSkillFrontmatter is the YAML frontmatter contract the generator emits for
// every sub-skill (D5/SLICE-3). `name` makes the skill register as
// /pasture:<name>; `description` is CommandSpec.Description.
type subSkillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseLeadingFrontmatter extracts and parses the YAML frontmatter block that
// must lead a generated sub-skill SKILL.md. It requires the very first line to
// be the "---" fence and parses up to the closing "---" fence.
func parseLeadingFrontmatter(t *testing.T, content, label string) subSkillFrontmatter {
	t.Helper()

	require.True(t, strings.HasPrefix(content, "---\n"),
		"%s: generated output must begin with a YAML frontmatter fence '---' so the "+
			"skill registers as an invocable /pasture:* command; got start:\n%s",
		label, truncate(content, 120))

	// Find the closing fence: the next line that is exactly "---".
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	require.GreaterOrEqual(t, end, 0,
		"%s: frontmatter has no closing '---' fence — the block is malformed", label)
	block := rest[:end]

	var fm subSkillFrontmatter
	require.NoError(t, yaml.Unmarshal([]byte(block), &fm),
		"%s: frontmatter is not valid YAML:\n%s", label, block)
	return fm
}

// TestSubSkillFrontmatter verifies the D5/SLICE-3 frontmatter contract for ALL
// 24 generator-driven sub-skills: generating each from its on-disk SKILL.md
// produces a leading YAML frontmatter block whose `name` equals the skill
// directory key AND whose `description` equals CommandSpec.Description. This is
// what makes each sub-skill register as an invocable /pasture:<name> command.
func TestSubSkillFrontmatter(t *testing.T) {
	root := repoRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	require.Len(t, allSubSkillCommandIds, 24,
		"expected exactly 24 generator-driven sub-skills (mirrors commandSkillDirs); "+
			"update the L3 table if the sub-skill set changed")

	for _, commandId := range allSubSkillCommandIds {
		commandId := commandId
		t.Run(commandId, func(t *testing.T) {
			t.Parallel()

			spec, dirKey, relPath := subSkillCommand(t, commandId)
			skillPath := filepath.Join(root, relPath)

			generated, err := codegen.GenerateSubSkill(commandId, skillPath, figuresDir, opts)
			require.NoError(t, err,
				"GenerateSubSkill failed for command %q (%q)", commandId, relPath)

			fm := parseLeadingFrontmatter(t, generated, commandId)

			assert.Equal(t, dirKey, fm.Name,
				"sub-skill %q frontmatter `name` must equal its directory key %q so it "+
					"registers as /pasture:%s — got %q", commandId, dirKey, dirKey, fm.Name)
			assert.Equal(t, spec.Description, fm.Description,
				"sub-skill %q frontmatter `description` must equal CommandSpec.Description — "+
					"got %q, want %q", commandId, fm.Description, spec.Description)
		})
	}
}

// ─── TestAllSkillsFrontmatter ────────────────────────────────────────────────

// TestAllSkillsFrontmatter is a durability regression guard: it reads every
// skills/*/SKILL.md on disk (the full set of 31) and asserts that each file
// has a parseable YAML frontmatter block with a non-empty `name` and
// `description`. This catches any skill silently shipping without frontmatter
// — a skill without frontmatter does not register as an invocable /pasture:*
// command in Claude Code.
//
// Coverage:
//   - 5 role skills (supervisor, worker, reviewer, architect, epoch) — generated by
//     skill.go.tmpl (dropPrefix=true).
//   - 24 command sub-skills (commandSkillDirs in harness.go) — generated
//     by skill_sub.go.tmpl (dropPrefix=true, D5/SLICE-3).
//   - 2 hand-authored skills (protocol, install-cli) — no BEGIN/END markers;
//     frontmatter is stable and maintained by hand.
//
// Total: 31 files, matching the full skills/ tree.
func TestAllSkillsFrontmatter(t *testing.T) {
	root := repoRoot(t)

	glob := filepath.Join(root, "skills", "*", "SKILL.md")
	skillFiles, err := filepath.Glob(glob)
	require.NoError(t, err, "cannot glob %q", glob)
	require.Len(t, skillFiles, 31,
		"expected exactly 31 on-disk SKILL.md files; got %d — "+
			"update this count if skills are added or removed", len(skillFiles))

	for _, abs := range skillFiles {
		abs := abs
		// Derive a readable label: "skills/<dir>/SKILL.md"
		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil {
			rel = abs
		}
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			raw, readErr := os.ReadFile(abs)
			require.NoError(t, readErr,
				"TestAllSkillsFrontmatter: cannot read %q — "+
					"ensure the file exists and is readable", abs)

			fm := parseLeadingFrontmatter(t, string(raw), rel)

			assert.NotEmpty(t, fm.Name,
				"SKILL.md %q has an empty frontmatter `name` — "+
					"every skill must declare a non-empty name so it registers as "+
					"/pasture:<name> in Claude Code; "+
					"fix: add 'name: <skill-dir-key>' to the frontmatter block at the top of the file",
				rel)
			assert.NotEmpty(t, fm.Description,
				"SKILL.md %q has an empty frontmatter `description` — "+
					"every skill must declare a non-empty description for the Claude Code "+
					"skill picker; "+
					"fix: add 'description: <one-line description>' to the frontmatter block",
				rel)
		})
	}
}

// TestGenerateIdempotent verifies that running the code generator on the
// checked-in SKILL.md files produces identical output (zero diff).
//
// It table-drives ALL 29 generator-managed SKILL.md files (5 roles +
// 24 sub-skills). The remaining 2 on-disk skills (protocol, install-cli) are
// hand-authored and marker-less, so they are not generated and not asserted
// here.
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

	// ─── Role skills (5) ─────────────────────────────────────────────────

	for _, tc := range allRoleSkills {
		tc := tc
		t.Run("role/"+tc.name, func(t *testing.T) {
			t.Parallel()

			file := filepath.Join("skills", tc.name, "SKILL.md")
			skillPath := filepath.Join(root, file)
			onDisk, err := os.ReadFile(skillPath)
			require.NoError(t, err,
				"cannot read on-disk SKILL.md at %q — "+
					"ensure the file exists in the repository", skillPath)

			generated, err := codegen.GenerateSkill(tc.roleId, skillPath, figuresDir, opts)
			require.NoError(t, err,
				"GenerateSkill failed for role %q with skill file %q", tc.roleId, skillPath)

			assert.Equal(t, string(onDisk), generated,
				"GenerateSkill output for role %q differs from on-disk %q — "+
					"run 'go generate ./internal/codegen/...' to regenerate, then commit the updated file",
				tc.roleId, file)
		})
	}

	// ─── Sub-skills (24) ─────────────────────────────────────────────────

	for _, commandId := range allSubSkillCommandIds {
		commandId := commandId
		t.Run("sub-skill/"+commandId, func(t *testing.T) {
			t.Parallel()

			_, _, relPath := subSkillCommand(t, commandId)
			skillPath := filepath.Join(root, relPath)
			onDisk, err := os.ReadFile(skillPath)
			require.NoError(t, err,
				"cannot read on-disk SKILL.md at %q — "+
					"ensure the file exists in the repository", skillPath)

			generated, err := codegen.GenerateSubSkill(commandId, skillPath, figuresDir, opts)
			require.NoError(t, err,
				"GenerateSubSkill failed for command %q with skill file %q", commandId, skillPath)

			assert.Equal(t, string(onDisk), generated,
				"GenerateSubSkill output for command %q differs from on-disk %q — "+
					"run 'go generate ./internal/codegen/...' to regenerate, then commit the updated file",
				commandId, relPath)
		})
	}
}

// ─── TestGenerateSkill_BodyInsideMarkers ─────────────────────────────────────

// TestGenerateSkill_BodyInsideMarkers verifies the unified single-pass pipeline:
// after GenerateSkill runs, ALL body content (preamble, sections, recipes) is
// rendered INSIDE the BEGIN/END markers, and NOTHING appears after END.
//
// This test registers a temporary SkillBody entry so it exercises the body
// path without depending on real content in SkillBodySpecs.
func TestGenerateSkill_BodyInsideMarkers(t *testing.T) {
	// Register a test body under a sentinel key that matches the worker role.
	testBody := codegen.SkillBody{
		Preamble: "Test preamble paragraph.",
		Sections: []codegen.ProseSection{
			{
				Id:      "test-section",
				Title:   "Test Section",
				Content: "Test section content.",
			},
		},
		Recipes: []codegen.RecipeBlock{
			{
				Id:          "test-recipe",
				Title:       "Test Recipe",
				Description: "Test recipe description.",
				Lang:        "bash",
				Code:        "echo hello",
			},
		},
		Behaviors: []codegen.BehaviorSpec{
			{
				Id:        "test-behavior",
				Given:     "test given",
				When:      "test when",
				Then:      "test then",
				ShouldNot: "test should not",
			},
		},
	}
	withBodySpec(t, string(protocol.RoleWorker), testBody)

	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
	require.NoError(t, err, "GenerateSkill with registered body should not error")

	// Both markers must be present.
	assert.Contains(t, result, codegen.GeneratedBegin,
		"BEGIN marker must be present after generation")
	assert.Contains(t, result, codegen.GeneratedEnd,
		"END marker must be present after generation")

	// The generated header content (role ID) must be present.
	assert.Contains(t, result, "worker",
		"generated header content (role ID) must be in the output")

	// Body content must be INSIDE the markers (between BEGIN and END).
	beginIdx := strings.Index(result, codegen.GeneratedBegin)
	endIdx := strings.Index(result, codegen.GeneratedEnd)
	require.Greater(t, endIdx, beginIdx, "END must come after BEGIN")
	markerRegion := result[beginIdx:endIdx]
	assert.Contains(t, markerRegion, "Test Section",
		"body section title must appear INSIDE the marker region (between BEGIN and END)")
	assert.Contains(t, markerRegion, "Test preamble paragraph.",
		"body preamble must appear INSIDE the marker region (between BEGIN and END)")
	assert.Contains(t, markerRegion, "Test Recipe",
		"body recipe title must appear INSIDE the marker region (between BEGIN and END)")
	assert.Contains(t, markerRegion, "echo hello",
		"body recipe code must appear INSIDE the marker region (between BEGIN and END)")
	assert.Contains(t, markerRegion, "test given",
		"body behavior must appear INSIDE the marker region (between BEGIN and END)")

	// R3: NOTHING after END marker.
	afterEnd := result[endIdx+len(codegen.GeneratedEnd):]
	assert.Equal(t, strings.TrimSpace(afterEnd), "",
		"with a body spec, nothing should appear after the END marker (R3)")
}

// TestGenerateSkill_NoBody_HeaderOnly verifies that when no SkillBody is
// registered for a role, GenerateSkill produces output with no body content
// and nothing after END.
func TestGenerateSkill_NoBody_HeaderOnly(t *testing.T) {
	// Suppress body spec for worker to test header-only path.
	suppressBodySpec(t, string(protocol.RoleWorker))

	skillPath := writeSkillFile(t, skillFileWithMarkers())
	opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

	result, err := codegen.GenerateSkill(protocol.RoleWorker, skillPath, "", opts)
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

// truncate returns the first n bytes of s, for use in error messages.
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

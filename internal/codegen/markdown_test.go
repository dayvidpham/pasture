package codegen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── TestValidateSkillStructure ─────────────────────────────────────────────

func TestValidateSkillStructure(t *testing.T) {
	t.Run("valid hierarchy passes", func(t *testing.T) {
		md := []byte("# Title\n\n## Section A\n\nContent A.\n\n### Sub A1\n\nContent A1.\n\n## Section B\n\nContent B.\n")
		err := codegen.ValidateSkillStructure(md)
		assert.NoError(t, err, "valid heading hierarchy should pass validation")
	})

	t.Run("duplicate H2 titles fails", func(t *testing.T) {
		md := []byte("# Title\n\n## Section A\n\nFirst.\n\n## Section A\n\nDuplicate.\n")
		err := codegen.ValidateSkillStructure(md)
		require.Error(t, err, "duplicate H2 titles should fail")

		var structErr *codegen.SkillStructureError
		require.ErrorAs(t, err, &structErr,
			"error should be *SkillStructureError; got: %T", err)
		assert.Len(t, structErr.Problems, 1,
			"should report exactly 1 problem for duplicate H2 titles")
		assert.Contains(t, structErr.Problems[0], "duplicate H2",
			"problem should mention 'duplicate H2'")
		assert.Contains(t, structErr.Problems[0], "Section A",
			"problem should mention the duplicate title")
	})

	t.Run("orphan H3 before any H1 or H2 fails", func(t *testing.T) {
		md := []byte("### Orphan Section\n\nContent.\n\n## Section A\n\nContent.\n")
		err := codegen.ValidateSkillStructure(md)
		require.Error(t, err, "orphan H3 before any parent heading should fail")

		var structErr *codegen.SkillStructureError
		require.ErrorAs(t, err, &structErr)
		assert.Len(t, structErr.Problems, 1,
			"should report exactly 1 problem for orphan H3")
		assert.Contains(t, structErr.Problems[0], "orphan H3",
			"problem should mention 'orphan H3'")
		assert.Contains(t, structErr.Problems[0], "Orphan Section",
			"problem should mention the orphan heading title")
	})

	t.Run("H3 under H1 is valid (sub-skill pattern)", func(t *testing.T) {
		// Sub-skills use H1 → H3 (skipping H2) for figure headings.
		md := []byte("# Sub-Skill Title\n\n### Figure Heading\n\nFigure content.\n\n## Section\n\nBody content.\n")
		err := codegen.ValidateSkillStructure(md)
		assert.NoError(t, err, "H3 under H1 should be valid (sub-skill pattern)")
	})

	t.Run("empty markdown passes gracefully", func(t *testing.T) {
		err := codegen.ValidateSkillStructure([]byte(""))
		assert.NoError(t, err, "empty markdown should be valid")
	})

	t.Run("whitespace-only markdown passes gracefully", func(t *testing.T) {
		err := codegen.ValidateSkillStructure([]byte("   \n\n  \n"))
		assert.NoError(t, err, "whitespace-only markdown should be valid")
	})

	t.Run("multiple problems reported together", func(t *testing.T) {
		// Orphan H3 (no parent) + duplicate H2 titles
		md := []byte("### Orphan\n\n## Section A\n\nContent.\n\n## Section A\n\nDupe.\n")
		err := codegen.ValidateSkillStructure(md)
		require.Error(t, err, "multiple problems should fail")

		var structErr *codegen.SkillStructureError
		require.ErrorAs(t, err, &structErr)
		assert.Len(t, structErr.Problems, 2,
			"should report both orphan H3 and duplicate H2 problems")
	})
}

// ─── TestExtractSection ─────────────────────────────────────────────────────

func TestExtractSection(t *testing.T) {
	t.Run("extracts known H2 section", func(t *testing.T) {
		md := []byte("# Title\n\n## Overview\n\nThis is the overview.\n\n## Details\n\nSome details.\n")
		content, err := codegen.ExtractSection(md, "Overview")
		require.NoError(t, err)
		assert.Contains(t, string(content), "This is the overview.",
			"extracted section should contain the section content")
		assert.NotContains(t, string(content), "Some details.",
			"extracted section should not contain content from the next section")
	})

	t.Run("returns error for missing section", func(t *testing.T) {
		md := []byte("# Title\n\n## Overview\n\nContent.\n")
		_, err := codegen.ExtractSection(md, "Nonexistent")
		require.Error(t, err, "missing section should return error")
		assert.Contains(t, err.Error(), "Nonexistent",
			"error should mention the missing heading title")
		assert.Contains(t, err.Error(), "not found",
			"error should say 'not found'")
	})

	t.Run("last section extends to EOF", func(t *testing.T) {
		md := []byte("# Title\n\n## First\n\nContent A.\n\n## Last Section\n\nThis extends to the end.\n")
		content, err := codegen.ExtractSection(md, "Last Section")
		require.NoError(t, err)
		assert.Contains(t, string(content), "This extends to the end.",
			"last section content should extend to EOF")
	})

	t.Run("handles nested subsections under H2", func(t *testing.T) {
		md := []byte("# Title\n\n## Parent\n\nParent content.\n\n### Child\n\nChild content.\n\n## Next\n\nNext content.\n")
		content, err := codegen.ExtractSection(md, "Parent")
		require.NoError(t, err)
		assert.Contains(t, string(content), "Parent content.",
			"should include parent content")
		assert.Contains(t, string(content), "Child content.",
			"should include nested H3 content within the H2 section")
		assert.NotContains(t, string(content), "Next content.",
			"should not include content from the next H2 section")
	})

	t.Run("heading-level ambiguity: H2 vs H3 same title returns H2", func(t *testing.T) {
		md := []byte("# Title\n\n## Shared Title\n\nH2 content here.\n\n### Sub\n\nSub content.\n\n### Shared Title\n\nH3 content here.\n\n## Other\n\nOther content.\n")
		content, err := codegen.ExtractSection(md, "Shared Title")
		require.NoError(t, err)
		assert.Contains(t, string(content), "H2 content here.",
			"should return H2 match content when same title appears at H2 and H3")
		// The H2 section includes everything until the next H2 ("Other"),
		// so it should also include the nested H3 content.
		assert.Contains(t, string(content), "H3 content here.",
			"H2 section should include nested content up to next H2")
	})

	t.Run("extracts H1 section", func(t *testing.T) {
		md := []byte("# Main Title\n\nIntro content.\n\n## Section\n\nSection content.\n")
		content, err := codegen.ExtractSection(md, "Main Title")
		require.NoError(t, err)
		assert.Contains(t, string(content), "Intro content.",
			"should extract H1 section content")
	})

	// ─── Integration test: ExtractSection on real GenerateSkill output ────

	t.Run("integration: extract section from real GenerateSkill output", func(t *testing.T) {
		root := repoRoot(t)
		figuresDir := filepath.Join(root, "skills", "protocol", "figures")

		// Generate the supervisor skill (it has rich content with many sections).
		skillPath := writeSkillFile(t, skillFileWithMarkers())
		opts := codegen.GenerateOptions{Diff: false, Write: false, Init: false}

		generated, err := codegen.GenerateSkill(types.RoleSupervisor, skillPath, figuresDir, opts)
		require.NoError(t, err, "GenerateSkill should succeed for supervisor")

		// Extract the "Constraints (Given/When/Then/Should Not)" section.
		content, err := codegen.ExtractSection([]byte(generated), "Constraints (Given/When/Then/Should Not)")
		require.NoError(t, err,
			"ExtractSection should find 'Constraints' section in generated supervisor output")
		assert.True(t, len(content) > 0,
			"extracted Constraints section should have non-empty content")

		// The constraints section should contain known constraint IDs.
		contentStr := string(content)
		assert.Contains(t, contentStr, "C-supervisor-no-impl",
			"Constraints section should contain supervisor-specific constraint")

		// Also extract "Handoffs" section.
		handoffs, err := codegen.ExtractSection([]byte(generated), "Handoffs")
		require.NoError(t, err, "ExtractSection should find 'Handoffs' section")
		assert.True(t, len(handoffs) > 0,
			"Handoffs section should have non-empty content")

		// Verify that missing sections return errors on real output.
		_, err = codegen.ExtractSection([]byte(generated), "This Section Does Not Exist")
		require.Error(t, err, "missing section in real output should return error")
		assert.Contains(t, strings.ToLower(err.Error()), "not found")
	})
}

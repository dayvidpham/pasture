package scan_test

import (
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCandidateAccessorsAndValidity(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "# A\n\nCall Skill(/pasture:worker) now.\n")

	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 1)
	candidate := candidates[0]

	assert.True(t, candidate.IsValid())
	assert.Equal(t, scan.PatternSkillInvocation, candidate.Pattern())
	assert.Equal(t, "Skill(/", candidate.Snippet())
	assert.Equal(t, "Call Skill(/pasture:worker) now.", candidate.ContentWindow())
	assert.Equal(t, "Paragraph", candidate.ASTNode())
	assert.Equal(t, "skills/a/SKILL.md", candidate.Location().Owner())
	assert.Equal(t, "skills/a/SKILL.md", candidate.Location().File())
	assert.Equal(t, "A", candidate.Location().Section())
}

// TestCandidateSectionDefaultsToBodyBeforeAnyHeading proves a candidate
// found before the document's first heading is reported under the "body"
// default section, never an empty or nil section string.
func TestCandidateSectionDefaultsToBodyBeforeAnyHeading(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "Call Skill(/pasture:worker) before any heading exists.\n\n# Later Heading\n")

	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 1)
	assert.Equal(t, "body", candidates[0].Location().Section())
}

// TestPatternRegistryRequiresCallSyntaxNotBareMentions proves the closed
// pattern registry matches call-like syntax (e.g. "Skill(/") and not every
// prose mention of a construct's bare name — a mention with no following
// call-prefix (e.g. discussing "TeamCreate and SendMessage" together, or a
// heading that merely names "TeamCreate") is not itself candidate harness
// syntax and must not be reported.
func TestPatternRegistryRequiresCallSyntaxNotBareMentions(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"),
		"# TeamCreate: SendMessage Assignment\n\n"+
			"When workers are spawned via TeamCreate, they receive context through SendMessage.\n",
	)

	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	assert.Empty(t, candidates, "a bare mention of TeamCreate/SendMessage with no call-prefix must not be reported as candidate harness syntax")
}

// TestWithinNodeCandidateOrderFollowsSourceRangeNotRegistryOrder proves two
// different patterns matching inside one node are reported in source
// (range) order, not pattern-registry order: matchPatterns previously
// appended matches pattern-by-pattern (registry order: TeamCreate,
// SendMessage, Skill, AskUserQuestion), so a block with SendMessage(
// physically before TeamCreate( still reported TeamCreate first.
func TestWithinNodeCandidateOrderFollowsSourceRangeNotRegistryOrder(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"),
		"# A\n\n```text\nSendMessage({ recipient: \"worker-1\" })\nTeamCreate({ team_name: \"epoch\" })\n```\n",
	)

	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 2)
	assert.Equal(t, scan.PatternSendMessage, candidates[0].Pattern(), "SendMessage( appears first in source and must be reported first")
	assert.Equal(t, scan.PatternTeamCreate, candidates[1].Pattern())
	assert.Less(t, candidates[0].Location().Range().Start, candidates[1].Location().Range().Start)
}

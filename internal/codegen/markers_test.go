// Package codegen_test contains black-box tests for the codegen package.
//
// FindMarkerPositions tests are driven by a YAML fixture so that new cases
// can be added without touching Go code. ReplaceMarkerRegion and
// PrependMarkers are tested inline to cover the two prefix modes precisely.
package codegen_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fixture types ────────────────────────────────────────────────────────────

// markerCase mirrors one entry in testdata/markers.yaml.
type markerCase struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Input       string `yaml:"input"`
	WantBegin   int    `yaml:"want_begin"`
	WantEnd     int    `yaml:"want_end"`
	WantError   string `yaml:"want_error"` // substring to match; "" means no error
}

// markerSuite is the top-level structure of testdata/markers.yaml.
type markerSuite struct {
	Cases []markerCase `yaml:"cases"`
}

// ─── FindMarkerPositions: fixture-driven ──────────────────────────────────────

// TestFindMarkerPositions loads all cases from testdata/markers.yaml and
// verifies that FindMarkerPositions returns the expected indices or error
// for each case.
func TestFindMarkerPositions(t *testing.T) {
	var suite markerSuite
	testutil.LoadFixtures(t, testutil.CodegenMarkers, &suite)

	require.NotEmpty(t, suite.Cases,
		"markers.yaml must contain at least one test case")

	for _, tc := range suite.Cases {
		tc := tc // capture for parallel sub-test
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			// Convert the input string to the []string representation used by
			// FindMarkerPositions (splitlines with keepends=true semantics).
			lines := splitKeepEnds(tc.Input)

			begin, end, err := codegen.FindMarkerPositions(lines, "test-file.md")

			if tc.WantError != "" {
				// Error expected — verify we got one and the message matches.
				require.Error(t, err,
					"case %q: expected an error containing %q but got nil",
					tc.Name, tc.WantError)
				assert.Contains(t, err.Error(), tc.WantError,
					"case %q: error message should contain %q; got: %s",
					tc.Name, tc.WantError, err.Error())

				// Also assert the error is a *MarkerError so callers can type-assert.
				var me *codegen.MarkerError
				assert.ErrorAs(t, err, &me,
					"case %q: error must be *MarkerError", tc.Name)
				return
			}

			// No error expected.
			require.NoError(t, err,
				"case %q: unexpected error: %v", tc.Name, err)
			assert.Equal(t, tc.WantBegin, begin,
				"case %q: wrong begin index", tc.Name)
			assert.Equal(t, tc.WantEnd, end,
				"case %q: wrong end index", tc.Name)
		})
	}
}

// TestFindMarkerPositions_PathInError verifies that the path argument appears
// in the error message so users know which file to fix.
func TestFindMarkerPositions_PathInError(t *testing.T) {
	lines := []string{"no markers here\n"}
	_, _, err := codegen.FindMarkerPositions(lines, "skills/worker/SKILL.md")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills/worker/SKILL.md",
		"error message should contain the path argument")
}

// ─── HasMarkers ───────────────────────────────────────────────────────────────

func TestHasMarkers_BothPresent(t *testing.T) {
	content := codegen.GeneratedBegin + "\nsome content\n" + codegen.GeneratedEnd
	assert.True(t, codegen.HasMarkers(content))
}

func TestHasMarkers_OnlyBegin(t *testing.T) {
	assert.False(t, codegen.HasMarkers(codegen.GeneratedBegin+"\n"))
}

func TestHasMarkers_OnlyEnd(t *testing.T) {
	assert.False(t, codegen.HasMarkers(codegen.GeneratedEnd+"\n"))
}

func TestHasMarkers_NeitherPresent(t *testing.T) {
	assert.False(t, codegen.HasMarkers("plain text without markers"))
}

// ─── ReplaceMarkerRegion ──────────────────────────────────────────────────────

// rendered is a minimal rendered header (including the markers themselves,
// as the Python templates emit them) used in ReplaceMarkerRegion tests.
const rendered = codegen.GeneratedBegin + "\nnew generated content\n" + codegen.GeneratedEnd + "\n"

// TestReplaceMarkerRegion_DropPrefix verifies that when dropPrefix=true,
// everything before the BEGIN marker is discarded and the hand-authored body
// below END is preserved.
//
// This mode mirrors generate_skill() in gen_skills.py (template owns
// frontmatter).
func TestReplaceMarkerRegion_DropPrefix(t *testing.T) {
	old := "---\ntitle: Worker\n---\n\n" +
		codegen.GeneratedBegin + "\nold generated\n" + codegen.GeneratedEnd + "\n" +
		"\nhand-authored body\n"

	got, err := codegen.ReplaceMarkerRegion(old, rendered, true)
	require.NoError(t, err)

	// Prefix (frontmatter) must be gone.
	assert.NotContains(t, got, "title: Worker",
		"drop-prefix mode must discard content before BEGIN")
	// Generated content replaced.
	assert.Contains(t, got, "new generated content")
	assert.NotContains(t, got, "old generated")
	// Body preserved.
	assert.Contains(t, got, "hand-authored body",
		"body below END must be preserved")
}

// TestReplaceMarkerRegion_PreservePrefix verifies that when dropPrefix=false,
// everything before the BEGIN marker is kept and the hand-authored body below
// END is preserved.
//
// This mode mirrors generate_sub_skill() in gen_skills.py (h1 is
// hand-authored).
func TestReplaceMarkerRegion_PreservePrefix(t *testing.T) {
	old := "# Worker\n\n" +
		codegen.GeneratedBegin + "\nold generated\n" + codegen.GeneratedEnd + "\n" +
		"\nhand-authored body\n"

	got, err := codegen.ReplaceMarkerRegion(old, rendered, false)
	require.NoError(t, err)

	// Prefix (h1 heading) must be preserved.
	assert.True(t, strings.HasPrefix(got, "# Worker\n"),
		"preserve-prefix mode must keep content before BEGIN; got: %q", got)
	// Generated content replaced.
	assert.Contains(t, got, "new generated content")
	assert.NotContains(t, got, "old generated")
	// Body preserved.
	assert.Contains(t, got, "hand-authored body",
		"body below END must be preserved")
}

// TestReplaceMarkerRegion_NoBodyAfterEnd verifies that ReplaceMarkerRegion
// works correctly when there is no hand-authored body after END.
func TestReplaceMarkerRegion_NoBodyAfterEnd(t *testing.T) {
	old := codegen.GeneratedBegin + "\nold content\n" + codegen.GeneratedEnd + "\n"

	got, err := codegen.ReplaceMarkerRegion(old, rendered, true)
	require.NoError(t, err)
	assert.Equal(t, rendered, got,
		"with no body, result should be exactly the rendered header")
}

// TestReplaceMarkerRegion_MalformedReturnsError verifies that a missing
// marker in oldContent is propagated as a *MarkerError.
func TestReplaceMarkerRegion_MalformedReturnsError(t *testing.T) {
	_, err := codegen.ReplaceMarkerRegion("no markers here", rendered, false)
	require.Error(t, err)

	var me *codegen.MarkerError
	assert.ErrorAs(t, err, &me,
		"error from malformed oldContent must be *MarkerError")
}

// TestReplaceMarkerRegion_RenderedMissingNewline verifies that rendered
// strings without a trailing newline are handled (newline appended internally).
func TestReplaceMarkerRegion_RenderedMissingNewline(t *testing.T) {
	old := codegen.GeneratedBegin + "\nold\n" + codegen.GeneratedEnd + "\nbody\n"
	// Strip trailing newline from rendered.
	renderedNoNL := strings.TrimRight(rendered, "\n")

	got, err := codegen.ReplaceMarkerRegion(old, renderedNoNL, true)
	require.NoError(t, err)

	// Must still contain the body on a fresh line (not concatenated).
	assert.Contains(t, got, "body\n",
		"body must appear on its own line even when rendered lacked trailing newline")
}

// ─── PrependMarkers ───────────────────────────────────────────────────────────

// TestPrependMarkers_NoExistingMarkers verifies that PrependMarkers adds the
// marker pair when the content has none.
func TestPrependMarkers_NoExistingMarkers(t *testing.T) {
	content := "# Some heading\n\nbody text\n"
	got := codegen.PrependMarkers(content)

	assert.True(t, strings.HasPrefix(got, codegen.GeneratedBegin),
		"PrependMarkers must start with GeneratedBegin; got: %q", got[:min(len(got), 80)])
	assert.Contains(t, got, codegen.GeneratedEnd)
	assert.Contains(t, got, content,
		"original content must be preserved after markers")
}

// TestPrependMarkers_AlreadyHasMarkers verifies that PrependMarkers is
// idempotent: content already containing both markers is returned unchanged.
func TestPrependMarkers_AlreadyHasMarkers(t *testing.T) {
	content := codegen.GeneratedBegin + "\ncontent\n" + codegen.GeneratedEnd + "\nbody\n"
	got := codegen.PrependMarkers(content)

	assert.Equal(t, content, got,
		"PrependMarkers must not modify content that already has both markers")
}

// TestPrependMarkers_ResultHasMarkers verifies that the result of
// PrependMarkers always passes HasMarkers.
func TestPrependMarkers_ResultHasMarkers(t *testing.T) {
	got := codegen.PrependMarkers("some text without markers")
	assert.True(t, codegen.HasMarkers(got),
		"PrependMarkers result must satisfy HasMarkers")
}

// TestPrependMarkers_InitModeRoundTrip verifies that --init mode works end-
// to-end: PrependMarkers on a bare file followed by FindMarkerPositions
// succeeds and returns the correct indices.
func TestPrependMarkers_InitModeRoundTrip(t *testing.T) {
	bare := "# Heading\n\nsome body\n"
	prepared := codegen.PrependMarkers(bare)

	lines := splitKeepEnds(prepared)
	begin, end, err := codegen.FindMarkerPositions(lines, "test.md")

	require.NoError(t, err, "PrependMarkers result must have valid marker positions")
	assert.Equal(t, 0, begin, "BEGIN must be the first line after PrependMarkers")
	assert.Equal(t, 1, end, "END must be the second line after PrependMarkers")
}

// ─── ReplaceBodyRegion ────────────────────────────────────────────────────────

// TestReplaceBodyRegion_EndMarkerPresent verifies that ReplaceBodyRegion
// replaces everything after the END marker with the supplied body.
func TestReplaceBodyRegion_EndMarkerPresent(t *testing.T) {
	header := codegen.GeneratedBegin + "\ngenerated header\n" + codegen.GeneratedEnd + "\n"
	existingBody := "\nold body content\n## Old Section\n"
	content := header + existingBody

	newBody := "## New Section\n\nFresh content.\n"
	got, err := codegen.ReplaceBodyRegion(content, newBody)
	require.NoError(t, err, "ReplaceBodyRegion should not error when END marker is present")

	// Header region must be preserved verbatim.
	assert.True(t, strings.HasPrefix(got, header),
		"ReplaceBodyRegion must preserve the header region (BEGIN..END+newline)\n"+
			"expected prefix: %q\ngot start: %q", header, got[:min(len(got), len(header)+20)])

	// New body must appear after the header.
	assert.Contains(t, got, newBody,
		"ReplaceBodyRegion must write the new body after the END marker")

	// Old body must be gone.
	assert.NotContains(t, got, "old body content",
		"ReplaceBodyRegion must replace (not append) the old body")
}

// TestReplaceBodyRegion_MissingEndMarker verifies that ReplaceBodyRegion
// returns a *MarkerError when the END marker is absent.
func TestReplaceBodyRegion_MissingEndMarker(t *testing.T) {
	content := codegen.GeneratedBegin + "\nsome generated content\n"
	// Note: no GeneratedEnd in content.

	_, err := codegen.ReplaceBodyRegion(content, "new body\n")
	require.Error(t, err, "ReplaceBodyRegion must error when END marker is missing")

	var me *codegen.MarkerError
	assert.ErrorAs(t, err, &me,
		"error must be *MarkerError when END marker is missing; got %T: %v", err, err)
	assert.Contains(t, me.Problem, "missing END marker",
		"MarkerError.Problem should describe the missing END marker")
}

// TestReplaceBodyRegion_PreservesHeader verifies that the header region
// (everything from file start through END marker line) is untouched.
func TestReplaceBodyRegion_PreservesHeader(t *testing.T) {
	frontmatter := "---\ntitle: Worker\n---\n\n# Worker Agent\n\n"
	markerBlock := codegen.GeneratedBegin + "\nRole: worker\n" + codegen.GeneratedEnd + "\n"
	content := frontmatter + markerBlock + "\nold body\n"

	got, err := codegen.ReplaceBodyRegion(content, "new body\n")
	require.NoError(t, err)

	// Everything up to and including END+newline must be preserved.
	wantPrefix := frontmatter + markerBlock
	assert.True(t, strings.HasPrefix(got, wantPrefix),
		"full header region (frontmatter + marker block) must be preserved\n"+
			"want prefix: %q\ngot start: %q",
		wantPrefix, got[:min(len(got), len(wantPrefix)+10)])
}

// TestReplaceBodyRegion_EmptyBody verifies that an empty rendered body
// is written correctly (header followed by a single blank line).
func TestReplaceBodyRegion_EmptyBody(t *testing.T) {
	header := codegen.GeneratedBegin + "\ncontent\n" + codegen.GeneratedEnd + "\n"
	content := header + "\nexisting body\n"

	got, err := codegen.ReplaceBodyRegion(content, "")
	require.NoError(t, err, "ReplaceBodyRegion must not error for empty body")

	// Header must still be present.
	assert.Contains(t, got, codegen.GeneratedEnd,
		"END marker must be preserved even when body is empty")

	// Old body must be gone.
	assert.NotContains(t, got, "existing body",
		"old body must be replaced even when new body is empty")
}

// ─── Constants ────────────────────────────────────────────────────────────────

// TestConstants_Values guards against accidental changes to the marker strings
// that would break existing SKILL.md files in the repository.
func TestConstants_Values(t *testing.T) {
	assert.Equal(t,
		"<!-- BEGIN GENERATED FROM aura schema -->",
		codegen.GeneratedBegin,
		"GeneratedBegin value must not change (breaks existing SKILL.md files)",
	)
	assert.Equal(t,
		"<!-- END GENERATED FROM aura schema -->",
		codegen.GeneratedEnd,
		"GeneratedEnd value must not change (breaks existing SKILL.md files)",
	)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// splitKeepEnds splits s into lines, retaining the trailing '\n' on each line.
// This mirrors Python's str.splitlines(keepends=True) behaviour and matches
// what FindMarkerPositions expects.
func splitKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	for {
		idx := strings.Index(s, "\n")
		if idx == -1 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:idx+1])
		s = s[idx+1:]
		if s == "" {
			break
		}
	}
	return lines
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

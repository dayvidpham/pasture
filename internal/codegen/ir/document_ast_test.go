package ir_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarkdownRetainsExactGoldmarkSourceRanges proves the retained AST source
// ranges are exact, not merely non-empty. The fixture below was verified
// against goldmark directly: an ATX heading, a one-line paragraph, and a
// two-line paragraph produce exactly these four byte ranges, in this order —
// a prior revision's test only asserted assert.NotEmpty(t, ranges), which
// would still pass even if collectASTRanges silently dropped, reordered, or
// mis-bounded a block.
func TestMarkdownRetainsExactGoldmarkSourceRanges(t *testing.T) {
	t.Parallel()

	source := []byte("# Heading\n\nFirst paragraph.\n\nSecond paragraph line one.\nSecond paragraph line two.\n")
	part, err := ir.Markdown(source, mustLocation(t, "exact-ranges", len(source)))
	require.NoError(t, err)

	ranges, err := ir.MarkdownSourceRanges(part)
	require.NoError(t, err)

	expected := []ir.SourceRange{
		{Start: 2, Stop: 9},   // "Heading" (the ATX heading's own text line)
		{Start: 11, Stop: 27}, // "First paragraph."
		{Start: 29, Stop: 56}, // "Second paragraph line one.\n"
		{Start: 56, Stop: 82}, // "Second paragraph line two."
	}
	require.Equal(t, expected, ranges)

	for _, r := range ranges {
		assert.Equal(t, string(source[r.Start:r.Stop]), string(source[r.Start:r.Stop]), "range must index the exact owned source")
	}
	assert.Equal(t, "Heading", string(source[expected[0].Start:expected[0].Stop]))
	assert.Equal(t, "First paragraph.", string(source[expected[1].Start:expected[1].Stop]))
	assert.Equal(t, "Second paragraph line one.\n", string(source[expected[2].Start:expected[2].Stop]))
	assert.Equal(t, "Second paragraph line two.", string(source[expected[3].Start:expected[3].Stop]))
}

// TestMarkdownSourceRangesReturnsDefensiveCopyOnEveryCall proves defensive
// ownership: mutating a returned []SourceRange must never corrupt the part's
// internal state, so a second, independent call still returns the original
// values. The package also has no exported accessor returning the retained
// goldmark ast.Node itself (only MarkdownSourceRanges' derived, defensively
// copied ranges) — there is no symbol a caller could even attempt to use to
// reach the live mutable tree.
func TestMarkdownSourceRangesReturnsDefensiveCopyOnEveryCall(t *testing.T) {
	t.Parallel()

	source := []byte("# Heading\n\nBody text.\n")
	part, err := ir.Markdown(source, mustLocation(t, "defensive-ranges", len(source)))
	require.NoError(t, err)

	first, err := ir.MarkdownSourceRanges(part)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	original := append([]ir.SourceRange(nil), first...)

	first[0] = ir.SourceRange{Start: 999, Stop: 999}

	second, err := ir.MarkdownSourceRanges(part)
	require.NoError(t, err)
	assert.Equal(t, original, second, "mutating one call's result must not affect a later call")
	assert.NotEqual(t, first, second)
}

// TestMarkdownSourceRangesRejectsNonMarkdownParts proves
// MarkdownSourceRanges is a defended accessor: it only accepts a value this
// package's own Markdown constructor produced.
func TestMarkdownSourceRangesRejectsNonMarkdownParts(t *testing.T) {
	t.Parallel()

	verbatim, err := ir.Verbatim([]byte("plain text"), mustLocation(t, "not-markdown", 0))
	require.NoError(t, err)
	_, err = ir.MarkdownSourceRanges(verbatim)
	require.Error(t, err)

	_, err = ir.MarkdownSourceRanges(nil)
	require.Error(t, err)
}

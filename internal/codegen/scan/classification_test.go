package scan_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
)

func TestClassificationsIsClosedAndValid(t *testing.T) {
	t.Parallel()

	classifications := scan.Classifications()
	assert.Len(t, classifications, 7)
	seen := make(map[scan.Classification]bool)
	for _, c := range classifications {
		assert.True(t, c.IsValid())
		assert.False(t, seen[c], "classification %q listed twice", c)
		seen[c] = true
	}
	assert.False(t, scan.Classification("not-a-real-classification").IsValid())
	assert.False(t, scan.Classification("").IsValid())

	// Mutating the returned slice must never corrupt later callers.
	classifications[0] = scan.Classification("corrupted")
	again := scan.Classifications()
	assert.NotEqual(t, scan.Classification("corrupted"), again[0])
}

func TestOwnerDispositionsIsClosedAndValid(t *testing.T) {
	t.Parallel()

	dispositions := scan.OwnerDispositions()
	assert.ElementsMatch(t, []scan.OwnerDisposition{scan.OwnerActive, scan.OwnerDead}, dispositions)
	for _, d := range dispositions {
		assert.True(t, d.IsValid())
	}
	assert.False(t, scan.OwnerDisposition("retired").IsValid())
}

func TestPatternIDsIsClosedAndValid(t *testing.T) {
	t.Parallel()

	patterns := scan.PatternIDs()
	assert.ElementsMatch(t, []scan.PatternID{
		scan.PatternTeamCreate,
		scan.PatternSendMessage,
		scan.PatternSkillInvocation,
		scan.PatternAskUserQuestion,
	}, patterns)
	for _, p := range patterns {
		assert.True(t, p.IsValid())
	}
	assert.False(t, scan.PatternID("unregistered_pattern").IsValid())
}

func TestCanonicalRootsIsClosedAndDefensive(t *testing.T) {
	t.Parallel()

	roots := scan.CanonicalRoots()
	assert.Equal(t, []string{"skills", "agents"}, roots)

	roots[0] = "corrupted"
	again := scan.CanonicalRoots()
	assert.Equal(t, []string{"skills", "agents"}, again)
}

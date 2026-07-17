package scan_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanOwnerCandidates is a small test helper wiring Discover+ScanCandidates
// (both production entrypoints) against a synthetic base directory whose
// files are all disposed active.
func scanOwnerCandidates(t testing.TB, base string, roots []string) []scan.Candidate {
	t.Helper()
	discovered, err := scan.Discover(base, roots)
	require.NoError(t, err)
	entries := make([]scan.OwnerEntry, 0, len(discovered))
	for _, path := range discovered {
		entries = append(entries, scan.OwnerEntry{Path: path, Disposition: scan.OwnerActive})
	}
	owners, err := scan.NewOwnerManifest(entries)
	require.NoError(t, err)
	candidates, err := scan.ScanCandidates(base, discovered, owners)
	require.NoError(t, err)
	return candidates
}

// TestClassifyKeysOnContentWindowNotBarePrefix proves two occurrences of the
// identical short Snippet ("Skill(/") on two different lines already get
// distinct classification keys from ContentWindow alone (no ordinal needed):
// keying on the bare matched prefix degenerated to (owner, pattern,
// encounter-order), which silently swapped classifications when occurrences
// were reordered.
func TestClassifyKeysOnContentWindowNotBarePrefix(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "dup", "SKILL.md"),
		"# Duplicate Skill Calls\n\n"+
			"Real invocation: Skill(/pasture:worker) first.\n\n"+
			"Doc example only: Skill(/pasture:worker) second.\n",
	)
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 2)
	for _, c := range candidates {
		assert.Equal(t, "Skill(/", c.Snippet(), "the narrow matched Snippet is identical across both occurrences")
	}
	assert.NotEqual(t, candidates[0].ContentWindow(), candidates[1].ContentWindow(),
		"ContentWindow (the enclosing line) must differ even though Snippet does not")

	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/dup/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Real invocation: Skill(/pasture:worker) first.", Section: "Duplicate Skill Calls", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/dup/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Doc example only: Skill(/pasture:worker) second.", Section: "Duplicate Skill Calls", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive},
	})
	require.NoError(t, err)

	inventory := scan.Classify(candidates, manifest)
	require.NoError(t, scan.RequireZeroUnclassified(inventory))
	require.NoError(t, scan.RequireNoOrphanedClassifications(inventory))
	assertClassificationByWindow(t, inventory,
		"Real invocation: Skill(/pasture:worker) first.", scan.ClassificationOrchestration,
		"Doc example only: Skill(/pasture:worker) second.", scan.ClassificationNeutralFalsePositive,
	)
}

// TestClassifyIsRobustToReorderingDistinctlyClassifiedOccurrences is the
// direct regression for an empirically-confirmed swap: two
// byte-identical-Snippet occurrences carrying DIFFERENT classifications,
// reordered in the source with the manifest held fixed, must keep each
// classification bound to its own physical content — never swap.
func TestClassifyIsRobustToReorderingDistinctlyClassifiedOccurrences(t *testing.T) {
	t.Parallel()

	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/dup/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Real invocation: Skill(/pasture:worker) first.", Section: "Duplicate Skill Calls", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/dup/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Doc example only: Skill(/pasture:worker) second.", Section: "Duplicate Skill Calls", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive},
	})
	require.NoError(t, err)

	classify := func(t *testing.T, content string) scan.Inventory {
		t.Helper()
		base := t.TempDir()
		mustWriteFile(t, filepath.Join(base, "skills", "dup", "SKILL.md"), content)
		candidates := scanOwnerCandidates(t, base, []string{"skills"})
		require.Len(t, candidates, 2)
		return scan.Classify(candidates, manifest)
	}

	original := classify(t, "# Duplicate Skill Calls\n\nReal invocation: Skill(/pasture:worker) first.\n\nDoc example only: Skill(/pasture:worker) second.\n")
	reordered := classify(t, "# Duplicate Skill Calls\n\nDoc example only: Skill(/pasture:worker) second.\n\nReal invocation: Skill(/pasture:worker) first.\n")

	for _, inv := range []scan.Inventory{original, reordered} {
		require.NoError(t, scan.RequireZeroUnclassified(inv))
		assertClassificationByWindow(t, inv,
			"Real invocation: Skill(/pasture:worker) first.", scan.ClassificationOrchestration,
			"Doc example only: Skill(/pasture:worker) second.", scan.ClassificationNeutralFalsePositive,
		)
	}
}

// TestClassifyOrdinalIsScopedPerSectionNotWholeFile proves ordinal is scoped
// to (owner, pattern, contentWindow, section) and not to whole-file encounter
// order. The discriminating construction uses one identical ContentWindow
// appearing once in Section A and twice in Section B, with Section A's
// occurrence classified differently from Section B's: under section-scoped
// ordinal, Section A's occurrence always starts counting fresh at its own
// ordinal 0 regardless of how many Section B occurrences of the same exact
// line precede or follow it in the file, so moving Section A before or after
// Section B must never change which classification each specific occurrence
// receives. A regression back to whole-file ordinal scoping would shift the
// global encounter-order number assigned to whichever section comes second,
// causing at least one occurrence to stop matching its manifest entry (and
// therefore become unclassified) in one of the two orderings — a prior
// version of this test used content confined entirely to one section, which
// could not actually distinguish section-scoped from whole-file ordinal.
func TestClassifyOrdinalIsScopedPerSectionNotWholeFile(t *testing.T) {
	t.Parallel()

	const sharedWindow = "Skill(/pasture:shared) example."
	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/sections/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: sharedWindow, Section: "Section A", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/sections/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: sharedWindow, Section: "Section B", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive},
		{Owner: "skills/sections/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: sharedWindow, Section: "Section B", Ordinal: 1, Classification: scan.ClassificationNeutralFalsePositive},
	})
	require.NoError(t, err)

	classify := func(t *testing.T, content string) scan.Inventory {
		t.Helper()
		base := t.TempDir()
		mustWriteFile(t, filepath.Join(base, "skills", "sections", "SKILL.md"), content)
		candidates := scanOwnerCandidates(t, base, []string{"skills"})
		require.Len(t, candidates, 3)
		return scan.Classify(candidates, manifest)
	}

	sectionAFirst := "# Section A\n\n" + sharedWindow + "\n\n# Section B\n\n" + sharedWindow + "\n\n" + sharedWindow + "\n"
	sectionBFirst := "# Section B\n\n" + sharedWindow + "\n\n" + sharedWindow + "\n\n# Section A\n\n" + sharedWindow + "\n"

	for _, content := range []string{sectionAFirst, sectionBFirst} {
		inventory := classify(t, content)
		require.NoError(t, scan.RequireZeroUnclassified(inventory), "a whole-file ordinal regression would leave at least one occurrence unclassified in one of the two section orderings")
		require.NoError(t, scan.RequireNoOrphanedClassifications(inventory))

		var sectionACount, sectionBCount int
		for _, classified := range inventory.Candidates() {
			switch classified.Candidate.Location().Section() {
			case "Section A":
				sectionACount++
				assert.Equal(t, scan.ClassificationOrchestration, classified.Classification,
					"Section A's occurrence must classify as orchestration regardless of which section comes first in the file")
			case "Section B":
				sectionBCount++
				assert.Equal(t, scan.ClassificationNeutralFalsePositive, classified.Classification,
					"every Section B occurrence must classify as neutral_false_positive regardless of which section comes first in the file")
			default:
				t.Fatalf("unexpected section %q", classified.Candidate.Location().Section())
			}
		}
		assert.Equal(t, 1, sectionACount)
		assert.Equal(t, 2, sectionBCount)
	}
}

// TestClassifyOrdinalWithinIdenticalScopeIsPositionalByDesign pins the one
// residual position-dependency ContentWindow/Section scoping does not
// remove (see ClassificationEntry's doc comment: "Ordinal is a last
// resort"): two manifest entries sharing the exact same (owner, pattern,
// ContentWindow, Section) but different Classification values can only be
// disambiguated by which physical occurrence Classify happens to encounter
// first within that section. Authoring two different classifications for
// byte-identical content under one heading is an inherently ambiguous
// authoring choice this package does not attempt to resolve; this test pins
// that current, documented behavior instead of leaving it unverified.
func TestClassifyOrdinalWithinIdenticalScopeIsPositionalByDesign(t *testing.T) {
	t.Parallel()

	const window = "Skill(/pasture:ambiguous) example."
	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/ambiguous/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: window, Section: "Notes", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/ambiguous/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: window, Section: "Notes", Ordinal: 1, Classification: scan.ClassificationNeutralFalsePositive},
	})
	require.NoError(t, err)

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "ambiguous", "SKILL.md"), "# Notes\n\n"+window+"\n\n"+window+"\n")
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 2)

	inventory := scan.Classify(candidates, manifest)
	require.NoError(t, scan.RequireZeroUnclassified(inventory))
	classified := inventory.Candidates()
	// The physical occurrence encountered first (in source order) receives
	// ordinal 0's classification; there is no content-level signal that
	// distinguishes the two identical occurrences, by design.
	assert.Equal(t, scan.ClassificationOrchestration, classified[0].Classification)
	assert.Equal(t, scan.ClassificationNeutralFalsePositive, classified[1].Classification)
}

func TestClassifyReportsUnclassifiedWithNoImplicitDefault(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "single", "SKILL.md"),
		"# Single Call\n\nCall Skill(/pasture:worker) once.\n",
	)
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 1)

	emptyManifest, err := scan.NewClassificationManifest(nil)
	require.NoError(t, err)

	inventory := scan.Classify(candidates, emptyManifest)
	assert.Equal(t, 1, inventory.UnclassifiedCount())
	classified := inventory.Candidates()
	require.Len(t, classified, 1)
	assert.False(t, classified[0].Classified)
	assert.Equal(t, scan.Classification(""), classified[0].Classification,
		"an unclassified candidate must never be silently assigned a real Classification value")

	err = scan.RequireZeroUnclassified(inventory)
	require.Error(t, err)
	assert.ErrorContains(t, err, "skills/single/SKILL.md")
	assert.ErrorContains(t, err, "unclassified")
	rng := classified[0].Candidate.Location().Range()
	assert.ErrorContains(t, err, fmt.Sprintf("[%d..%d]", rng.Start, rng.Stop),
		"the diagnostic must include the byte range its own doc comment promises")
}

func TestInventoryCountByClassification(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "matrix", "SKILL.md"),
		"# Matrix\n\n"+
			"Call Skill(/pasture:worker) here.\n\n"+
			"AskUserQuestion(questions: [1]) opens a survey.\n",
	)
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 2)

	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/matrix/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) here.", Section: "Matrix", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/matrix/SKILL.md", Pattern: scan.PatternAskUserQuestion, ContentWindow: "AskUserQuestion(questions: [1]) opens a survey.", Section: "Matrix", Ordinal: 0, Classification: scan.ClassificationUserDecision},
	})
	require.NoError(t, err)

	inventory := scan.Classify(candidates, manifest)
	assert.Equal(t, 1, inventory.CountByClassification(scan.ClassificationOrchestration))
	assert.Equal(t, 1, inventory.CountByClassification(scan.ClassificationUserDecision))
	assert.Equal(t, 0, inventory.CountByClassification(scan.ClassificationTaskEffect))
}

// TestRequireNoOrphanedClassificationsDetectsStaleEntries is the
// classification-manifest counterpart to TestReconcileOwners' stale-entry
// case: a manifest entry with an ordinal beyond the real occurrence count
// must fail the scan, not silently sit unused forever.
func TestRequireNoOrphanedClassificationsDetectsStaleEntries(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "# A\n\nCall Skill(/pasture:worker) now.\n")
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 1)

	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/a/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) now.", Section: "A", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/a/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) now.", Section: "A", Ordinal: 1, Classification: scan.ClassificationOrchestration}, // stale: only 1 real occurrence exists
	})
	require.NoError(t, err)

	inventory := scan.Classify(candidates, manifest)
	assert.NoError(t, scan.RequireZeroUnclassified(inventory), "the real candidate is still classified by ordinal 0; only the extra manifest entry is stale")

	orphaned := inventory.OrphanedClassifications()
	require.Len(t, orphaned, 1)
	assert.Equal(t, 1, orphaned[0].Ordinal)

	err = scan.RequireNoOrphanedClassifications(inventory)
	require.Error(t, err)
	assert.ErrorContains(t, err, "skills/a/SKILL.md")
	assert.ErrorContains(t, err, "ordinal 1")
}

func TestRequireNoOrphanedClassificationsPassesWhenEveryEntryMatches(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "# A\n\nCall Skill(/pasture:worker) now.\n")
	candidates := scanOwnerCandidates(t, base, []string{"skills"})
	require.Len(t, candidates, 1)

	manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
		{Owner: "skills/a/SKILL.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) now.", Section: "A", Ordinal: 0, Classification: scan.ClassificationOrchestration},
	})
	require.NoError(t, err)

	inventory := scan.Classify(candidates, manifest)
	assert.Empty(t, inventory.OrphanedClassifications())
	assert.NoError(t, scan.RequireNoOrphanedClassifications(inventory))
}

func assertClassificationByWindow(t *testing.T, inv scan.Inventory, windowA string, classA scan.Classification, windowB string, classB scan.Classification) {
	t.Helper()
	seen := map[string]scan.Classification{}
	for _, c := range inv.Candidates() {
		seen[c.Candidate.ContentWindow()] = c.Classification
	}
	assert.Equal(t, classA, seen[windowA], "content window %q", windowA)
	assert.Equal(t, classB, seen[windowB], "content window %q", windowB)
}

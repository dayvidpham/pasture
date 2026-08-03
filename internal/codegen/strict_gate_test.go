package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSyntheticTree materializes files (relative slash paths -> content)
// under a fresh temp directory and returns that base directory. It is the
// minimal source tree the strict gate scans in a test, standing in for the
// real skills/ and agents/ canonical roots.
func writeSyntheticTree(t *testing.T, files map[string]string) string {
	t.Helper()
	base := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(base, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return base
}

// classifyEveryCandidateAs scans base once with an empty classification
// manifest, then builds a classification manifest that classifies every
// candidate the scan actually found as c. It derives each entry's
// owner/pattern/content-window/section/ordinal from the real candidate so a
// test never hardcodes a fragile content-window string.
func classifyEveryCandidateAs(t *testing.T, base string, roots []string, owners scan.OwnerManifest, c scan.Classification) scan.ClassificationManifest {
	t.Helper()
	empty, err := scan.NewClassificationManifest(nil)
	require.NoError(t, err)
	inventory, err := scan.ScanWithManifests(base, roots, owners, empty)
	require.NoError(t, err)

	var entries []scan.ClassificationEntry
	for _, candidate := range inventory.Candidates() {
		loc := candidate.Candidate.Location()
		entries = append(entries, scan.ClassificationEntry{
			Owner:          loc.Owner(),
			Pattern:        candidate.Candidate.Pattern(),
			ContentWindow:  candidate.Candidate.ContentWindow(),
			Section:        loc.Section(),
			Ordinal:        candidate.Ordinal,
			Classification: c,
		})
	}
	manifest, err := scan.NewClassificationManifest(entries)
	require.NoError(t, err)
	return manifest
}

func activeOwners(t *testing.T, paths ...string) scan.OwnerManifest {
	t.Helper()
	entries := make([]scan.OwnerEntry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, scan.OwnerEntry{Path: p, Disposition: scan.OwnerActive})
	}
	manifest, err := scan.NewOwnerManifest(entries)
	require.NoError(t, err)
	return manifest
}

// TestRequireClassifiedSourcePassesAgainstCanonicalTree proves the strict
// rejection gate accepts this repository's real checked-in source: every
// harness-syntax candidate under skills/ and agents/ is classified (the
// zero-unclassified baseline pasture#42 enforces on every future generation).
func TestRequireClassifiedSourcePassesAgainstCanonicalTree(t *testing.T) {
	t.Parallel()
	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	require.NoError(t, codegen.RequireClassifiedSource(root),
		"the checked-in canonical source must contain zero unclassified candidates")
}

// TestCanonicalInventoryIsExhaustivelyCountedAndClassified proves the strict
// gate's precondition holds against the real source with no gaps: every one of
// the 55 discovered candidates is accounted for by exactly one classification
// (classified counts across the closed set plus the unclassified count equal
// the total), zero remain unclassified, and the Verbatim/TargetLiteral bypass
// counts are individually queryable — so those bypasses stay fragment-scoped,
// exhaustive, and counted rather than an unbounded escape hatch.
func TestCanonicalInventoryIsExhaustivelyCountedAndClassified(t *testing.T) {
	t.Parallel()
	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	inventory, err := scan.ScanCanonical(root)
	require.NoError(t, err)

	assert.Zero(t, inventory.UnclassifiedCount(), "the canonical inventory must contain zero unclassified candidates")

	total := inventory.UnclassifiedCount()
	for _, classification := range scan.Classifications() {
		// Every classification's count is queryable, including the
		// portable_verbatim and target_literal bypass counts.
		total += inventory.CountByClassification(classification)
	}
	assert.Equal(t, inventory.Len(), total,
		"every candidate must be accounted for by exactly one classification (the count is exhaustive)")
}

// TestStrictGateRejectsUnclassifiedCandidate proves the gate fails closed on
// a real, unclassified harness-syntax candidate and that its error is
// actionable: it names the abort-with-no-output guarantee, the owner, and the
// section so a maintainer can classify the candidate without re-running the
// scanner.
func TestStrictGateRejectsUnclassifiedCandidate(t *testing.T) {
	t.Parallel()
	roots := []string{"skills", "agents"}
	base := writeSyntheticTree(t, map[string]string{
		"skills/example/SKILL.md": "# Example\n\n## Team Spawn\n\nSpawn workers via TeamCreate({ team_name: \"epoch-impl\" }) now.\n",
		"agents/example.md":       "# Example Agent\n\nOrdinary prose with no harness syntax.\n",
	})
	owners := activeOwners(t, "skills/example/SKILL.md", "agents/example.md")
	empty, err := scan.NewClassificationManifest(nil)
	require.NoError(t, err)

	err = codegen.RequireClassifiedSourceWithManifests(base, roots, owners, empty)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "no partial output")
	assert.Contains(t, msg, "unclassified")
	assert.Contains(t, msg, "skills/example/SKILL.md")
	assert.Contains(t, msg, "Team Spawn")
}

// TestStrictGatePassesWhenEveryCandidateClassified proves the same tree
// passes once every candidate carries an explicit classification.
func TestStrictGatePassesWhenEveryCandidateClassified(t *testing.T) {
	t.Parallel()
	roots := []string{"skills", "agents"}
	base := writeSyntheticTree(t, map[string]string{
		"skills/example/SKILL.md": "# Example\n\n## Team Spawn\n\nSpawn workers via TeamCreate({ team_name: \"epoch-impl\" }) now.\n",
		"agents/example.md":       "# Example Agent\n\nOrdinary prose with no harness syntax.\n",
	})
	owners := activeOwners(t, "skills/example/SKILL.md", "agents/example.md")
	classifications := classifyEveryCandidateAs(t, base, roots, owners, scan.ClassificationOrchestration)

	require.NoError(t, codegen.RequireClassifiedSourceWithManifests(base, roots, owners, classifications))
}

// TestStrictGateRegressionFixtures is the pasture#42 gate-boundary regression
// suite: each row is a source fragment the gate must handle correctly before
// it may activate. Cases that introduce an unrecognized-but-real harness
// candidate must be rejected until classified; cases that contain only prose,
// an invented (non-registry) operation name, or a documentation example must
// not fabricate a candidate that would force a spurious migration.
func TestStrictGateRegressionFixtures(t *testing.T) {
	t.Parallel()
	roots := []string{"skills", "agents"}

	cases := []struct {
		name       string
		body       string
		wantReject bool
		// wantInMsg is asserted on the rejection error (only when wantReject).
		wantInMsg string
	}{
		{
			name:       "malformed TeamCreate arrow-task is a candidate and stays unclassified",
			body:       "# S\n\n## Malformed\n\nBroken example: TeamCreate({...}) -> task(({...}) should never be silently rewritten.\n",
			wantReject: true,
			wantInMsg:  "team_create",
		},
		{
			name:       "invented OpenCode operation name is not a registry pattern and yields no candidate",
			body:       "# S\n\n## Invented\n\nSome text mentions OpenCodeSpawn(worker) and TeamDissolve(team) which are not native syntax.\n",
			wantReject: false,
		},
		{
			name:       "ordinary prose and headings alone produce no candidate",
			body:       "# Heading\n\nA plain paragraph describing the workflow with no call syntax at all.\n",
			wantReject: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := writeSyntheticTree(t, map[string]string{
				"skills/regression/SKILL.md": tc.body,
				"agents/placeholder.md":      "# Placeholder\n\nNo syntax here.\n",
			})
			owners := activeOwners(t, "skills/regression/SKILL.md", "agents/placeholder.md")
			empty, err := scan.NewClassificationManifest(nil)
			require.NoError(t, err)

			err = codegen.RequireClassifiedSourceWithManifests(base, roots, owners, empty)
			if tc.wantReject {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "no partial output")
				if tc.wantInMsg != "" {
					assert.Contains(t, err.Error(), tc.wantInMsg)
				}
				return
			}
			require.NoError(t, err, "no real candidate exists, so the gate must not reject")
		})
	}
}

// TestStrictGateReportsScanFailureWithoutOutputGuarantee proves a scan-stage
// failure (here, a canonical root that is not a directory) is surfaced as a
// gate abort rather than a partial run.
func TestStrictGateReportsScanFailureWithoutOutputGuarantee(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// skills/ present as a directory, agents missing entirely.
	require.NoError(t, os.MkdirAll(filepath.Join(base, "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "skills", "SKILL.md"), []byte("# x\n"), 0o644))

	err := codegen.RequireClassifiedSourceWithManifests(base, []string{"skills", "agents"}, activeOwners(t), mustEmptyClassifications(t))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "aborted"), "gate must report the abort")
}

func mustEmptyClassifications(t *testing.T) scan.ClassificationManifest {
	t.Helper()
	m, err := scan.NewClassificationManifest(nil)
	require.NoError(t, err)
	return m
}

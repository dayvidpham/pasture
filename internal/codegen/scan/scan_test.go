package scan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func goldenFixtureRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "testdata", "golden")
}

// goldenOwnerManifest is the checked-in disposition for every file under
// testdata/golden: every owner is active except skills/dead-owner.md, which
// carries an explicit dead disposition and reason (pasture#47 forbids a
// silent skip).
func goldenOwnerManifest(t testing.TB) scan.OwnerManifest {
	t.Helper()
	manifest, err := scan.NewOwnerManifest([]scan.OwnerEntry{
		{Path: "agents/example.md", Disposition: scan.OwnerActive},
		{Path: "skills/classification-matrix.md", Disposition: scan.OwnerActive},
		{Path: "skills/codeblocks.md", Disposition: scan.OwnerActive},
		{Path: "skills/dead-owner.md", Disposition: scan.OwnerDead, Reason: "retained for audit only; golden fixture proving a dead owner is never parsed for candidates"},
		{Path: "skills/html.md", Disposition: scan.OwnerActive},
		{Path: "skills/prose.md", Disposition: scan.OwnerActive},
	})
	require.NoError(t, err)
	return manifest
}

// goldenClassificationManifest classifies every candidate testdata/golden's
// active owners produce, covering all seven Classification values, the
// malformed TeamCreate/task(( regression, and HTML block/inline raw HTML
// content. Keys are (owner, pattern, ContentWindow, Section, Ordinal) — see
// ClassificationEntry's doc comment.
func goldenClassificationManifest(t testing.TB) scan.ClassificationManifest {
	t.Helper()
	manifest, err := scan.NewClassificationManifest(goldenClassificationManifestEntries())
	require.NoError(t, err)
	return manifest
}

// goldenClassificationManifestEntries returns the raw entry slice
// goldenClassificationManifest validates and wraps; exposed separately so
// TestScanWithManifestsFailsOnClassificationManifestDrift can build a
// deliberately-drifted manifest (the golden set plus one stale entry)
// without duplicating the whole literal table.
func goldenClassificationManifestEntries() []scan.ClassificationEntry {
	return []scan.ClassificationEntry{
		// agents/example.md
		{Owner: "agents/example.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Start with Skill(/pasture:supervisor) to load role instructions.", Section: "Example Agent", Ordinal: 0, Classification: scan.ClassificationOrchestration},

		// skills/classification-matrix.md: one golden fixture demonstrating
		// every closed Classification value; each occurrence sits on its own
		// distinct line/section, so ContentWindow alone disambiguates them
		// (Ordinal 0 throughout).
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) to begin.", Section: "Orchestration Example", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternAskUserQuestion, ContentWindow: "AskUserQuestion(questions: [...]) opens the survey.", Section: "Decision Example", Ordinal: 0, Classification: scan.ClassificationUserDecision},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Skill(/pasture:task-effect-example) stands in for a future bd invocation pattern.", Section: "Task Effect Example", Ordinal: 0, Classification: scan.ClassificationTaskEffect},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Skill(/pasture:process-effect-example) stands in for a future git invocation pattern.", Section: "Process Effect Example", Ordinal: 0, Classification: scan.ClassificationProcessEffect},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Skill(/pasture:verbatim-example) is kept exactly as historical documentation shows.", Section: "Verbatim Example", Ordinal: 0, Classification: scan.ClassificationPortableVerbatim},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Skill(/pasture:target-literal-example) is the harness-pinned literal form.", Section: "Target Literal Example", Ordinal: 0, Classification: scan.ClassificationTargetLiteral},
		{Owner: "skills/classification-matrix.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "The word Skill(/pasture:neutral-example) is merely named here, not invoked.", Section: "Neutral False Positive Example", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive},

		// skills/codeblocks.md: prompt, orchestration, and the malformed
		// regression. The two team_create entries have distinct
		// ContentWindow/Section (Team Spawn vs Malformed Regression), so both
		// are Ordinal 0 under the new content-keyed scheme.
		{Owner: "skills/codeblocks.md", Pattern: scan.PatternAskUserQuestion, ContentWindow: "AskUserQuestion(questions: [...])", Section: "Prompt Example", Ordinal: 0, Classification: scan.ClassificationUserDecision},
		{Owner: "skills/codeblocks.md", Pattern: scan.PatternTeamCreate, ContentWindow: `TeamCreate({ team_name: "epoch-impl" })`, Section: "Team Spawn", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/codeblocks.md", Pattern: scan.PatternSendMessage, ContentWindow: `SendMessage({ recipient: "worker-1" })`, Section: "Team Spawn", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/codeblocks.md", Pattern: scan.PatternTeamCreate, ContentWindow: "TeamCreate({...}) -> task(({...})", Section: "Malformed Regression", Ordinal: 0, Classification: scan.ClassificationOrchestration, Notes: "malformed TeamCreate/task(( regression fixture (see pasture#47); reported, never rewritten"},

		// skills/html.md: block HTML comment (across Lines()), block HTML
		// comment closed on its ClosureLine, and inline raw HTML.
		{Owner: "skills/html.md", Pattern: scan.PatternTeamCreate, ContentWindow: `TeamCreate({ team_name: "legacy" })`, Section: "HTML Content", Ordinal: 0, Classification: scan.ClassificationPortableVerbatim, Notes: "commented-out example inside an HTML block comment's Lines()"},
		{Owner: "skills/html.md", Pattern: scan.PatternSendMessage, ContentWindow: `SendMessage({ recipient: "worker-1" }) -->`, Section: "HTML Content", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive, Notes: "commented-out example on the HTML block comment's ClosureLine"},
		{Owner: "skills/html.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Inline comment example: <!-- Skill(/pasture:hidden-example) --> stays invisible to a renderer but not to an LLM reading the raw file.", Section: "HTML Content", Ordinal: 0, Classification: scan.ClassificationOrchestration, Notes: "inline raw HTML comment"},

		// skills/prose.md: prose paragraph, list item, blockquote, code span
		// — each on its own distinct line, so all Ordinal 0.
		{Owner: "skills/prose.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Some intro text before any heading uses Skill(/pasture:worker) inline in a", Section: "Title", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/prose.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "- Then: call Skill(/pasture:supervisor) first.", Section: "Constraints", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/prose.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "> Blockquote note: remember to call Skill(/pasture:explore) before delegating.", Section: "Constraints", Ordinal: 0, Classification: scan.ClassificationOrchestration},
		{Owner: "skills/prose.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Use `Skill(/pasture:worker)` inline code span example.", Section: "Constraints", Ordinal: 0, Classification: scan.ClassificationOrchestration},
	}
}

func TestScanWithManifestsGoldenFixturesAreFullyClassified(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	inventory, err := scan.ScanWithManifests(base, []string{"skills", "agents"}, goldenOwnerManifest(t), goldenClassificationManifest(t))
	require.NoError(t, err)

	require.NoError(t, scan.RequireZeroUnclassified(inventory))
	assert.Equal(t, 0, inventory.UnclassifiedCount())
	require.NoError(t, scan.RequireNoOrphanedClassifications(inventory))
	assert.Empty(t, inventory.OrphanedClassifications())

	// Every closed Classification value is exercised at least once.
	for _, classification := range scan.Classifications() {
		assert.Positive(t, inventory.CountByClassification(classification), "classification %q must have at least one golden fixture", classification)
	}

	// dead-owner.md contains a Skill(/ construct but must never produce a
	// candidate: its disposition is dead.
	for _, candidate := range inventory.Candidates() {
		assert.NotEqual(t, "skills/dead-owner.md", candidate.Candidate.Location().Owner(),
			"a dead-disposed owner must never be parsed for candidates")
	}
}

// TestScanWithManifestsScansHTMLBlockAndRawHTML proves HTMLBlock (including
// content only reachable via ClosureLine) and inline RawHTML are scanned: a
// "commented-out" native-syntax example is still live content to an LLM
// reading the raw file and must not be invisible to the inventory.
func TestScanWithManifestsScansHTMLBlockAndRawHTML(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	discovered, err := scan.Discover(base, []string{"skills", "agents"})
	require.NoError(t, err)
	candidates, err := scan.ScanCandidates(base, discovered, goldenOwnerManifest(t))
	require.NoError(t, err)

	var htmlCandidates []scan.Candidate
	for _, c := range candidates {
		if c.Location().Owner() == "skills/html.md" {
			htmlCandidates = append(htmlCandidates, c)
		}
	}
	require.Len(t, htmlCandidates, 3)

	byPattern := map[scan.PatternID]scan.Candidate{}
	for _, c := range htmlCandidates {
		byPattern[c.Pattern()] = c
	}
	require.Len(t, byPattern, 3, "each of the three html.md candidates uses a distinct pattern")

	teamCreate, ok := byPattern[scan.PatternTeamCreate]
	require.True(t, ok)
	assert.Equal(t, "HTMLBlock", teamCreate.ASTNode(), "the first HTML comment's TeamCreate( is reachable via HTMLBlock.Lines()")

	rawHTML, ok := byPattern[scan.PatternSkillInvocation]
	require.True(t, ok)
	assert.Equal(t, "RawHTML", rawHTML.ASTNode(), "inline raw HTML must be scanned")

	// Exactly one HTMLBlock/RawHTML pattern (send_message) is reachable only
	// via ClosureLine — Lines() alone omits the closing line.
	var closureMatched bool
	for _, c := range htmlCandidates {
		if c.ASTNode() == "HTMLBlock" && c.Pattern() == scan.PatternSendMessage {
			closureMatched = true
			raw, readErr := os.ReadFile(filepath.Join(base, "skills", "html.md"))
			require.NoError(t, readErr)
			rng := c.Location().Range()
			assert.Equal(t, "SendMessage(", string(raw[rng.Start:rng.Stop]))
		}
	}
	assert.True(t, closureMatched, "the second HTML comment's SendMessage( is reachable only via HTMLBlock.ClosureLine")
}

func TestScanCandidatesNeverParsesADeadOwner(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	discovered, err := scan.Discover(base, []string{"skills", "agents"})
	require.NoError(t, err)
	require.Contains(t, discovered, "skills/dead-owner.md", "the dead owner must still be independently discovered")

	candidates, err := scan.ScanCandidates(base, discovered, goldenOwnerManifest(t))
	require.NoError(t, err)
	for _, candidate := range candidates {
		assert.NotEqual(t, "skills/dead-owner.md", candidate.Location().Owner())
	}
}

func TestMalformedTeamCreateRegressionIsReportedNeverRewritten(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	discovered, err := scan.Discover(base, []string{"skills", "agents"})
	require.NoError(t, err)
	candidates, err := scan.ScanCandidates(base, discovered, goldenOwnerManifest(t))
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(base, "skills", "codeblocks.md"))
	require.NoError(t, err)

	var found []scan.Candidate
	for _, candidate := range candidates {
		if candidate.Location().Owner() == "skills/codeblocks.md" && candidate.Location().Section() == "Malformed Regression" {
			found = append(found, candidate)
		}
	}
	require.Len(t, found, 1, "the malformed TeamCreate/task(( line must yield exactly one candidate (the well-formed TeamCreate( prefix), never a crash or a second guessed match")

	candidate := found[0]
	assert.Equal(t, scan.PatternTeamCreate, candidate.Pattern())
	assert.Equal(t, "TeamCreate(", candidate.Snippet())
	assert.Equal(t, "TeamCreate({...}) -> task(({...})", candidate.ContentWindow())
	rng := candidate.Location().Range()
	assert.Equal(t, "TeamCreate(", string(raw[rng.Start:rng.Stop]),
		"the reported byte range must index the exact original (unmodified) source")
	assert.Contains(t, string(raw), "TeamCreate({...}) -> task(({...})",
		"the malformed native syntax itself must remain byte-for-byte in the source — the scanner never rewrites it")
}

func TestScanWithManifestsFailsOnUnreconciledOwnerDrift(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	incompleteOwners, err := scan.NewOwnerManifest([]scan.OwnerEntry{
		{Path: "agents/example.md", Disposition: scan.OwnerActive},
		{Path: "skills/classification-matrix.md", Disposition: scan.OwnerActive},
		{Path: "skills/codeblocks.md", Disposition: scan.OwnerActive},
		{Path: "skills/dead-owner.md", Disposition: scan.OwnerDead, Reason: "retained for audit only"},
		{Path: "skills/html.md", Disposition: scan.OwnerActive},
		// skills/prose.md is deliberately omitted to prove reconciliation fails.
	})
	require.NoError(t, err)

	_, err = scan.ScanWithManifests(base, []string{"skills", "agents"}, incompleteOwners, goldenClassificationManifest(t))
	require.Error(t, err)
	assert.ErrorContains(t, err, "skills/prose.md")
}

// TestScanWithManifestsFailsOnClassificationManifestDrift proves
// ScanWithManifests itself (not just a standalone Inventory method) fails
// when the checked-in classification manifest contains a stale/unmatched
// entry, so the production pipeline fails on classification drift exactly
// as it already fails on owner drift.
func TestScanWithManifestsFailsOnClassificationManifestDrift(t *testing.T) {
	t.Parallel()

	base := goldenFixtureRoot(t)
	baseline := goldenClassificationManifestEntries()
	stale := scan.ClassificationEntry{
		Owner: "skills/prose.md", Pattern: scan.PatternSkillInvocation,
		ContentWindow: "this line does not exist in prose.md", Section: "Constraints", Ordinal: 0,
		Classification: scan.ClassificationOrchestration,
	}
	drifted, err := scan.NewClassificationManifest(append(append([]scan.ClassificationEntry{}, baseline...), stale))
	require.NoError(t, err)

	_, err = scan.ScanWithManifests(base, []string{"skills", "agents"}, goldenOwnerManifest(t), drifted)
	require.Error(t, err)
	assert.ErrorContains(t, err, "skills/prose.md")
	assert.ErrorContains(t, err, "no matching candidate")
}

func TestScanCanonicalAgainstTheRealRepository(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	before, err := scan.HashTree(root, scan.CanonicalRoots())
	require.NoError(t, err)

	inventory, err := scan.ScanCanonical(root)
	require.NoError(t, err)

	after, err := scan.HashTree(root, scan.CanonicalRoots())
	require.NoError(t, err)
	assert.Equal(t, before, after, "ScanCanonical must leave the real repository's canonical roots byte-for-byte unchanged")

	assert.Positive(t, inventory.Len())
	assert.NoError(t, scan.RequireZeroUnclassified(inventory),
		"the checked-in production classification manifest must classify every real candidate this pattern registry currently finds")
	assert.NoError(t, scan.RequireNoOrphanedClassifications(inventory),
		"ScanCanonical already enforces this internally; asserting it again documents the guarantee at the call site")
}

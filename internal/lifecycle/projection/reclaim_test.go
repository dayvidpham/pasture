package projection_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// reclaimWindow is the writer window the tests inject: the longest time a
// writer may hold between its blob write and its journal append.
const reclaimWindow = 30 * time.Second

// snapshotEpoch is the fixed instant every clock in this file starts from.
var snapshotEpoch = time.Unix(1_700_000_000, 0).UTC()

// sequenceClock answers with its current instant and moves to the next one
// only when the test's journal wrapper says the journal was read. The rebuild
// must take its snapshot BEFORE that read; a snapshot taken after it sees the
// later instant.
type sequenceClock struct {
	instants []time.Time
	index    int
}

func (c *sequenceClock) Now() time.Time { return c.instants[c.index] }

func (c *sequenceClock) advance() {
	if c.index < len(c.instants)-1 {
		c.index++
	}
}

// slowReadJournal is the production journal with one change: every evidence
// read advances the clock, as a slow read on a loaded store would.
type slowReadJournal struct {
	provenance.Journal
	clock *sequenceClock
}

func (j slowReadJournal) Facts() provenance.FactQueryAPI {
	return slowReadFacts{FactQueryAPI: j.Journal.Facts(), clock: j.clock}
}

type slowReadFacts struct {
	provenance.FactQueryAPI
	clock *sequenceClock
}

func (f slowReadFacts) QueryEvidence(query provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	f.clock.advance()
	return f.FactQueryAPI.QueryEvidence(query)
}

// openReclaimStore opens a fresh unified store with its persisted identity
// bootstrapped, exactly as the production commands find one.
func openReclaimStore(t *testing.T) (protocol.TaskTracker, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = bootstrap.Create("file://projection-reclaim-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tracker.Close() })
	return tracker, auditDB(t, tracker)
}

// seedBlob writes one payload blob with an explicit written-at stamp, through
// the table the production store writes, so the age the reclaim reads is the
// one the test chose.
func seedBlob(t *testing.T, db *sql.DB, body []byte, writtenAt time.Time) digest.Digest {
	t.Helper()
	ref := digest.FromBytes(body)
	stamp := int64(0)
	if !writtenAt.IsZero() {
		stamp = writtenAt.UnixNano()
	}
	_, err := db.Exec(`INSERT INTO lifecycle_payload_blobs(digest, body, byte_count, written_at) VALUES(?,?,?,?)`, ref.String(), body, len(body), stamp)
	require.NoError(t, err)
	return ref
}

func blobExists(t *testing.T, db *sql.DB, ref digest.Digest) bool {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs WHERE digest=?`, ref.String()).Scan(&count))
	return count == 1
}

// commitOccurrenceNaming appends one occurrence to the real journal that
// names the blob, through the production journal and identity, so the
// projection rebuild will project it.
func commitOccurrenceNaming(t *testing.T, tracker protocol.TaskTracker, ref digest.Digest, operation string) {
	t.Helper()
	ctx := context.Background()
	resolver, ok := tracker.(receipt.IdentityResolver)
	require.True(t, ok)
	identity, err := resolver.ResolveLifecycleIdentity(ctx)
	require.NoError(t, err)
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/2.1.210")
	require.NoError(t, err)
	payload, err := json.Marshal(struct {
		Contract string                      `json:"contract"`
		Event    model.ContractEventKind     `json:"event"`
		Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
		Bindings []model.NativeBinding       `json:"bindings"`
		Capture  model.CaptureDisposition    `json:"capture"`
		Body     string                      `json:"body_digest"`
	}{Contract: contract.String(), Event: model.ContractEventKind(1), Envelope: model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: contract}}, Capture: model.CaptureValid, Body: ref.String()})
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte("pasture.lifecycle.receipt.append/v1\x00"), payload...))
	authority := identity.Authority
	input := provenance.OperationInput{
		OperationID: provenance.OperationID(operation), ActorID: identity.Actor, AuthorityJournalID: &authority,
		CommandDigest: command[:], RecordedAt: snapshotEpoch.UnixNano(),
		Effects: []provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID("occurrence"), EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.occurrence.v1"), ContentDigest: sum[:], Payload: append(json.RawMessage(nil), payload...)}},
	}
	canonical, err := provenance.Canonicalize(input)
	require.NoError(t, err)
	input.Effects = canonical.NormalizedEffects()
	for index := range input.Effects {
		digestSum := sha256.Sum256(input.Effects[index].Payload)
		input.Effects[index].ContentDigest = append([]byte(nil), digestSum[:]...)
	}
	journal, ok := tracker.Journal().(provenance.ContextJournal)
	require.True(t, ok)
	_, err = journal.ApplyContext(ctx, input)
	require.NoError(t, err)
}

func reclaimingRebuild(t *testing.T, tracker protocol.TaskTracker, db *sql.DB, clock receipt.Clock) (projection.ReclaimReport, string) {
	t.Helper()
	var diagnostics bytes.Buffer
	report, err := projection.RebuildOccurrencesReclaiming(context.Background(), tracker.Journal(), db, projection.RebuildOptions{Clock: clock, Window: reclaimWindow, Diagnostics: &diagnostics})
	require.NoError(t, err, "the reclaiming rebuild must complete")
	return report, diagnostics.String()
}

func TestReclaimingRebuildReclaimsOnlyUnnamedBlobsOlderThanTheWindow(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	oldOrphan := seedBlob(t, db, []byte("old orphan"), snapshotEpoch.Add(-reclaimWindow-time.Nanosecond))
	youngOrphan := seedBlob(t, db, []byte("young orphan"), snapshotEpoch.Add(-reclaimWindow+time.Second))
	oldNamed := seedBlob(t, db, []byte("old named"), snapshotEpoch.Add(-1000*time.Second))
	commitOccurrenceNaming(t, tracker, oldNamed, "reclaim.named.old")
	legacyNamed := seedBlob(t, db, []byte("legacy named"), time.Time{})
	commitOccurrenceNaming(t, tracker, legacyNamed, "reclaim.named.legacy")
	legacyOrphan := seedBlob(t, db, []byte("legacy orphan"), time.Time{})

	report, diagnostics := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, report.Failure)
	assert.Empty(t, diagnostics, "a reclaim that succeeds says nothing")
	assert.Equal(t, []digest.Digest{legacyOrphan, oldOrphan}, report.Reclaimed, "oldest first: the legacy blob at 0, then the orphan older than the window")

	assert.False(t, blobExists(t, db, oldOrphan), "an unnamed blob older than the window is reclaimed")
	assert.False(t, blobExists(t, db, legacyOrphan), "an unnamed legacy blob at written_at 0 is older than any bound and is reclaimed")
	assert.True(t, blobExists(t, db, youngOrphan), "an unnamed blob inside the window is NEVER reclaimed: its journal append may still be in flight")
	assert.True(t, blobExists(t, db, oldNamed), "a blob an occurrence names is never reclaimed at any age (%s)", oldNamed)
	assert.True(t, blobExists(t, db, legacyNamed), "a legacy blob an occurrence names is never reclaimed (%s)", legacyNamed)
	var projected int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&projected))
	assert.Equal(t, 2, projected, "the rebuild still projected every journal occurrence")
}

// TestThePlainRebuildReclaimsNothing pins that the entry every read command
// runs today is unchanged: it projects and deletes no blob, however old.
func TestThePlainRebuildReclaimsNothing(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	orphan := seedBlob(t, db, []byte("plain orphan"), time.Time{})
	require.NoError(t, projection.RebuildOccurrences(context.Background(), tracker.Journal(), db))
	assert.True(t, blobExists(t, db, orphan), "RebuildOccurrences must not reclaim; only the reclaiming entry does")
}

// TestReclaimAgesAgainstTheSnapshotInstantNotTheDeleteInstant: the clock
// answers the snapshot at the epoch and any later look far past the window,
// as a slow rebuild would see. An unnamed blob written one second before the
// snapshot is inside the window relative to the snapshot and must be kept; a
// reclaim that aged it against the delete instant would remove it while its
// append could still be in flight.
func TestReclaimAgesAgainstTheSnapshotInstantNotTheDeleteInstant(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	inFlight := seedBlob(t, db, []byte("written just before the snapshot"), snapshotEpoch.Add(-time.Second))
	clock := &sequenceClock{instants: []time.Time{snapshotEpoch, snapshotEpoch.Add(reclaimWindow + 2*time.Second)}}
	var diagnostics bytes.Buffer
	report, err := projection.RebuildOccurrencesReclaiming(context.Background(), slowReadJournal{Journal: tracker.Journal(), clock: clock}, db, projection.RebuildOptions{Clock: clock, Window: reclaimWindow, Diagnostics: &diagnostics})
	require.NoError(t, err)
	require.NoError(t, report.Failure)
	require.Equal(t, 1, clock.index, "the journal read advanced the clock, so a snapshot taken after the read would see the later instant")
	assert.Empty(t, report.Reclaimed)
	assert.True(t, blobExists(t, db, inFlight), "a blob younger than the window relative to the SNAPSHOT instant must be kept (%s); ageing it against the delete instant reclaims an in-flight blob", inFlight)
}

// TestReclaimCapIsOldestFirstAndExactlyTheCap is the budget proof: with more
// orphans than the cap, one rebuild reclaims exactly the cap, the oldest
// first, and the next rebuild reclaims the rest. It waits on nothing but the
// rebuilds themselves.
func TestReclaimCapIsOldestFirstAndExactlyTheCap(t *testing.T) {
	t.Parallel()
	// The cap is PINNED at its value, not read back from the constant: a
	// test that derived its counts from the constant would stay green if the
	// cap were raised until it bounded nothing.
	require.Equal(t, 1024, projection.ReclaimCap, "the cap sizes a read command's added latency and drains the measured legacy backlog in about fifteen commands; a change is a change to that claim")
	tracker, db := openReclaimStore(t)
	const cap = 1024
	const extra = 6
	total := cap + extra
	youngest := make([]digest.Digest, 0, extra)
	for index := 0; index < total; index++ {
		writtenAt := snapshotEpoch.Add(-2*reclaimWindow - time.Duration(total-index)*time.Millisecond)
		ref := seedBlob(t, db, []byte(fmt.Sprintf("orphan %d", index)), writtenAt)
		if index >= cap {
			youngest = append(youngest, ref)
		}
	}
	first, _ := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, first.Failure)
	assert.Len(t, first.Reclaimed, cap, "exactly the cap is reclaimed in one rebuild")
	for _, ref := range youngest {
		assert.True(t, blobExists(t, db, ref), "the youngest orphans wait for the next rebuild")
	}
	second, _ := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, second.Failure)
	assert.Len(t, second.Reclaimed, extra, "the next rebuild reclaims the rest")
	var remaining int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs`).Scan(&remaining))
	assert.Equal(t, 0, remaining)
}

// TestStaleProjectionNeverLosesAJournalNamedBlob is the mutation the design
// exists for: the projection was NEVER rebuilt, the journal names a blob
// written long before the window, and the reclaiming rebuild must keep it,
// because the reclaim reads the projection this rebuild has just re-inserted
// in the same transaction. A reclaim run before the re-insert would delete it
// and the re-insert would then fail on the foreign key.
func TestStaleProjectionNeverLosesAJournalNamedBlob(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	named := seedBlob(t, db, []byte("named long ago"), snapshotEpoch.Add(-1000*time.Second))
	commitOccurrenceNaming(t, tracker, named, "reclaim.stale.named")
	var projected int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&projected))
	require.Equal(t, 0, projected, "the projection is stale by construction: never rebuilt")

	report, _ := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, report.Failure)
	assert.Empty(t, report.Reclaimed)
	assert.True(t, blobExists(t, db, named), "the journal-named blob %s was deleted although a journal row names it", named)
}

// TestAReclaimFailureLeavesTheRebuildCompleteAndSaysSo makes the reclaim's
// delete fail by taking the written-at column away, and requires: the rebuild
// completes and projects, nothing is deleted, the report carries the failure,
// and exactly one diagnostic line is written with its pinned phrases.
func TestAReclaimFailureLeavesTheRebuildCompleteAndSaysSo(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	orphan := seedBlob(t, db, []byte("orphan under a failing reclaim"), time.Time{})
	named := seedBlob(t, db, []byte("named under a failing reclaim"), time.Time{})
	commitOccurrenceNaming(t, tracker, named, "reclaim.failure.named")
	_, err := db.Exec(`ALTER TABLE lifecycle_payload_blobs RENAME COLUMN written_at TO written_at_gone`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE lifecycle_payload_blobs RENAME COLUMN written_at_gone TO written_at`)
	})

	report, diagnostics := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.Error(t, report.Failure, "the reclaim must report that it could not run")
	assert.Empty(t, report.Reclaimed)
	assert.True(t, blobExists(t, db, orphan), "a failed reclaim deletes nothing")
	var projected int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&projected))
	assert.Equal(t, 1, projected, "the rebuild still completed and projected the journal")
	lines := strings.Split(strings.TrimRight(diagnostics, "\n"), "\n")
	require.Len(t, lines, 1, "exactly one diagnostic line, got %q", diagnostics)
	assert.Equal(t, projection.ReclaimFailureLine(report.Failure), lines[0])
	assert.Contains(t, lines[0], "the orphan payload reclaim inside the projection rebuild failed")
	assert.Contains(t, lines[0], "the rebuild still completed and nothing was deleted")
	assert.Contains(t, lines[0], "the next read command retries the reclaim")
}

func TestRebuildOptionsRefuseMissingInputs(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	clock := &sequenceClock{instants: []time.Time{snapshotEpoch}}
	cases := []struct {
		name    string
		options projection.RebuildOptions
		refusal string
	}{
		{"no clock", projection.RebuildOptions{Window: reclaimWindow, Diagnostics: &bytes.Buffer{}}, "has no clock"},
		{"no window", projection.RebuildOptions{Clock: clock, Diagnostics: &bytes.Buffer{}}, "has no writer window"},
		{"no diagnostics", projection.RebuildOptions{Clock: clock, Window: reclaimWindow}, "has no diagnostic stream"},
	}
	for _, tc := range cases {
		_, err := projection.RebuildOccurrencesReclaiming(context.Background(), tracker.Journal(), db, tc.options)
		require.Error(t, err, tc.name)
		assert.Contains(t, err.Error(), tc.refusal, tc.name)
	}
}

// moduleRootFrom walks up from the package directory to the module root.
func moduleRootFrom(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent)
		dir = parent
	}
}

func renderExpr(node ast.Expr) string {
	var out strings.Builder
	_ = printer.Fprint(&out, token.NewFileSet(), node)
	return out.String()
}

// TestTheReclaimIsReachedOnlyFromTheRebuild is the reach guard.
//
// WHAT IT VISITS: every non-test Go source of this package, for calls to the
// unexported reclaim; and every non-test Go source of the whole module, for
// constructions of the narrow delete type receipt.PayloadReclaimer and for
// calls to the reclaiming rebuild entry.
// WHAT IT DOES NOT READ: test sources, which may construct anything; and
// whether a caller of the reclaiming entry supplies the right window, which
// the options refusals and the callers' own tests hold.
//
// It requires: the reclaim is called exactly once, from rebuildOccurrences;
// the delete type is constructed exactly once, in this package's reclaim
// file; and the number of production callers of the reclaiming entry is the
// stated one, so a caller added on a hook path is refused here rather than
// discovered by an operator.
func TestTheReclaimIsReachedOnlyFromTheRebuild(t *testing.T) {
	t.Parallel()
	root := moduleRootFrom(t)

	reclaimCalls := map[string]int{}
	deleters := []string{}
	reclaimingCallers := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "worktree", "legacy", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, _ := filepath.Rel(root, path)
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CallExpr:
					switch renderExpr(typed.Fun) {
					case "reclaimOrphanPayloads":
						reclaimCalls[relative+": "+function.Name.Name]++
					case "RebuildOccurrencesReclaiming", "projection.RebuildOccurrencesReclaiming":
						reclaimingCallers = append(reclaimingCallers, relative+": "+function.Name.Name)
					}
				case *ast.CompositeLit:
					if renderExpr(typed.Type) == "receipt.PayloadReclaimer" || renderExpr(typed.Type) == "PayloadReclaimer" {
						deleters = append(deleters, relative+": "+function.Name.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{filepath.Join("internal", "lifecycle", "projection", "rebuild.go") + ": rebuildOccurrences": 1}, reclaimCalls,
		"the reclaim is called exactly once, from the rebuild, inside its transaction after the re-insert; any other caller reads a projection that may be stale")
	assert.Equal(t, []string{filepath.Join("internal", "lifecycle", "projection", "reclaim.go") + ": reclaimOrphanPayloads"}, deleters,
		"the narrow delete type is constructed only by the reclaim; a construction anywhere else is a second delete door")
	// The reclaiming entry has NO production caller yet: the read commands
	// still run the plain rebuild until their diagnostic stream is threaded to
	// it. This number is stated so that the day a caller appears it is a
	// deliberate change to this line, and a caller on a hook path is refused.
	assert.Empty(t, reclaimingCallers, "production callers of the reclaiming rebuild: %v; a new caller must be a read command with a diagnostic stream, never a hook path", reclaimingCallers)
}

func TestReclaimFailureLineNamesTheOutcome(t *testing.T) {
	t.Parallel()
	line := projection.ReclaimFailureLine(fmt.Errorf("the delete was refused"))
	assert.True(t, strings.HasPrefix(line, "pasture: the orphan payload reclaim inside the projection rebuild failed: the delete was refused"))
	assert.Contains(t, line, "after the projection rows were rebuilt")
	assert.Contains(t, line, "the rebuild still completed and nothing was deleted")
	assert.Contains(t, line, "the next read command retries the reclaim")
}

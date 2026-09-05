package projection_test

import (
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

func reclaimingRebuild(t *testing.T, tracker protocol.TaskTracker, db *sql.DB, clock receipt.Clock) projection.ReclaimOutcome {
	t.Helper()
	outcome, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{Clock: clock, Window: reclaimWindow})
	require.NoError(t, err, "the rebuild must complete")
	return outcome
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

	report := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, report.Failure, "a reclaim that succeeds reports no failure, so the store prints nothing")
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
	report, err := projection.RebuildOccurrences(context.Background(), slowReadJournal{Journal: tracker.Journal(), clock: clock}, db, projection.RebuildOptions{Clock: clock, Window: reclaimWindow})
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
	first := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, first.Failure)
	assert.Len(t, first.Reclaimed, cap, "exactly the cap is reclaimed in one rebuild")
	for _, ref := range youngest {
		assert.True(t, blobExists(t, db, ref), "the youngest orphans wait for the next rebuild")
	}
	second := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
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

	report := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
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

	report := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.Error(t, report.Failure, "the failed reclaim is reported in the outcome for the store to print")
	diagnostics := projection.ReclaimFailureLine(report.Failure) + "\n"
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
		{"no clock", projection.RebuildOptions{Window: reclaimWindow}, "has no clock"},
		{"no window", projection.RebuildOptions{Clock: clock}, "has no writer window"},
	}
	for _, tc := range cases {
		_, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, tc.options)
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
					case "RebuildOccurrences", "projection.RebuildOccurrences":
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
	// The rebuild has EXACTLY ONE production caller, the tasks wrapper that
	// every read command reaches; the reclaim is reached through it and
	// through nothing else. A second caller is a new path that must state
	// where its failure line goes; a caller on a hook path is refused.
	assert.Equal(t, []string{filepath.Join("internal", "tasks", "lifecycle_identity.go") + ": RebuildLifecycleOccurrencesReporting"}, reclaimingCallers,
		"production callers of the rebuild: %v; the one caller is the tasks wrapper the read commands share, never a hook path", reclaimingCallers)
}

func TestReclaimFailureLineNamesTheOutcome(t *testing.T) {
	t.Parallel()
	line := projection.ReclaimFailureLine(fmt.Errorf("the delete was refused"))
	assert.True(t, strings.HasPrefix(line, "pasture: the orphan payload reclaim inside the projection rebuild failed: the delete was refused"))
	assert.Contains(t, line, "after the projection rows were rebuilt")
	assert.Contains(t, line, "the rebuild still completed and nothing was deleted")
	assert.Contains(t, line, "the next read command retries the reclaim")
}

// TestAFreshlyPutBlobIsNeverReclaimedWhileItsWriterMayStillAppend is the age
// test on the production write: a blob the production store Put at the
// snapshot instant is younger than the window and survives the reclaim,
// while one it Put earlier than the window is reclaimed. A store that wrote
// no stamp would leave both at zero and reclaim both.
func TestAFreshlyPutBlobIsNeverReclaimedWhileItsWriterMayStillAppend(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	ctx := context.Background()
	fresh := []byte(`{"hook_event_name":"Stop","writer":"in flight"}`)
	freshRef := digest.FromBytes(fresh)
	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(&sequenceClock{instants: []time.Time{snapshotEpoch}})).Put(ctx, freshRef, fresh))
	stale := []byte(`{"hook_event_name":"Stop","writer":"abandoned long ago"}`)
	staleRef := digest.FromBytes(stale)
	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(&sequenceClock{instants: []time.Time{snapshotEpoch.Add(-reclaimWindow - time.Nanosecond)}})).Put(ctx, staleRef, stale))

	report := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, report.Failure)
	assert.True(t, blobExists(t, db, freshRef), "a blob the production store wrote inside the window is never reclaimed: its writer may still be between the blob write and the journal append")
	assert.False(t, blobExists(t, db, staleRef), "control: a blob the production store wrote before the window is reclaimed")
	assert.Equal(t, []digest.Digest{staleRef}, report.Reclaimed)
}

// TestARedeliveredOrphanBodyIsProtectedByItsNewWriter: a legacy orphan
// carries the body a new delivery re-sends. The new writer's Put must refresh
// the stamp, or the reclaim would delete the blob under that writer's append
// and the journal would then name an absent blob. The control is a legacy
// orphan nobody re-sent, which is reclaimed at once.
func TestARedeliveredOrphanBodyIsProtectedByItsNewWriter(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	redelivered := []byte(`{"hook_event_name":"SessionStart","body":"seen before"}`)
	redeliveredRef := seedBlob(t, db, redelivered, time.Time{})
	untouchedRef := seedBlob(t, db, []byte(`{"hook_event_name":"SessionStart","body":"never seen again"}`), time.Time{})
	require.NoError(t, receipt.NewSQLiteBlobStore(db, receipt.WithPayloadClock(&sequenceClock{instants: []time.Time{snapshotEpoch}})).Put(context.Background(), redeliveredRef, redelivered))

	report := reclaimingRebuild(t, tracker, db, &sequenceClock{instants: []time.Time{snapshotEpoch}})
	require.NoError(t, report.Failure)
	assert.True(t, blobExists(t, db, redeliveredRef), "a legacy orphan whose body a new writer re-delivered inside the window is protected by that writer's refreshed stamp")
	assert.False(t, blobExists(t, db, untouchedRef), "control: the legacy orphan nobody re-delivered is reclaimed")
}

// TestTheReclaimIsOneBoundedStatement is the budget proof's structural half:
// the work a rebuild adds is ONE SQL statement whose row count the cap bounds,
// so a read command's added latency scales with the cap and not with the
// backlog, and the drain proof above (exactly the cap, then the rest) is the
// behavioural half. WHAT IT VISITS: the body of ReclaimOrphansWrittenBefore
// in internal/lifecycle/receipt/journal.go, every call on the transaction, and
// the string literal that call is given. WHAT IT DOES NOT READ: any other
// function, the projection package, or SQLite's plan for the statement.
func TestTheReclaimIsOneBoundedStatement(t *testing.T) {
	t.Parallel()
	path := filepath.Join(moduleRootFrom(t), "internal", "lifecycle", "receipt", "journal.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "ReclaimOrphansWrittenBefore" {
			body = function.Body
		}
	}
	require.NotNil(t, body, "ReclaimOrphansWrittenBefore must be declared in journal.go")
	statements := []string{}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(renderExpr(selector.X), "r.Tx") {
			return true
		}
		require.GreaterOrEqual(t, len(call.Args), 2, "a statement on the transaction takes the context and the SQL first")
		literal, ok := call.Args[1].(*ast.BasicLit)
		require.True(t, ok, "the SQL must be a literal so this pin can read it")
		statements = append(statements, selector.Sel.Name+": "+literal.Value)
		return true
	})
	require.Len(t, statements, 1, "the reclaim issues exactly one statement on the transaction: %v", statements)
	assert.Contains(t, statements[0], "LIMIT ?", "the one statement carries the cap as its LIMIT")
	assert.Contains(t, statements[0], "b.written_at < ?", "the one statement carries the age bound")
	assert.Contains(t, statements[0], "o.journal_id IS NULL", "the one statement carries the named-by-nothing condition")
	assert.Contains(t, statements[0], "ORDER BY b.written_at ASC", "the one statement takes the oldest first")
}

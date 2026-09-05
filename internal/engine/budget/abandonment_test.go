package budget_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/engine/budget"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

// This file is one subject: WHAT AN ABANDONED LIFECYCLE INVOCATION LEAVES
// BEHIND in the durable store.
//
// The lifecycle hook bounds its own work and abandons it at the deadline, so
// the invocation can stop between the two durable writes of one receipt. The
// safety of that abandonment used to be ARGUED in prose and asserted nowhere.
// These tests assert it.
//
// # The invariant, and why the obvious wording is wrong
//
// The tempting sentence is "an abandoned invocation leaves either a committed
// receipt or no receipt, never a partial write". That is NOT exhaustive. One
// receipt is TWO durable writes, in a deliberate order stated at
// internal/lifecycle/receipt/service.go: the payload blob commits in its own
// transaction, and the journal occurrence is appended afterwards, because "an
// orphan blob is reclaimable, while a journal row that names an absent blob is
// corruption".
//
// So abandonment can land in FOUR legal states:
//
//	1. nothing written;
//	2. a metamodel activation and no receipt — safe, the activation is
//	   idempotent under a content-derived operation identity;
//	3. a committed payload BLOB and no occurrence — safe and named: the write
//	   order is chosen for exactly this, and the blob is reclaimable;
//	4. a committed occurrence and no continuation to the host — safe under
//	   fail-open, and the record is the evidence.
//
// The invariant that matters, and the one the code states, is stronger than the
// pair: AN OCCURRENCE NEVER NAMES AN ABSENT BLOB. State 3 is legal by design and
// must stay legal; it is not an error and the write order must not be changed to
// remove it.
//
// # The consequence that costs a user disk
//
// State 3 is the LIKELY outcome under the condition the deadline exists for,
// because the journal append is exactly where a locked database makes the
// invocation wait. One abandoned invocation leaves exactly ONE orphan blob
// holding the FULL RAW HOST PAYLOAD. Nothing reclaims it today. The measurement
// is asserted below so the number is a fact and not an estimate.

// blockedJournal is the seam the receipt service already has: the production
// appender applies its operation through an injected provenance journal. This
// one signals that the append has REALLY begun and then waits on the context it
// was given, exactly as a contended journal does, so the test can cancel the
// invocation at that precise point instead of racing a clock.
type blockedJournal struct {
	provenance.Journal
	reached chan struct{}
}

func (j *blockedJournal) ApplyContext(ctx context.Context, _ provenance.OperationInput) (provenance.CommittedResult, error) {
	close(j.reached)
	<-ctx.Done()
	return provenance.CommittedResult{}, ctx.Err()
}

// TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob proves the
// invariant over the states abandonment can reach, through the PRODUCTION
// commit sequence and a REAL store, with a DETERMINISTIC interleaving: the
// appender signals that it has been reached and then waits, and the test cancels
// the invocation exactly there. Nothing races a clock.
//
// LIMIT, stated where a later reader meets it: this proves what the COMMIT
// SEQUENCE leaves in the store when it is cancelled at that point. It does not
// kill an operating-system process mid-write. The remaining gap is SQLite's own
// crash behaviour, which is the database engine's guarantee and not pasture's:
// the blob transaction has already committed, and an uncommitted journal
// transaction is rolled back from the write-ahead log when the next connection
// opens the file.
func TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pasture.db")
	profile := timeouts.DeadlineTestProfile()
	bootstrap(t, dbPath, profile)
	tracker, err := openTrackerWithPool(dbPath, profile, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()

	service, err := tasks.NewLifecycleReceiptServiceWithProfile(
		tracker, budget.RealClock{}, &operationSource{prefix: "abandoned"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	blobs, isReal := service.Blobs.(receipt.SQLiteBlobStore)
	if !isReal {
		t.Fatalf("production blob store type=%T, want the real SQLite store", service.Blobs)
	}

	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := claudeingress.Parse(
		raw, registration.ClaudeCode2_1_261().Events[0], registration.ClaudeCode2_1_261().Version, model.OccurrenceEnvelopeRef{}).Delivery
	ref := digest.FromBytes(delivery.Body)

	// STATE 3: abandoned between the blob commit and the journal append.
	abandoned := service
	journal := &blockedJournal{Journal: service.Appender.Journal, reached: make(chan struct{})}
	abandoned.Appender.Journal = journal

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, receiveErr := abandoned.Receive(ctx, deliveryWarrant(t, delivery), delivery)
		failed <- receiveErr
	}()

	// The condition, not a sleep: the append has really begun.
	<-journal.reached
	cancel()
	receiveErr := <-failed

	if receiveErr == nil {
		t.Fatal("an abandoned append must return an error, never a receipt")
	}
	if !stderrors.Is(receiveErr, context.Canceled) {
		t.Fatalf("Receive error=%v, want the abandonment to surface as a cancellation", receiveErr)
	}
	var structured *pasterrors.StructuredError
	if !stderrors.As(receiveErr, &structured) {
		t.Fatalf("Receive error=%v, want the actionable structured error", receiveErr)
	}
	if !strings.Contains(structured.Impact, "reclaimable orphan") {
		t.Errorf("the abandonment impact = %q, want it to name the orphan blob it may have left, "+
			"because that is the state a reader has to go and look for", structured.Impact)
	}

	exists, err := blobs.Exists(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		// Reported, not fatal: the invariant check below is the load-bearing
		// assertion of this test and must still run when the write order
		// changes, so a reader sees BOTH consequences of one edit.
		t.Error("state 3 requires the payload blob to be committed before the journal append")
	}
	assertNoOccurrenceNamesAnAbsentBlob(t, tracker, blobs)

	// THE MEASUREMENT. Exactly one orphan per abandoned invocation, holding the
	// whole raw host payload. Both numbers are reported so the growth is a fact.
	orphans, err := blobs.Reclaimable(context.Background(), receipt.MaxReclaimablePayloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0] != ref {
		t.Errorf("reclaimable set = %v, want exactly one orphan [%s] per abandoned invocation", orphans, ref)
	}
	t.Logf("ORPHAN MEASUREMENT: one abandoned invocation leaves %d orphan blob of %d bytes "+
		"(this host payload); the per-orphan bound is the ingress payload cap of %d bytes",
		len(orphans), len(delivery.Body), model.MaxNativePayloadBytes)

	// STATE 4: the same delivery, not abandoned, commits and leaves NO orphan.
	// The blob is content-addressed, so the orphan of the abandoned run is
	// adopted by the occurrence rather than duplicated: a retry after an
	// abandonment reclaims its own orphan.
	if _, err := service.Receive(context.Background(), deliveryWarrant(t, delivery), delivery); err != nil {
		t.Fatalf("the same delivery must commit once the store is free: %v", err)
	}
	assertNoOccurrenceNamesAnAbsentBlob(t, tracker, blobs)
	orphans, err = blobs.Reclaimable(context.Background(), receipt.MaxReclaimablePayloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("reclaimable set = %v after the committed retry, want none: the occurrence adopts its own blob", orphans)
	}
}

// assertNoOccurrenceNamesAnAbsentBlob is the invariant itself, read straight
// out of the store: every committed occurrence names a payload digest, and
// every one of those digests is present in the blob table. The corrupting state
// is unreachable because the blob write precedes the append and the append is
// one operation.
func assertNoOccurrenceNamesAnAbsentBlob(t *testing.T, tracker protocol.TaskTracker, blobs receipt.SQLiteBlobStore) {
	t.Helper()

	// The projection rebuild is the FIRST place the invariant is enforced, and
	// it is enforced by the schema rather than by this test: the occurrence
	// table carries a foreign key to the payload blob table, so an occurrence
	// that named an absent blob could not be projected at all. A rebuild that
	// fails on a foreign-key violation IS this invariant refusing the corrupting
	// state; it is not a broken test fixture.
	if err := tasks.RebuildLifecycleOccurrences(context.Background(), tracker); err != nil {
		t.Fatalf("the lifecycle occurrence projection could not be rebuilt: %v. "+
			"A foreign-key violation here means an occurrence names a payload blob that is not stored, "+
			"which is the corrupting state the blob-before-journal write order exists to prevent", err)
	}
	rows, err := blobs.DB.QueryContext(context.Background(),
		`SELECT o.journal_id, o.payload_digest FROM lifecycle_occurrences o
		 LEFT JOIN lifecycle_payload_blobs b ON b.digest = o.payload_digest
		 WHERE b.digest IS NULL`)
	if err != nil {
		t.Fatalf("read the occurrence-to-blob join: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var journalID int64
		var payload string
		if err := rows.Scan(&journalID, &payload); err != nil {
			t.Fatal(err)
		}
		t.Errorf("occurrence %d names payload %s, which is absent: this is the corrupting state and it must be unreachable",
			journalID, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

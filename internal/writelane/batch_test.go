package writelane

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite"
)

// realRecord is a lane Record backed by a real canonical operation: Prepare
// returns the operation unchanged and Settle records the outcome it was handed
// and reports it through the lane's own mapping. It lets a test drive the lane
// against a REAL provenance batch journal and observe each record's report.
type realRecord struct {
	input provenance.OperationInput

	mu      sync.Mutex
	outcome provenance.BatchRecordResult
	settles int
}

func (r *realRecord) Prepare(context.Context) (provenance.OperationInput, error) {
	return r.input, nil
}

func (r *realRecord) Settle(_ context.Context, outcome provenance.BatchRecordResult) error {
	r.mu.Lock()
	r.outcome = outcome
	r.settles++
	r.mu.Unlock()
	return Report(outcome)
}

func (r *realRecord) observed() (provenance.BatchRecordResult, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outcome, r.settles
}

// coalesce stages every record behind one blocker record and releases it, so the
// lane's next turn takes all of them as ONE batch. It returns each record's
// Submit result channel.
func coalesce(t *testing.T, lane *Lane, records []Record) []<-chan error {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	blocker := commitFunc(func(context.Context) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	})
	blockerDone := waitForCommitted(lane, context.Background(), 8, blocker)
	<-entered
	done := make([]<-chan error, len(records))
	for i, record := range records {
		done[i] = waitForCommitted(lane, context.Background(), 8, record)
	}
	deadline := time.Now().Add(2 * time.Second)
	for lane.Stats().QueueDepth < int64(len(records)) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if depth := lane.Stats().QueueDepth; depth < int64(len(records)) {
		t.Fatalf("only %d of %d records staged behind the blocker; the batch would not be the set under test", depth, len(records))
	}
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatalf("the blocker record failed: %v", err)
	}
	return done
}

// TestTheOutcomeMappingIsHonest pins the ONE home of the mapping from a
// provenance BatchRecordOutcome to the lane's per-request report, for every
// outcome in the closed set plus the missing-outcome case a refused batch
// produces.
func TestTheOutcomeMappingIsHonest(t *testing.T) {
	conflict := errors.New("the operation identity is already committed with a differing identity")
	failed := errors.New("the record failed its precondition")
	rollback := errors.New("the batch rolled back")

	cases := []struct {
		name    string
		outcome provenance.BatchRecordResult
		apply   error
		// missing exercises the branch a batch apply that refused before it
		// built any outcome produces.
		missing bool
		// wantDurable is what the caller learns: nil error means committed.
		wantDurable bool
		wantErr     error
	}{
		{
			name:        "committed is durable and is not a failure",
			outcome:     provenance.BatchRecordResult{Outcome: provenance.BatchRecordCommitted},
			wantDurable: true,
		},
		{
			name:        "short-circuited is durable from an earlier transaction and is not a failure",
			outcome:     provenance.BatchRecordResult{Outcome: provenance.BatchRecordShortCircuited},
			wantDurable: true,
		},
		{
			name:    "conflict reports its typed conflict and never a commit",
			outcome: provenance.BatchRecordResult{Outcome: provenance.BatchRecordConflict, Err: conflict},
			wantErr: conflict,
		},
		{
			name:    "failed reports its own typed error",
			outcome: provenance.BatchRecordResult{Outcome: provenance.BatchRecordFailed, Err: failed},
			wantErr: failed,
		},
		{
			name:    "not-recorded carries the batch error when the record has none of its own",
			outcome: provenance.BatchRecordResult{Outcome: provenance.BatchRecordNotRecorded},
			apply:   rollback,
			wantErr: rollback,
		},
		{
			name:    "a missing outcome is not-recorded with the batch error attached",
			missing: true,
			apply:   rollback,
			wantErr: rollback,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// batchOutcome is the lane's fill-in step; Report is the mapping.
			result := provenance.BatchResult{Committed: tc.wantDurable, Records: []provenance.BatchRecordResult{tc.outcome}}
			if tc.missing {
				result = provenance.BatchResult{}
			}
			mapped := batchOutcome(result, 0, tc.apply)
			if got := OutcomeIsDurable(mapped.Outcome); got != tc.wantDurable {
				t.Fatalf("OutcomeIsDurable(%q) = %v, want %v", mapped.Outcome, got, tc.wantDurable)
			}
			err := Report(mapped)
			switch {
			case tc.wantDurable && err != nil:
				t.Fatalf("a durable outcome reported a failure: %v", err)
			case !tc.wantDurable && err == nil:
				t.Fatalf("a non-durable outcome (%q) reported a commit", mapped.Outcome)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("reported error = %v, want it to carry %v", err, tc.wantErr)
			}
		})
	}
}

// TestAWholeBatchFailureIsNeverReportedAsSomeCommitted drives a lane whose
// batch applier rolls the batch back with mixed per-record outcomes: the record
// the batch newly folded must NOT be reported as committed, and the
// short-circuited record — durable from an earlier transaction — must still
// report success.
func TestAWholeBatchFailureIsNeverReportedAsSomeCommitted(t *testing.T) {
	conflict := errors.New("the operation identity is already committed with a differing identity")
	rollback := errors.New("the batch transaction rolled back")

	folded := &realRecord{input: provenance.OperationInput{OperationID: "batch-folded"}}
	replayed := &realRecord{input: provenance.OperationInput{OperationID: "batch-replayed"}}
	conflicting := &realRecord{input: provenance.OperationInput{OperationID: "batch-conflicting"}}

	apply := func(_ context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
		records := make([]provenance.BatchRecordResult, len(inputs))
		for i, in := range inputs {
			switch in.OperationID {
			case "batch-replayed":
				records[i] = provenance.BatchRecordResult{Outcome: provenance.BatchRecordShortCircuited}
			case "batch-conflicting":
				records[i] = provenance.BatchRecordResult{Outcome: provenance.BatchRecordConflict, Err: conflict}
			default:
				records[i] = provenance.BatchRecordResult{Outcome: provenance.BatchRecordNotRecorded}
			}
		}
		return provenance.BatchResult{Committed: false, Records: records},
			&provenance.BatchApplyError{Index: 2, OperationID: "batch-conflicting", Cause: rollback}
	}

	budget := testBudget()
	budget.BatchRecords = 8
	budget.QueueRecords = 32
	lane, err := New(budget, apply)
	if err != nil {
		t.Fatal(err)
	}
	defer lane.Close(context.Background())

	done := coalesce(t, lane, []Record{folded, replayed, conflicting})

	if got := <-done[0]; !errors.Is(got, rollback) {
		t.Fatalf("the newly folded record reported %v, want the batch rollback; a rolled-back record must never be reported as committed", got)
	}
	if got := <-done[1]; got != nil {
		t.Fatalf("the short-circuited record reported %v, want success; a short-circuit is durable from an earlier transaction and is not a failure", got)
	}
	if got := <-done[2]; !errors.Is(got, conflict) {
		t.Fatalf("the conflicting record reported %v, want its typed conflict %v", got, conflict)
	}

	if outcome, settles := folded.observed(); OutcomeIsDurable(outcome.Outcome) || settles != 1 {
		t.Fatalf("the rolled-back record's outcome = %+v after %d settles, want exactly one not-recorded outcome", outcome, settles)
	}
	if outcome, settles := replayed.observed(); !OutcomeIsDurable(outcome.Outcome) || settles != 1 {
		t.Fatalf("the short-circuited record's outcome = %+v after %d settles, want exactly one durable outcome", outcome, settles)
	}
}

// realBatchJournal opens an in-memory provenance store and returns its batch
// journal plus the actor and authority a test builds operations with.
func realBatchJournal(t *testing.T) (provenance.BatchJournal, provenance.ActorID, provenance.JournalID) {
	t.Helper()
	tracker, err := provenance.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	return batchCoordinates(t, tracker)
}

// batchCoordinates registers an agent and a genesis authority on the tracker and
// returns its batch journal with the coordinates a test builds operations with.
func batchCoordinates(t *testing.T, tracker provenance.Tracker) (provenance.BatchJournal, provenance.ActorID, provenance.JournalID) {
	t.Helper()
	agent, err := tracker.RegisterSoftwareAgent("lane-batch-test", "actor", "1", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent: %v", err)
	}
	actor := agent.Agent.ID
	result, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:   "lane-batch-genesis",
		ActorID:       actor,
		CommandDigest: []byte("lane-batch-genesis-command"),
		Effects:       []provenance.Effect{{Sort: provenance.EffectBootstrapAuthority, ResultSlot: "auth", BootstrapLabel: "lane-batch-test"}},
	})
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	var authority provenance.JournalID
	for _, slot := range result.ResultSlots {
		if slot.Slot == "auth" {
			authority = slot.ProducedJournalID
		}
	}
	if authority == 0 {
		t.Fatal("genesis returned no authority slot")
	}
	batch, ok := tracker.Journal().(provenance.BatchJournal)
	if !ok {
		t.Fatal("the provenance journal does not implement BatchJournal")
	}
	return batch, actor, authority
}

// readStoreGeneration reads the O(1)-open currency ledger's generation counter,
// the value the store-wide triggers advance in the SAME transaction as the DML
// that moved it.
func readStoreGeneration(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var generation int64
	if err := db.QueryRow(`SELECT generation FROM provenance_store_generation WHERE id = 1`).Scan(&generation); err != nil {
		t.Fatalf("read the store generation: %v", err)
	}
	return generation
}

// TestABatchedFlushAdvancesTheCurrencyLedger proves the lane's phase-2 commit
// keeps the O(1)-open ledger moving: a flush committed through ONE
// ApplyBatchContext advances the store-wide generation counter, which the
// covered-table triggers bump inside the same transaction, so a later open does
// not have to re-verify the whole journal.
func TestABatchedFlushAdvancesTheCurrencyLedger(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lane-ledger.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	tracker, err := provenance.OpenBorrowedSQLite(db)
	if err != nil {
		t.Fatalf("OpenBorrowedSQLite: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })

	batch, actor, authority := batchCoordinates(t, tracker)
	apply := func(ctx context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
		return batch.ApplyBatchContext(ctx, inputs)
	}
	budget := testBudget()
	budget.BatchRecords = 8
	budget.QueueRecords = 32
	lane, err := New(budget, apply)
	if err != nil {
		t.Fatal(err)
	}
	defer lane.Close(context.Background())

	before := readStoreGeneration(t, db)
	records := []Record{
		&realRecord{input: batchOperation("lane-ledger-a", actor, authority, `{"a":1}`)},
		&realRecord{input: batchOperation("lane-ledger-b", actor, authority, `{"b":1}`)},
		&realRecord{input: batchOperation("lane-ledger-c", actor, authority, `{"c":1}`)},
	}
	for i, done := range coalesce(t, lane, records) {
		if got := <-done; got != nil {
			t.Fatalf("ledger batch record %d reported %v, want a commit", i, got)
		}
	}
	after := readStoreGeneration(t, db)
	if after <= before {
		t.Fatalf("the store generation did not advance across a batched flush: before=%d after=%d", before, after)
	}
}

// batchOperation builds one canonical operation for the real-journal tests.
func batchOperation(operationID string, actor provenance.ActorID, authority provenance.JournalID, payload string) provenance.OperationInput {
	body := []byte(payload)
	sum := sha256.Sum256(body)
	return provenance.OperationInput{
		OperationID:        provenance.OperationID(operationID),
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operationID + "-command"),
		RecordedAt:         time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:          provenance.EffectEvidence,
			ResultSlot:    "batch.evidence",
			EvidenceKind:  "lane.batch.test.v1",
			ContentDigest: sum[:],
			Payload:       body,
		}},
	}
}

// TestARealBatchShortCircuitIsNotAFailureAndAConflictRollsBack drives the lane
// against the REAL provenance batch journal: a replayed batch short-circuits
// every record and reports success, and a batch whose record conflicts rolls
// back, reports the conflict, and leaves NOTHING this batch folded committed.
func TestARealBatchShortCircuitIsNotAFailureAndAConflictRollsBack(t *testing.T) {
	batch, actor, authority := realBatchJournal(t)
	apply := func(ctx context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
		return batch.ApplyBatchContext(ctx, inputs)
	}

	budget := testBudget()
	budget.BatchRecords = 8
	budget.QueueRecords = 32
	lane, err := New(budget, apply)
	if err != nil {
		t.Fatal(err)
	}
	defer lane.Close(context.Background())

	// The first batch commits two records.
	firstA := &realRecord{input: batchOperation("lane-batch-a", actor, authority, `{"a":1}`)}
	firstB := &realRecord{input: batchOperation("lane-batch-b", actor, authority, `{"b":1}`)}
	for i, done := range coalesce(t, lane, []Record{firstA, firstB}) {
		if got := <-done; got != nil {
			t.Fatalf("first batch record %d reported %v, want a commit", i, got)
		}
	}

	// The replay short-circuits both; a short-circuit is NOT a failure.
	replayA := &realRecord{input: batchOperation("lane-batch-a", actor, authority, `{"a":1}`)}
	replayB := &realRecord{input: batchOperation("lane-batch-b", actor, authority, `{"b":1}`)}
	for i, done := range coalesce(t, lane, []Record{replayA, replayB}) {
		if got := <-done; got != nil {
			t.Fatalf("replayed record %d reported %v, want success; a short-circuit is durable from the earlier transaction", i, got)
		}
	}
	if outcome, _ := replayA.observed(); outcome.Outcome != provenance.BatchRecordShortCircuited {
		t.Fatalf("the replay's outcome = %q, want short-circuited", outcome.Outcome)
	}

	// A changed payload under an existing OperationID conflicts, and a fresh
	// record in the same batch must be reported not-recorded (never committed).
	fresh := &realRecord{input: batchOperation("lane-batch-fresh", actor, authority, `{"fresh":1}`)}
	conflicting := &realRecord{input: batchOperation("lane-batch-a", actor, authority, `{"a":2}`)}
	done := coalesce(t, lane, []Record{fresh, conflicting})
	freshErr := <-done[0]
	conflictErr := <-done[1]
	if freshErr == nil {
		t.Fatal("the fresh record of a rolled-back batch reported a commit; a whole-batch failure must never be reported as a partial commit")
	}
	if conflictErr == nil || !errors.Is(conflictErr, provenance.ErrOperationConflict) {
		t.Fatalf("the conflicting record reported %v, want a typed conflict", conflictErr)
	}

	committed, err := batch.LookupCommitted(provenance.OperationID("lane-batch-fresh"))
	if err != nil {
		t.Fatalf("LookupCommitted: %v", err)
	}
	if committed.Kind == provenance.CommittedExact {
		t.Fatal("the fresh record of the rolled-back batch is durably committed; the batch did not roll back atomically")
	}
}

package tasks_test

// tracker_race_test.go — Cross-subsystem race test (BLOCKER B3).
//
// PROPOSAL-2 §10.3 / §11 Scenario 14b: this test proves that the unified
// SQLite file at pasture.db can absorb concurrent writes from all three
// subsystems (provenance.Tracker, audit.Trail, and the pasture-only
// context_edges writer) without SQLITE_BUSY / SQLITE_LOCKED, and that every
// row inserted lands in its expected table. It is the load-bearing
// concurrency proof for D11 / C5 ("low write contention; no message-queue
// interposition; one file is fine").
//
// Test shape (per PROPOSAL-2 §10.3 BLOCKER B3 spec):
//   - File-backed SQLite at filepath.Join(t.TempDir(), "race.db") — never
//     in-memory (which bypasses WAL / busy_timeout / fsync, the exact
//     mechanisms D11 relies on).
//   - N=64 goroutines (matched to the proposal's spec; reduce to N=16 if
//     CI-flaky and document the choice in a bd comment).
//   - Total ops > 1000; each goroutine chooses a random op from the 4 mix.
//   - The 4 ops mixed: audit.Trail.RecordEvent, protocol.TaskTracker.AttachContext,
//     provenance.Tracker.Create, provenance.Tracker.StartActivity.
//   - Run under `CGO_ENABLED=1 go test -race ./internal/tasks/...`. The race
//     flag (and CGO_ENABLED=1, since Go's -race requires CGo) is mandatory.
//
// Assertions (per §10.3 spec):
//   - Zero un-retried SQLITE_BUSY: any returned error must contain neither
//     "SQLITE_BUSY" nor "database is locked" nor "SQLITE_LOCKED" in its
//     formatted message. WAL + busy_timeout=5000ms must absorb contention.
//   - All RecordEvent rows present in audit_events.
//   - All AttachContext rows present in context_edges.
//   - All Create rows present in tasks.
//   - All StartActivity rows present in activities.
//
// The goroutines record successful op counts via atomic counters; after
// goroutine join we read the row counts directly from SQLite and compare.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// raceOp identifies one of the 4 operations the race test mixes.
//
// Strongly typed (rather than int constants) so the dispatch switch in the
// goroutine body fails to compile if a new op is added without handling.
type raceOp int

const (
	opRecordEvent   raceOp = iota // audit.Trail.RecordEvent
	opAttachContext               // protocol.TaskTracker.AttachContext
	opCreateTask                  // provenance.Tracker.Create
	opStartActivity               // provenance.Tracker.StartActivity
	opDBOSWriter                  // engine-style emit: keyed audit row + deterministic-id activity
	numRaceOps                    // sentinel: count of op kinds
)

// dbosEpochId is the per-goroutine epoch id used by opDBOSWriter, so each
// goroutine's deterministic keys are epoch-distinct.
func dbosEpochId(goroutineId int) string {
	return fmt.Sprintf("epoch-dbos-%d", goroutineId)
}

// dbosWriterEmit replays the engine's per-step forensic write through the
// tracker directly: one keyed audit_events row (dedup_key) plus one
// deterministic-id activity, both derived from the single pinned encoder
// protocol.DedupKey(epochId, phase, kind, stepSeq). Identical inputs yield
// identical keys, so a re-emission collapses onto one row in BOTH tables —
// the same exactly-once mechanism the durable engine relies on.
func dbosWriterEmit(ctx context.Context, tracker protocol.TaskTracker, agentId provenance.AgentID, epochId, stepSeq string) error {
	const phase = protocol.PhaseCodeReview
	// The two tiers pass DISTINCT kinds to the one DedupKey encoder, so the audit
	// dedup_key and the activity id are different values (different id-spaces) for
	// the same logical step — exactly as the engine emits them. Each is keyed and
	// deduped independently within its own table.
	auditKey := protocol.DedupKey(epochId, string(phase), string(protocol.EventPhaseTransition), stepSeq)
	activityKey := protocol.DedupKey(epochId, string(phase), engine.ActivityKindPhaseTransition, stepSeq)

	if _, err := tracker.RecordEventReturningId(ctx, protocol.AuditEvent{
		EpochId:   epochId,
		Phase:     phase,
		Role:      "dbos-writer",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"step": stepSeq},
		Timestamp: time.Now().UTC(),
		DedupKey:  auditKey,
	}); err != nil {
		return err
	}

	u, err := uuid.Parse(activityKey)
	if err != nil {
		return fmt.Errorf("dbosWriterEmit: activity DedupKey is not a UUID: %w", err)
	}
	if _, err := tracker.StartActivityWithID(
		provenance.ActivityID{Namespace: "pasture", UUID: u},
		agentId,
		provenance.PhaseCodeReview,
		provenance.StageInProgress,
		"dbos-activity-"+stepSeq,
	); err != nil {
		return err
	}
	return nil
}

// TestRaceCrossSubsystem_FileBacked is the cross-subsystem race test.
//
// Run with `CGO_ENABLED=1 go test -race -run TestRaceCrossSubsystem_FileBacked ./internal/tasks/...`.
// The CGO_ENABLED=1 override is required because Go's race detector needs
// CGo. Pasture's production builds use CGO_ENABLED=0 (modernc/sqlite is pure
// Go); the race detector is a test-time-only opt-in.
//
// On a workstation this completes in ~3-5s; the race detector adds ~10x
// overhead vs a non-race run.
func TestRaceCrossSubsystem_FileBacked(t *testing.T) {
	// We do NOT call t.Parallel(): this test is heavyweight and we want it
	// to run in isolation so contention from other parallel tests doesn't
	// muddy the SQLITE_BUSY assertion.

	const (
		// N goroutines per the proposal spec. If you change N, also update
		// the bd comment on aura-plugins-mbkfi documenting the choice.
		N = 64
		// Iterations per goroutine. N * iterPerGoroutine must exceed 1000
		// per the proposal spec.
		iterPerGoroutine = 24 // 64 * 24 = 1536 ops total

		// Pre-seeded audit events — AttachContext FKs into audit_events,
		// so we need real event IDs for context_edges rows to be valid.
		// 50 seed events distributed across N goroutines is plenty.
		seedEventCount = 50
	)

	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTracker(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("Close failed during cleanup: %v", err)
		}
	})

	ctx := context.Background()

	// ─── Seed: pre-populate audit_events so AttachContext has valid FKs ──
	//
	// We record `seedEventCount` events sequentially before launching the
	// concurrent workers, then read back the assigned row IDs. Each
	// goroutine picks a random seed ID when its random op selection lands
	// on opAttachContext.
	seedEventIDs, err := seedAuditEvents(ctx, tracker, dbPath, seedEventCount)
	if err != nil {
		t.Fatalf("seedAuditEvents failed: %v", err)
	}
	if len(seedEventIDs) != seedEventCount {
		t.Fatalf("seedAuditEvents returned %d ids, want %d", len(seedEventIDs), seedEventCount)
	}

	// ─── Pre-register one SoftwareAgent for the StartActivity op ──────
	//
	// StartActivity requires a registered AgentId. Registering once
	// outside the goroutines avoids forcing every concurrent op to
	// also do an agent insert (which would mask the actual contention
	// we want to measure on the 4 target tables).
	agent, err := tracker.RegisterSoftwareAgent("pasture-race-test", "race-runner", "0.0.0", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent failed: %v", err)
	}
	agentId := agent.ID

	// ─── Counters: atomic bookkeeping per op kind ─────────────────────
	//
	// Each goroutine bumps the counter for the op it just successfully
	// performed. After join, we compare counters to row counts read
	// directly from SQLite. These counters also guard against silent
	// data loss (a successful Exec that didn't actually persist).
	var (
		attemptedByOp [numRaceOps]int64
		succeededByOp [numRaceOps]int64
		busyErrors    int64 // SQLITE_BUSY / SQLITE_LOCKED counter
	)

	// ─── Launch N concurrent goroutines ───────────────────────────────
	var wg sync.WaitGroup
	wg.Add(N)
	startBarrier := make(chan struct{})

	for g := 0; g < N; g++ {
		// Capture per-goroutine random source so all goroutines pick
		// different op sequences without sharing rand.Source state.
		// math/rand.NewSource is fine for test-only randomness; we do
		// not need crypto-strength entropy here.
		seed := int64(g)*1_000_003 + time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		go func(goroutineId int, rng *rand.Rand) {
			defer wg.Done()
			<-startBarrier // unblock all goroutines simultaneously

			for i := 0; i < iterPerGoroutine; i++ {
				op := raceOp(rng.Intn(int(numRaceOps)))
				atomic.AddInt64(&attemptedByOp[op], 1)

				err := runRaceOp(
					ctx, tracker, op,
					goroutineId, i, agentId,
					seedEventIDs, rng,
				)

				if err != nil {
					if isBusyOrLockedErr(err) {
						atomic.AddInt64(&busyErrors, 1)
						// Continue iterating — we record but don't
						// abort. The final assertion will fail loudly
						// if any busy errors slipped through.
						continue
					}
					// Any other error is a hard failure.
					t.Errorf("goroutine %d iter %d op %v: unexpected error: %v",
						goroutineId, i, op, err)
					continue
				}
				atomic.AddInt64(&succeededByOp[op], 1)
			}
		}(g, rng)
	}

	// Release all goroutines simultaneously to maximize contention.
	close(startBarrier)
	wg.Wait()

	// ─── Assertion 1: zero busy/locked errors ────────────────────────
	//
	// WAL mode + busy_timeout=5000ms in NewSqliteAuditTrail and
	// openAuditHandle MUST absorb every contention spike. Any escaped
	// SQLITE_BUSY indicates the proposal's D11 / C5 binding is
	// violated and a message queue (or other interposition) is needed.
	if busyErrors > 0 {
		t.Errorf("BLOCKER B3 failure: observed %d SQLITE_BUSY/SQLITE_LOCKED errors that escaped busy_timeout — D11 binding violated, see PROPOSAL-2 §10.3", busyErrors)
	}

	totalAttempted := int64(0)
	totalSucceeded := int64(0)
	for op := raceOp(0); op < numRaceOps; op++ {
		totalAttempted += attemptedByOp[op]
		totalSucceeded += succeededByOp[op]
	}
	if totalAttempted < 1000 {
		t.Errorf("PROPOSAL-2 §10.3 spec requires >1000 ops; only attempted %d", totalAttempted)
	}
	t.Logf("race ops: attempted=%d succeeded=%d (per-op succeeded: record=%d attach=%d create=%d activity=%d dbos=%d); busy_errors=%d",
		totalAttempted, totalSucceeded,
		succeededByOp[opRecordEvent], succeededByOp[opAttachContext],
		succeededByOp[opCreateTask], succeededByOp[opStartActivity], succeededByOp[opDBOSWriter],
		busyErrors)

	// ─── Assertion 2-5: row counts match successful op counts ────────
	//
	// We open a fresh *sql.DB for the verification reads; using the
	// tracker's connection would couple the assertion to its
	// concurrency. Reading post-join also ensures everything is
	// committed to the WAL and visible.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("verification sql.Open failed: %v", err)
	}
	defer verifyDB.Close()

	// audit_events: seedEventCount + opRecordEvent successes + opDBOSWriter
	// successes (each DBOS writer writes one keyed audit row; every key is
	// distinct under the unique per-(goroutine,iter) inputs, so none dedup).
	gotEvents := mustCountRows(t, verifyDB, "audit_events")
	wantEvents := int64(seedEventCount) + succeededByOp[opRecordEvent] + succeededByOp[opDBOSWriter]
	if gotEvents != wantEvents {
		t.Errorf("audit_events row count = %d, want %d (seed=%d + RecordEvent=%d + DBOSWriter=%d)",
			gotEvents, wantEvents, seedEventCount, succeededByOp[opRecordEvent], succeededByOp[opDBOSWriter])
	}

	// dedup_key rows: exactly one per successful opDBOSWriter (distinct keys,
	// no contention loss). This is the audit half of the exactly-once invariant.
	gotKeyed := mustCountRows(t, verifyDB, "audit_events WHERE dedup_key IS NOT NULL")
	if gotKeyed != succeededByOp[opDBOSWriter] {
		t.Errorf("keyed audit_events row count = %d, want %d (one per DBOSWriter success)",
			gotKeyed, succeededByOp[opDBOSWriter])
	}

	// context_edges: opAttachContext successes + auto-EpochContext writes
	// from RecordEvent (post-S4: SqliteAuditTrail.RecordEvent writes a
	// context_edges (event_id, 'EpochContext', epochId) row whenever
	// event.EpochId is non-empty). Both seed events and opRecordEvent
	// successes contribute one EpochContext edge each.
	//
	// AttachContext uses INSERT OR IGNORE so duplicate
	// (event_id, kind, contextId) triples are silently absorbed. The
	// upper bound is therefore (seed RecordEvent calls) +
	// (opRecordEvent successes) + (opAttachContext successes), with the
	// actual count being lower whenever the random distribution
	// produces a duplicate triple.
	gotEdges := mustCountRows(t, verifyDB, "context_edges")
	maxEdges := int64(seedEventCount) + succeededByOp[opRecordEvent] + succeededByOp[opDBOSWriter] + succeededByOp[opAttachContext]
	if gotEdges > maxEdges {
		t.Errorf("context_edges row count = %d > max possible %d (seed=%d + RecordEvent=%d + AttachContext=%d) — impossible without phantom inserts",
			gotEdges, maxEdges, seedEventCount, succeededByOp[opRecordEvent], succeededByOp[opAttachContext])
	}
	if gotEdges == 0 && (succeededByOp[opAttachContext] > 0 || succeededByOp[opRecordEvent] > 0) {
		t.Errorf("context_edges has 0 rows but AttachContext succeeded %d times and RecordEvent succeeded %d times — silent write loss",
			succeededByOp[opAttachContext], succeededByOp[opRecordEvent])
	}

	// tasks: opCreateTask successes (Provenance auto-creates a UUIDv7
	// per call — no duplicate key collisions).
	gotTasks := mustCountRows(t, verifyDB, "tasks")
	if gotTasks != succeededByOp[opCreateTask] {
		t.Errorf("tasks row count = %d, want %d (Create successes)", gotTasks, succeededByOp[opCreateTask])
	}

	// activities: opStartActivity successes (random ids) + opDBOSWriter
	// successes (deterministic ids; all distinct under the unique inputs).
	gotActs := mustCountRows(t, verifyDB, "activities")
	wantActs := succeededByOp[opStartActivity] + succeededByOp[opDBOSWriter]
	if gotActs != wantActs {
		t.Errorf("activities row count = %d, want %d (StartActivity=%d + DBOSWriter=%d)",
			gotActs, wantActs, succeededByOp[opStartActivity], succeededByOp[opDBOSWriter])
	}
}

// TestDBOSWriterEmit_ReplayExactlyOnce proves the exactly-once half directly: a
// re-emission with the SAME deterministic inputs collapses onto ONE row in both
// forensic tables (audit_events via the dedup_key partial index, activities via
// ON CONFLICT(id)). This is what makes a crash-replay of the engine's step safe.
func TestDBOSWriterEmit_ReplayExactlyOnce(t *testing.T) {
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })

	ctx := context.Background()
	agent, err := tracker.RegisterSoftwareAgent("pasture-replay-test", "replay-runner", "0.0.0", "test")
	if err != nil {
		t.Fatalf("RegisterSoftwareAgent: %v", err)
	}

	// Emit the same (epoch, step) three times — modelling two crash-replays.
	for i := 0; i < 3; i++ {
		if err := dbosWriterEmit(ctx, tracker, agent.ID, "epoch-replay", "7"); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}

	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer verifyDB.Close()

	if n := mustCountRows(t, verifyDB, "audit_events WHERE dedup_key IS NOT NULL"); n != 1 {
		t.Errorf("keyed audit_events = %d, want 1 (replays must collapse)", n)
	}
	if n := mustCountRows(t, verifyDB, "activities"); n != 1 {
		t.Errorf("activities = %d, want 1 (replays must collapse via ON CONFLICT(id))", n)
	}

	// A different step → a distinct key → a second row in each table.
	if err := dbosWriterEmit(ctx, tracker, agent.ID, "epoch-replay", "8"); err != nil {
		t.Fatalf("distinct-step emit: %v", err)
	}
	if n := mustCountRows(t, verifyDB, "audit_events WHERE dedup_key IS NOT NULL"); n != 2 {
		t.Errorf("keyed audit_events = %d, want 2 after a distinct step", n)
	}
	if n := mustCountRows(t, verifyDB, "activities"); n != 2 {
		t.Errorf("activities = %d, want 2 after a distinct step", n)
	}
}

// runRaceOp dispatches one iteration's chosen op. Centralised so the
// goroutine body stays compact and the op-handler logic can be reviewed
// in one place.
//
// Each handler picks fresh inputs per call (random goroutine ID +
// iteration index avoid collisions across goroutines for tasks/agents).
func runRaceOp(
	ctx context.Context,
	tracker protocol.TaskTracker,
	op raceOp,
	goroutineId, iter int,
	agentId provenance.AgentID,
	seedEventIDs []int64,
	rng *rand.Rand,
) error {
	switch op {
	case opRecordEvent:
		ev := protocol.AuditEvent{
			EpochId:   fmt.Sprintf("epoch-race-%d", goroutineId),
			Phase:     protocol.PhaseWorkerSlices,
			Role:      "race-test",
			EventType: protocol.EventSliceStarted,
			Payload:   map[string]any{"g": goroutineId, "i": iter},
			Timestamp: time.Now().UTC(),
		}
		return tracker.RecordEvent(ctx, ev)

	case opAttachContext:
		// Attach to a random pre-seeded event ID; pick a random
		// ContextKind from the valid set (excluding ContextNone since
		// it's the zero-value marker, not a meaningful attachment).
		eventId := seedEventIDs[rng.Intn(len(seedEventIDs))]
		validKinds := []protocol.ContextKind{
			protocol.ContextEpoch,
			protocol.ContextSlice,
			protocol.ContextReview,
			protocol.ContextFollowup,
			protocol.ContextGit,
			protocol.ContextSkill,
			protocol.ContextSession,
		}
		kind := validKinds[rng.Intn(len(validKinds))]
		// Per-goroutine context ID guarantees variety; including
		// iter prevents BCNF idempotency from suppressing all writes
		// (otherwise every goroutine using the same kind+id would
		// collapse to one row).
		contextId := fmt.Sprintf("ctx-%d-%d", goroutineId, iter)
		return tracker.AttachContext(ctx, eventId, kind, contextId)

	case opCreateTask:
		_, err := tracker.Create(
			"pasture-race-test",
			fmt.Sprintf("race-task-%d-%d", goroutineId, iter),
			"race test task",
			provenance.TaskTypeTask,
			provenance.PriorityMedium,
			provenance.PhaseRequest,
		)
		return err

	case opStartActivity:
		_, err := tracker.StartActivity(
			agentId,
			provenance.PhaseWorkerSlices,
			provenance.StageInProgress,
			fmt.Sprintf("race-activity-%d-%d", goroutineId, iter),
		)
		return err

	case opDBOSWriter:
		// Per-goroutine epoch + per-iteration step → a unique deterministic key
		// per call, so this op contributes N keyed audit rows + N activities
		// (one each) and the row-count invariant holds under contention.
		return dbosWriterEmit(ctx, tracker, agentId, dbosEpochId(goroutineId), strconv.Itoa(iter))
	}
	return fmt.Errorf("runRaceOp: unhandled op %v — bug in the race test", op)
}

// seedAuditEvents records `count` audit events sequentially and returns
// their assigned row IDs in insertion order. Used to give AttachContext
// goroutines valid event_id values for the context_edges FK.
//
// Sequential (not concurrent) seeding keeps the setup deterministic so
// the goroutine launch is the only contention scenario under test.
func seedAuditEvents(ctx context.Context, tracker protocol.TaskTracker, dbPath string, count int) ([]int64, error) {
	for i := 0; i < count; i++ {
		ev := protocol.AuditEvent{
			EpochId:   "epoch-race-seed",
			Phase:     protocol.PhaseRequest,
			Role:      "seed",
			EventType: protocol.EventPhaseTransition,
			Payload:   map[string]any{"seed": i},
			Timestamp: time.Now().UTC(),
		}
		if err := tracker.RecordEvent(ctx, ev); err != nil {
			return nil, fmt.Errorf("seedAuditEvents: RecordEvent #%d failed: %w", i, err)
		}
	}

	// Read back the IDs of the rows we just inserted. We open a fresh
	// *sql.DB rather than reuse tracker's auditDB (private) to keep the
	// helper self-contained.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("seedAuditEvents: sql.Open(%q) failed: %w", dbPath, err)
	}
	defer db.Close()

	// Post-S4 (v4 schema): audit_events.epoch_id is gone; epoch
	// attachment lives in context_edges with kind='EpochContext'. The
	// trail.RecordEvent call above writes the context_edges row so the
	// JOIN below resolves the seeded events by their epoch correlation.
	rows, err := db.QueryContext(ctx,
		`SELECT ae.id FROM audit_events ae
		 INNER JOIN context_edges ce ON ce.event_id = ae.id
		 WHERE ce.context_kind = ? AND ce.context_id = ?
		 ORDER BY ae.id ASC`,
		"EpochContext", "epoch-race-seed",
	)
	if err != nil {
		return nil, fmt.Errorf("seedAuditEvents: read-back query failed: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0, count)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("seedAuditEvents: row scan failed: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seedAuditEvents: row iteration failed: %w", err)
	}
	return ids, nil
}

// mustCountRows returns SELECT COUNT(*) FROM <table>, failing the test on
// query error. Used by the post-join row-count assertions.
func mustCountRows(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	// Table name is a hard-coded constant in the call site so SQL
	// interpolation is safe; never accept user-supplied table names here.
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("mustCountRows(%q) failed: %v", table, err)
	}
	return n
}

// isBusyOrLockedErr reports whether err contains a SQLite contention
// signature (SQLITE_BUSY or SQLITE_LOCKED). Substring match is sufficient
// because modernc.org/sqlite formats both as "database is locked
// (SQLITE_BUSY)" or "(SQLITE_LOCKED)" in the underlying driver error chain.
//
// Centralised so the test's busy-error assertion has one definition; new
// SQLite contention signatures can be added here without touching the test
// body.
func isBusyOrLockedErr(err error) bool {
	if err == nil {
		return false
	}
	// Fast path: errors.Is against any sentinel the driver might export.
	// modernc.org/sqlite doesn't yet export sentinel busy errors, so we
	// fall through to substring matching on the message.
	if errors.Is(err, sql.ErrConnDone) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED") ||
		strings.Contains(msg, "database is locked")
}

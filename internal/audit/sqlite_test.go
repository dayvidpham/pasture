package audit_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers "sqlite" with database/sql)
)

func newTestSqliteTrail(t *testing.T) (*audit.SqliteAuditTrail, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_audit.db")
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = trail.Close() })
	return trail, dbPath
}

func TestSqliteAuditTrail_Suite(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	runTrailSuite(t, trail)
}

// TestSqliteAuditTrail_Durability verifies that events survive a close/reopen
// cycle — the core "survives kill-restart" acceptance criterion.
func TestSqliteAuditTrail_Durability(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "durable.db")
	ctx := context.Background()

	// Phase 1: write events and close.
	trail1, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}

	ev1 := makeEvent("dur-epoch", protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)
	ev2 := makeEvent("dur-epoch", protocol.PhaseElicit, "worker", protocol.EventVoteRecorded)

	if err := trail1.RecordEvent(ctx, ev1); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := trail1.RecordEvent(ctx, ev2); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := trail1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase 2: reopen and verify events are present.
	trail2, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail (reopen): %v", err)
	}
	defer trail2.Close()

	got, err := trail2.QueryEvents(ctx, "dur-epoch", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events after reopen, got %d", len(got))
	}
	if got[0].Phase != protocol.PhaseRequest {
		t.Errorf("event[0]: want phase %q, got %q", protocol.PhaseRequest, got[0].Phase)
	}
	if got[1].Phase != protocol.PhaseElicit {
		t.Errorf("event[1]: want phase %q, got %q", protocol.PhaseElicit, got[1].Phase)
	}
}

// TestSqliteAuditTrail_ParentDirCreation verifies that NewSqliteAuditTrail
// creates intermediate directories when the parent does not exist.
func TestSqliteAuditTrail_ParentDirCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "sub", "audit.db")

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail with missing parent: %v", err)
	}
	defer trail.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file not created at %q: %v", dbPath, err)
	}
}

// TestSqliteAuditTrail_ConcurrentAccess verifies thread-safety under parallel
// writes. SQLite with WAL mode serialises writers without data loss.
func TestSqliteAuditTrail_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	ctx := context.Background()

	const goroutines = 10
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range eventsPerGoroutine {
				ev := protocol.AuditEvent{
					EpochId:   "concurrent-sqlite",
					Phase:     protocol.PhaseWorkerSlices,
					Role:      "worker",
					EventType: protocol.EventSliceStarted,
					Payload:   map[string]any{"goroutine": idx, "seq": j},
					Timestamp: time.Now().UTC(),
				}
				if err := trail.RecordEvent(ctx, ev); err != nil {
					t.Errorf("goroutine %d: RecordEvent: %v", idx, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	got, err := trail.QueryEvents(ctx, "concurrent-sqlite", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	want := goroutines * eventsPerGoroutine
	if len(got) != want {
		t.Errorf("want %d events, got %d", want, len(got))
	}
}

func TestSqliteAuditTrail_SessionEntrySuite(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	runSessionEntrySuite(t, trail)
}

// TestSqliteAuditTrail_SessionEntryDurability verifies session entries survive
// a close/reopen cycle — the SQLite-specific persistence contract.
func TestSqliteAuditTrail_SessionEntryDurability(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "session_dur.db")
	ctx := context.Background()

	// Phase 1: write entries and close.
	trail1, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}

	entries := []protocol.SessionEntry{
		makeSessionEntry("dur-session", 0, "user"),
		makeSessionEntry("dur-session", 1, "assistant"),
	}
	if err := trail1.RecordSessionEntries(ctx, entries); err != nil {
		t.Fatalf("RecordSessionEntries: %v", err)
	}
	if err := trail1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase 2: reopen and verify entries are present.
	trail2, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail (reopen): %v", err)
	}
	defer trail2.Close()

	got, err := trail2.QuerySessionEntries(ctx, "dur-session")
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries after reopen, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Errorf("entry[0]: want role %q, got %q", "user", got[0].Role)
	}
	if got[1].Role != "assistant" {
		t.Errorf("entry[1]: want role %q, got %q", "assistant", got[1].Role)
	}
}

// TestSqliteAuditTrail_RecordEventReturningId_Suite runs the shared
// RecordEventReturningId contract suite against the SQLite-backed trail.
func TestSqliteAuditTrail_RecordEventReturningId_Suite(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	runRecordEventReturningIdSuite(t, trail)
}

// TestSqliteAuditTrail_RecordEventReturningId_MatchesRowId verifies that the
// id returned by RecordEventReturningId equals the actual id column of the
// inserted audit_events row. This is the core LastInsertId guarantee.
func TestSqliteAuditTrail_RecordEventReturningId_MatchesRowId(t *testing.T) {
	t.Parallel()
	trail, dbPath := newTestSqliteTrail(t)
	ctx := context.Background()

	ev := makeEvent("matchid-epoch", protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)
	returnedId, err := trail.RecordEventReturningId(ctx, ev)
	if err != nil {
		t.Fatalf("RecordEventReturningId: %v", err)
	}

	// Open a fresh handle on the same file and look the row up by id directly.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	var actualId int64
	var eventType string
	err = verifyDB.QueryRow(
		`SELECT id, event_type FROM audit_events WHERE id = ?`,
		returnedId,
	).Scan(&actualId, &eventType)
	if err != nil {
		t.Fatalf("verify row id=%d exists: %v", returnedId, err)
	}
	if actualId != returnedId {
		t.Errorf("actual id %d != returned id %d", actualId, returnedId)
	}
	if eventType != string(protocol.EventPhaseTransition) {
		t.Errorf("event_type for id=%d: got %q, want %q", returnedId, eventType, string(protocol.EventPhaseTransition))
	}
}

// TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique is the Phase
// 11 R1-B regression test. Under N concurrent goroutines all calling
// RecordEventReturningId against the SAME trail handle, EVERY returned id
// MUST be unique AND MUST correspond to a real audit_events row with a
// matching id column. This is the property that the SELECT MAX(id)
// workaround (now removed) failed to provide — concurrent SELECT MAX(id)
// after independent INSERTs could observe a row written by a DIFFERENT
// goroutine and hand the same id to two callers.
//
// If this test ever fails, the LastInsertId path has regressed.
func TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique(t *testing.T) {
	t.Parallel()
	trail, dbPath := newTestSqliteTrail(t)
	ctx := context.Background()

	const N = 32
	var wg sync.WaitGroup
	idCh := make(chan int64, N)
	errCh := make(chan error, N)

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ev := protocol.AuditEvent{
				EpochId:   "concurrent-unique-epoch",
				Phase:     protocol.PhaseWorkerSlices,
				Role:      "worker",
				EventType: protocol.EventSliceStarted,
				Payload:   map[string]any{"goroutine": idx},
				Timestamp: time.Now().UTC(),
			}
			id, err := trail.RecordEventReturningId(ctx, ev)
			if err != nil {
				errCh <- err
				return
			}
			idCh <- id
		}(i)
	}
	wg.Wait()
	close(idCh)
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent RecordEventReturningId error: %v", err)
	}

	// Collect ids and assert per-goroutine uniqueness — the property the
	// SELECT MAX(id) workaround was unable to guarantee.
	seen := make(map[int64]int)
	var ids []int64
	for id := range idCh {
		if id <= 0 {
			t.Errorf("RecordEventReturningId returned non-positive id %d", id)
			continue
		}
		seen[id]++
		ids = append(ids, id)
	}
	if len(ids) != N {
		t.Fatalf("expected %d ids, got %d", N, len(ids))
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("id %d returned to %d goroutines (must be unique per call)", id, count)
		}
	}

	// And each returned id MUST resolve to a real row with the matching id
	// in audit_events on disk. This rules out a scenario where the ids are
	// unique but unrelated to the rows actually written.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verification handle: %v", err)
	}
	defer verifyDB.Close()

	var totalRows int
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, string(protocol.EventSliceStarted)).Scan(&totalRows); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if totalRows != N {
		t.Errorf("audit_events count = %d, want %d", totalRows, N)
	}
	for _, id := range ids {
		var rowExists int
		if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id = ?`, id).Scan(&rowExists); err != nil {
			t.Fatalf("verify id %d exists: %v", id, err)
		}
		if rowExists != 1 {
			t.Errorf("returned id %d resolves to %d rows in audit_events (want exactly 1)", id, rowExists)
		}
	}
}

// TestSqliteAuditTrail_RecordEventReturningId_RejectsEmptyRole verifies that
// the new method preserves the validation contract from the original
// RecordEvent — empty Role still returns CategoryValidation.
func TestSqliteAuditTrail_RecordEventReturningId_RejectsEmptyRole(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	ctx := context.Background()

	ev := protocol.AuditEvent{
		EpochId:   "validation-epoch",
		Phase:     protocol.PhaseRequest,
		Role:      "", // empty — must be rejected
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{},
		Timestamp: time.Now().UTC(),
	}
	id, err := trail.RecordEventReturningId(ctx, ev)
	if err == nil {
		t.Fatalf("RecordEventReturningId with empty Role: want error, got id=%d nil", id)
	}
	if id != 0 {
		t.Errorf("on validation failure want id=0, got id=%d", id)
	}
}

// TestSqliteAuditTrail_PreservesChronologicalOrder verifies that rows come back
// in insertion order (ascending id), not arbitrary order.
func TestSqliteAuditTrail_PreservesChronologicalOrder(t *testing.T) {
	t.Parallel()
	trail, _ := newTestSqliteTrail(t)
	ctx := context.Background()

	phases := []protocol.PhaseId{protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose}
	for _, ph := range phases {
		ev := makeEvent("order-sqlite", ph, "supervisor", protocol.EventPhaseTransition)
		if err := trail.RecordEvent(ctx, ev); err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
	}

	got, err := trail.QueryEvents(ctx, "order-sqlite", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	for i, ph := range phases {
		if got[i].Phase != ph {
			t.Errorf("event[%d]: want %q, got %q", i, ph, got[i].Phase)
		}
	}
}

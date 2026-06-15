// Package audit_test — migrate_v4_v5_test.go
//
// File-backed tests for the v4→v5 migration. Coverage:
//
//   - Non-destructive: rows seeded at v4 survive with dedup_key NULL.
//   - Version bookkeeping: post-Migrate, MAX(version) = 5.
//   - The partial unique index idx_audit_events_dedup exists.
//   - Legacy NULL coexistence: multiple RecordEvent writes (which leave
//     dedup_key NULL) all insert — the partial index does not reject NULLs.
//   - Engine dedup: two RecordEvent writes with the SAME dedup_key collapse to
//     one row; distinct keys produce distinct rows.
//
// All tests are file-backed via t.TempDir() per pasture/CLAUDE.md (in-memory
// SQLite would bypass WAL/busy_timeout/fsync behaviour the engine relies on).
package audit_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"

	_ "modernc.org/sqlite"
)

// seedV4DB hand-builds a database at audit schema version 4: the post-v4
// audit_events shape (no epoch_id, no role; agent_id NOT NULL) plus an
// audit_schema_meta row stamped at version 4 and two seeded rows. Returns the
// two seeded row ids.
func seedV4DB(t *testing.T, dbPath string) (int64, int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open seed: %v", err)
	}
	defer db.Close()

	mustExec(t, db, `
		CREATE TABLE audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			phase      TEXT,
			agent_id   TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload    TEXT NOT NULL,
			timestamp  INTEGER NOT NULL
		)`)
	mustExec(t, db, `CREATE INDEX idx_audit_events_agent ON audit_events (agent_id)`)
	mustExec(t, db, `CREATE INDEX idx_audit_events_timestamp ON audit_events (timestamp)`)
	mustExec(t, db, `
		CREATE TABLE audit_schema_meta (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`)
	mustExec(t, db, `INSERT INTO audit_schema_meta (version, applied_at) VALUES (4, ?)`, time.Now().UnixNano())

	seed := func(eventType string) int64 {
		res, err := db.Exec(
			`INSERT INTO audit_events (phase, agent_id, event_type, payload, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			"code-review", "agent-x", eventType, `{"note":"survives v4 to v5"}`, time.Now().UnixNano(),
		)
		if err != nil {
			t.Fatalf("seed audit_events insert: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed LastInsertId: %v", err)
		}
		return id
	}
	return seed("PhaseTransition"), seed("VoteRecorded")
}

func TestMigrateV4toV5_NonDestructive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	id1, id2 := seedV4DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("Migrate v4→v5: %v", err)
	}

	// Version advanced to 5.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 5 {
		t.Errorf("schema version = %d, want 5", version)
	}

	// Both seeded rows survive, with dedup_key NULL.
	for _, id := range []int64{id1, id2} {
		var dedup sql.NullString
		if err := db.QueryRow(`SELECT dedup_key FROM audit_events WHERE id = ?`, id).Scan(&dedup); err != nil {
			t.Fatalf("read seeded row %d: %v", id, err)
		}
		if dedup.Valid {
			t.Errorf("row %d dedup_key = %q, want NULL", id, dedup.String)
		}
	}

	// The partial unique index exists.
	if !indexExists(t, db, "idx_audit_events_dedup") {
		t.Error("idx_audit_events_dedup does not exist after migration")
	}
}

func TestMigrateV4toV5_LegacyNullCoexistence(t *testing.T) {
	// A fresh file migrates v1→v5, creating the full schema (incl. the agent
	// registry RecordEvent attributes through). The dedup_key column + partial
	// index land in the v4→v5 step.
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Open via the trail so the migrator runs to v5 and the legacy write path
	// is exercised. RecordEvent leaves dedup_key NULL.
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}
	defer trail.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ev := protocol.AuditEvent{
			EpochId:   "epoch-legacy",
			Phase:     protocol.PhaseCodeReview,
			Role:      "supervisor",
			EventType: protocol.EventPhaseTransition,
			Payload:   map[string]any{"i": i},
			Timestamp: time.Now(),
		}
		if err := trail.RecordEvent(ctx, ev); err != nil {
			t.Fatalf("RecordEvent %d: %v", i, err)
		}
	}

	// The partial unique index must NOT reject multiple NULL dedup_key rows.
	db := openDB(t, dbPath)
	var nullCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE dedup_key IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count NULL rows: %v", err)
	}
	// 3 legacy RecordEvent writes → 3 NULL-keyed rows all coexist.
	if nullCount != 3 {
		t.Errorf("NULL-keyed row count = %d, want 3 (multiple NULLs must coexist under the partial index)", nullCount)
	}
}

func TestMigrateV4toV5_EngineDedupExactlyOnce(t *testing.T) {
	// A fresh file migrates v1→v5, creating the full schema (incl. the agent
	// registry RecordEvent attributes through). The dedup_key column + partial
	// index land in the v4→v5 step.
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}
	defer trail.Close()
	ctx := context.Background()

	key := protocol.DedupKey("epoch-1", "code-review", "PhaseTransition", "1")
	emit := func() (int64, error) {
		return trail.RecordEventReturningId(ctx, protocol.AuditEvent{
			EpochId:   "epoch-1",
			Phase:     protocol.PhaseCodeReview,
			Role:      "supervisor",
			EventType: protocol.EventPhaseTransition,
			Payload:   map[string]any{"k": 1},
			Timestamp: time.Now(),
			DedupKey:  key,
		})
	}

	id1, err := emit()
	if err != nil {
		t.Fatalf("first emit: %v", err)
	}
	id2, err := emit() // replay: same dedup_key
	if err != nil {
		t.Fatalf("replay emit: %v", err)
	}
	if id1 != id2 {
		t.Errorf("replay returned a different id (%d vs %d); dedup must resolve the same row", id1, id2)
	}

	// A different key produces a distinct row.
	otherKey := protocol.DedupKey("epoch-2", "code-review", "PhaseTransition", "1")
	id3, err := trail.RecordEventReturningId(ctx, protocol.AuditEvent{
		EpochId:   "epoch-2",
		Phase:     protocol.PhaseCodeReview,
		Role:      "supervisor",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"k": 1},
		Timestamp: time.Now(),
		DedupKey:  otherKey,
	})
	if err != nil {
		t.Fatalf("distinct-key emit: %v", err)
	}
	if id3 == id1 {
		t.Errorf("distinct dedup_key collapsed onto the same row id %d", id3)
	}

	db := openDB(t, dbPath)
	var keyed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE dedup_key IS NOT NULL`).Scan(&keyed); err != nil {
		t.Fatalf("count keyed rows: %v", err)
	}
	if keyed != 2 {
		t.Errorf("engine-keyed row count = %d, want 2 (one per distinct key after a replay)", keyed)
	}
}

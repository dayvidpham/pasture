package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, CGO_ENABLED=0 compatible
)

// SqliteAuditTrail is a Trail implementation backed by a SQLite database.
//
// Uses modernc.org/sqlite (pure Go, CGO_ENABLED=0 compatible — no C toolchain
// required). Events survive process restarts. The database file and any
// intermediate parent directories are created on first open.
//
// Schema (audit_events table):
//
//	id         INTEGER PRIMARY KEY AUTOINCREMENT
//	epoch_id   TEXT NOT NULL
//	phase      TEXT NOT NULL   (protocol.PhaseId string value)
//	role       TEXT NOT NULL   (string role identifier)
//	event_type TEXT NOT NULL   (protocol.EventType string value)
//	payload    TEXT NOT NULL   (JSON-encoded map[string]any)
//	timestamp  INTEGER NOT NULL (Unix nanoseconds UTC)
//
// All methods are safe for concurrent use; SQLite WAL mode is enabled to allow
// concurrent readers alongside a single writer.
type SqliteAuditTrail struct {
	db *sql.DB
}

// NewSqliteAuditTrail opens (or creates) the SQLite database at dbPath,
// applies the schema, and enables WAL mode for concurrent access.
//
// dbPath: Filesystem path to the SQLite database file. Parent directories are
// created if they do not exist.
//
// Returns an error if:
//   - The parent directory cannot be created (permissions, disk full).
//   - The database file cannot be opened.
//   - Schema migration fails.
//
// The caller must call Close when done to release the file handle.
func NewSqliteAuditTrail(dbPath string) (*SqliteAuditTrail, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot create parent directory for %q: %w — "+
				"check that the path is writable and the filesystem has space",
			dbPath, err,
		)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot open database at %q: %w — "+
				"verify the path is valid and the process has read/write permission",
			dbPath, err,
		)
	}

	// SQLite only supports one writer at a time. Limiting the pool to one
	// connection prevents "database is locked" (SQLITE_BUSY) errors when
	// multiple goroutines call RecordEvent concurrently. All writes are
	// serialised through this single connection; reads share it too since
	// the Temporal worker I/O pattern is write-heavy, not query-heavy.
	db.SetMaxOpenConns(1)

	// Enable WAL mode so concurrent readers don't block the writer,
	// and set a busy timeout so transient SQLITE_BUSY errors retry
	// automatically for up to 5 seconds before returning an error.
	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf(
				"audit.NewSqliteAuditTrail: failed to apply %q on %q: %w",
				p, dbPath, err,
			)
		}
	}

	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: schema migration failed for %q: %w",
			dbPath, err,
		)
	}

	return &SqliteAuditTrail{db: db}, nil
}

// Close releases the underlying database connection. Must be called when the
// trail is no longer needed to avoid resource leaks.
func (s *SqliteAuditTrail) Close() error {
	return s.db.Close()
}

// RecordEvent persists a single AuditEvent to the SQLite database.
//
// Timestamp is stored as Unix nanoseconds (INTEGER) for exact round-trip
// without format parsing overhead.
func (s *SqliteAuditTrail) RecordEvent(_ context.Context, event protocol.AuditEvent) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf(
			"audit.SqliteAuditTrail.RecordEvent: cannot marshal payload for epoch=%q event_type=%q: %w — "+
				"ensure Payload contains only JSON-serializable values",
			event.EpochID, event.EventType, err,
		)
	}

	_, err = s.db.Exec(
		`INSERT INTO audit_events (epoch_id, phase, role, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.EpochID,
		string(event.Phase),
		event.Role,
		string(event.EventType),
		string(payload),
		event.Timestamp.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf(
			"audit.SqliteAuditTrail.RecordEvent: write failed for epoch=%q: %w — "+
				"check that the database file is still accessible and not locked",
			event.EpochID, err,
		)
	}
	return nil
}

// QueryEvents returns audit events matching the given filters in chronological
// order (ascending row id, which equals insertion order).
//
// epochID is required and is always part of the WHERE clause. phase and role
// are optional; nil means "no filter".
func (s *SqliteAuditTrail) QueryEvents(_ context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	var clauses []string
	var args []any

	clauses = append(clauses, "epoch_id = ?")
	args = append(args, epochID)

	if phase != nil {
		clauses = append(clauses, "phase = ?")
		args = append(args, string(*phase))
	}
	if role != nil {
		clauses = append(clauses, "role = ?")
		args = append(args, *role)
	}

	query := `SELECT epoch_id, phase, role, event_type, payload, timestamp
	          FROM audit_events
	          WHERE ` + strings.Join(clauses, " AND ") + `
	          ORDER BY id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QueryEvents: query failed for epoch=%q: %w — "+
				"verify the database file is accessible and the schema is up to date",
			epochID, err,
		)
	}
	defer rows.Close()

	var events []protocol.AuditEvent
	for rows.Next() {
		var (
			epochIDCol   string
			phaseCol     string
			roleCol      string
			eventTypeCol string
			payloadCol   string
			tsNano       int64
		)
		if err := rows.Scan(&epochIDCol, &phaseCol, &roleCol, &eventTypeCol, &payloadCol, &tsNano); err != nil {
			return nil, fmt.Errorf(
				"audit.SqliteAuditTrail.QueryEvents: row scan failed for epoch=%q: %w",
				epochID, err,
			)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadCol), &payload); err != nil {
			return nil, fmt.Errorf(
				"audit.SqliteAuditTrail.QueryEvents: payload unmarshal failed for epoch=%q event_type=%q: %w — "+
					"the stored JSON may be corrupt",
				epochIDCol, eventTypeCol, err,
			)
		}

		events = append(events, protocol.AuditEvent{
			EpochID:   epochIDCol,
			Phase:     protocol.PhaseId(phaseCol),
			Role:      roleCol,
			EventType: protocol.EventType(eventTypeCol),
			Payload:   payload,
			Timestamp: time.Unix(0, tsNano).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QueryEvents: row iteration error for epoch=%q: %w",
			epochID, err,
		)
	}
	return events, nil
}

// ensureSchema creates the audit_events table and indexes if they do not exist.
func ensureSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			epoch_id   TEXT    NOT NULL,
			phase      TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			event_type TEXT    NOT NULL,
			payload    TEXT    NOT NULL,
			timestamp  INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create table audit_events: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_epoch_id ON audit_events (epoch_id)`)
	if err != nil {
		return fmt.Errorf("create index idx_epoch_id: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_phase ON audit_events (phase)`)
	if err != nil {
		return fmt.Errorf("create index idx_phase: %w", err)
	}

	return nil
}

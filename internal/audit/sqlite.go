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

	// Run forward schema migrations to bring the file up to
	// MaxKnownSchemaVersion. On a fresh database this seeds
	// audit_schema_meta(version=2, applied_at=<now>); on an already-
	// migrated database it is a no-op. On a database whose recorded
	// version is higher than MaxKnownSchemaVersion (a future binary
	// wrote it), Migrate returns a *pasterrors.StructuredError with
	// Category=CategoryStorage — propagated unwrapped so callers can
	// errors.As() it (PROPOSAL-2 §11 Scenario 5).
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
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

// RecordSessionEntries persists a batch of SessionEntry records to the SQLite
// database in a single transaction. Nil or empty slices are accepted as no-ops.
//
// All entries are written atomically; if any INSERT fails the entire batch is
// rolled back so callers can retry safely.
func (s *SqliteAuditTrail) RecordSessionEntries(_ context.Context, entries []protocol.SessionEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf(
			"audit.SqliteAuditTrail.RecordSessionEntries: failed to begin transaction: %w — "+
				"check that the database file is still accessible and not locked",
			err,
		)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT INTO session_entries (
			session_id, entry_index, provider, entry_type, role,
			timestamp_ms, content_preview, tokens_in, tokens_out,
			has_tool_use, tool_kind, tool_names_csv,
			has_thinking, is_error, stop_reason, raw_byte_length,
			tool_call_id, entry_id, parent_entry_id, depth,
			parent_index, tool_input, tool_output, extra
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?
		)
	`)
	if err != nil {
		return fmt.Errorf(
			"audit.SqliteAuditTrail.RecordSessionEntries: failed to prepare statement: %w",
			err,
		)
	}
	defer stmt.Close()

	for i, e := range entries {
		_, err = stmt.Exec(
			e.SessionID, e.EntryIndex, e.Provider, e.EntryType, e.Role,
			e.TimestampMs, e.ContentPreview, e.TokensIn, e.TokensOut,
			boolToInt(e.HasToolUse), e.ToolKind, e.ToolNamesCsv,
			boolToInt(e.HasThinking), boolToInt(e.IsError), e.StopReason, e.RawByteLength,
			e.ToolCallID, e.EntryID, e.ParentEntryID, e.Depth,
			e.ParentIndex, e.ToolInput, e.ToolOutput, e.Extra,
		)
		if err != nil {
			return fmt.Errorf(
				"audit.SqliteAuditTrail.RecordSessionEntries: INSERT failed for entry[%d] "+
					"(sessionID=%q, entryIndex=%d): %w — "+
					"check that the database file is still accessible",
				i, e.SessionID, e.EntryIndex, err,
			)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf(
			"audit.SqliteAuditTrail.RecordSessionEntries: commit failed: %w",
			err,
		)
	}
	return nil
}

// boolToInt converts a bool to its SQLite integer representation (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// QuerySessionEntries returns all session entries for the given sessionID in
// ascending entry_index order (matching insertion order for well-formed data).
//
// Returns an empty (non-nil) slice when no entries exist for sessionID.
func (s *SqliteAuditTrail) QuerySessionEntries(_ context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	rows, err := s.db.Query(`
		SELECT
			session_id, entry_index, provider, entry_type, role,
			timestamp_ms, content_preview, tokens_in, tokens_out,
			has_tool_use, tool_kind, tool_names_csv,
			has_thinking, is_error, stop_reason, raw_byte_length,
			tool_call_id, entry_id, parent_entry_id, depth,
			parent_index, tool_input, tool_output, extra
		FROM session_entries
		WHERE session_id = ?
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QuerySessionEntries: query failed for sessionID=%q: %w — "+
				"verify the database file is accessible and the schema is up to date",
			sessionID, err,
		)
	}
	defer rows.Close()

	result := make([]protocol.SessionEntry, 0)
	for rows.Next() {
		var (
			e           protocol.SessionEntry
			hasToolUse  int
			hasThinking int
			isError     int
		)
		if err := rows.Scan(
			&e.SessionID, &e.EntryIndex, &e.Provider, &e.EntryType, &e.Role,
			&e.TimestampMs, &e.ContentPreview, &e.TokensIn, &e.TokensOut,
			&hasToolUse, &e.ToolKind, &e.ToolNamesCsv,
			&hasThinking, &isError, &e.StopReason, &e.RawByteLength,
			&e.ToolCallID, &e.EntryID, &e.ParentEntryID, &e.Depth,
			&e.ParentIndex, &e.ToolInput, &e.ToolOutput, &e.Extra,
		); err != nil {
			return nil, fmt.Errorf(
				"audit.SqliteAuditTrail.QuerySessionEntries: row scan failed for sessionID=%q: %w",
				sessionID, err,
			)
		}
		e.HasToolUse = hasToolUse != 0
		e.HasThinking = hasThinking != 0
		e.IsError = isError != 0
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QuerySessionEntries: row iteration error for sessionID=%q: %w",
			sessionID, err,
		)
	}
	return result, nil
}

// ensureSchema creates the audit_events and session_entries tables (and indexes)
// if they do not exist.
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

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS session_entries (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id       TEXT    NOT NULL,
			entry_index      INTEGER NOT NULL,
			provider         TEXT    NOT NULL,
			entry_type       TEXT    NOT NULL,
			role             TEXT    NOT NULL,
			timestamp_ms     INTEGER,
			content_preview  TEXT,
			tokens_in        INTEGER,
			tokens_out       INTEGER,
			has_tool_use     INTEGER NOT NULL DEFAULT 0,
			tool_kind        TEXT,
			tool_names_csv   TEXT,
			has_thinking     INTEGER NOT NULL DEFAULT 0,
			is_error         INTEGER NOT NULL DEFAULT 0,
			stop_reason      TEXT,
			raw_byte_length  INTEGER,
			tool_call_id     TEXT,
			entry_id         TEXT,
			parent_entry_id  TEXT,
			depth            INTEGER NOT NULL DEFAULT 0,
			parent_index     INTEGER,
			tool_input       TEXT,
			tool_output      TEXT,
			extra            TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("create table session_entries: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_id ON session_entries (session_id)`)
	if err != nil {
		return fmt.Errorf("create index idx_session_id: %w", err)
	}

	return nil
}

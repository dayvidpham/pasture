package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, CGO_ENABLED=0 compatible
)

// SqliteAuditTrail is a Trail implementation backed by a SQLite database.
//
// Uses modernc.org/sqlite (pure Go, CGO_ENABLED=0 compatible — no C toolchain
// required). Events survive process restarts. The database file and any
// intermediate parent directories are created on first open.
//
// Schema (audit_events table, post-v3 — see PROPOSAL-2 §7.10.1):
//
//	id         INTEGER PRIMARY KEY AUTOINCREMENT
//	epoch_id   TEXT             (legacy column, dropped in v4)
//	phase      TEXT             (NULLABLE in v3; legacy v1 had NOT NULL)
//	agent_id   TEXT NOT NULL    (FK-shaped reference to agents.id)
//	event_type TEXT NOT NULL    (protocol.EventType string value)
//	payload    TEXT NOT NULL    (JSON-encoded map[string]any)
//	timestamp  INTEGER NOT NULL (Unix nanoseconds UTC)
//
// # Legacy-role compatibility shim
//
// PROPOSAL-2 §7.11 plans for S8 to replace direct Trail.RecordEvent(role)
// calls with TaskTracker.RecordEvent(agent_id) at the workflow boundary.
// Until S8 lands, callers continue to pass a free-string Role on
// protocol.AuditEvent — the v3 schema drops role but keeps agent_id, so
// SqliteAuditTrail bridges the two by find-or-creating a SoftwareAgent
// named "pasture/legacy-role/<role>" via the same raw-SQL path the v3
// migration uses (migrate_v3_backfill.go), and writes the resulting
// agent_id into the new column. QueryEvents joins back through
// agents_software to repopulate event.Role for the caller, preserving the
// existing API byte-for-byte.
//
// The mapping is cached in roleToAgentID so a write-heavy workload pays
// the find-or-create cost only on the first event per role per process.
//
// All methods are safe for concurrent use; SQLite WAL mode is enabled to
// allow concurrent readers alongside a single writer. The roleToAgentID
// cache is guarded by roleMu.
type SqliteAuditTrail struct {
	db *sql.DB

	// roleMu guards roleToAgentID. The map is populated lazily on first
	// RecordEvent for each distinct role (cache hit on subsequent writes).
	roleMu        sync.Mutex
	roleToAgentID map[string]string
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

	// _txlock=immediate makes db.Begin / db.BeginTx issue "BEGIN IMMEDIATE"
	// instead of plain "BEGIN" (modernc.org/sqlite/sqlite.go:187-193 +
	// tx.go:22-25). This is required for the migration framework's
	// concurrent-migrator safety per PROPOSAL-2 §7.10.3 — without it the
	// migrator's deferred BEGIN would let a concurrent writer interleave
	// between the version probe and the first migration write.
	//
	// _txlock applies to ALL transactions on this *sql.DB, not just the
	// migrator's. That's the right default for the audit subsystem: every
	// audit transaction (RecordSessionEntries today, future TaskTracker
	// methods) is a write transaction that should hold the lock from
	// statement one. The cost is negligible — IMMEDIATE acquires the same
	// lock DEFERRED would have lazily, just earlier.
	db, err := sql.Open("sqlite", dbPath+"?_txlock=immediate")
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

	return &SqliteAuditTrail{
		db:            db,
		roleToAgentID: make(map[string]string),
	}, nil
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
//
// Legacy-role compatibility (PROPOSAL-2 §7.10.2 + this file's type doc):
// the v3 schema dropped audit_events.role and replaced it with agent_id.
// Until S8 wires TaskTracker.RecordEvent(agent_id) at the workflow
// boundary, callers continue to set event.Role on protocol.AuditEvent.
// This method bridges by resolving Role to a SoftwareAgent named
// "pasture/legacy-role/<role>" via raw SQL on agents_software (find or
// create) — the same shape the v3 migration uses — and writes that
// agent_id into the new column. The role→agent_id mapping is cached in
// s.roleToAgentID for write-amortised cost.
//
// An empty event.Role is rejected with CategoryValidation; the v3 schema
// requires a non-NULL agent_id and there is no sensible default.
func (s *SqliteAuditTrail) RecordEvent(_ context.Context, event protocol.AuditEvent) error {
	if event.Role == "" {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.RecordEvent: event.Role is empty for epoch=%q event_type=%q", event.EpochID, event.EventType),
			Why:      "the v3 audit_events schema requires every row to be attributed to an agent (agent_id NOT NULL); an empty Role cannot be resolved to one",
			Impact:   "the event was not recorded; the audit trail for this epoch will be missing this entry until the caller resends with Role populated",
			Fix:      "set event.Role to the originating role string (architect, supervisor, worker, reviewer, etc.); for synthetic/automaton events use the canonical role name from PROPOSAL-2 §7.7.2",
		}
	}

	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.RecordEvent: cannot marshal payload for epoch=%q event_type=%q", event.EpochID, event.EventType),
			Why:      err.Error(),
			Impact:   "the event was not recorded; the audit trail for this epoch will be missing this entry",
			Fix:      "ensure event.Payload contains only JSON-serializable values (no chans, funcs, or cyclic graphs); marshal the payload yourself with json.Marshal to localise the bad field",
		}
	}

	agentID, err := s.resolveLegacyRoleAgentID(event.Role)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO audit_events (epoch_id, phase, agent_id, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.EpochID,
		string(event.Phase),
		agentID,
		string(event.EventType),
		string(payload),
		event.Timestamp.UTC().UnixNano(),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.RecordEvent: INSERT into audit_events failed for epoch=%q event_type=%q", event.EpochID, event.EventType),
			Why:      err.Error(),
			Impact:   "the event was not recorded; the audit trail for this epoch will be missing this entry",
			Fix:      "check that the database file is still accessible and not held by an exclusive writer; verify 'PRAGMA integrity_check' returns ok",
		}
	}
	return nil
}

// resolveLegacyRoleAgentID returns the agent_id for a given role, minting a
// SoftwareAgent on first use. The mapping is cached in s.roleToAgentID so
// subsequent RecordEvent calls for the same role pay no DB lookup cost.
//
// Find-or-create semantics match the v3 migration's
// findOrCreateLegacyRoleAgent (migrate_v3_backfill.go): we reuse any
// existing pasture/legacy-role/<role> agent (e.g. one minted by the
// migration when promoting v1 data) so the cache is consistent with the
// on-disk state across restarts.
//
// The cache + DB lookup sequence is split: we hold s.roleMu only across
// map operations, dropping it for the DB query so concurrent RecordEvent
// calls for OTHER roles don't queue behind a slow lookup. The first writer
// for a given role pays the DB cost; subsequent writers race to populate
// the cache and benefit from idempotent behaviour (find branch returns the
// same id every time).
func (s *SqliteAuditTrail) resolveLegacyRoleAgentID(role string) (string, error) {
	s.roleMu.Lock()
	if cached, ok := s.roleToAgentID[role]; ok {
		s.roleMu.Unlock()
		return cached, nil
	}
	s.roleMu.Unlock()

	name := legacyRoleAgentNamePrefix + role

	// Find branch — common case after the first write for this role across
	// the lifetime of the process or after a v3 migration.
	var existing string
	err := s.db.QueryRow(
		`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
		 WHERE a.kind_id = 2 AND s.name = ? LIMIT 1`,
		name,
	).Scan(&existing)
	switch {
	case err == nil:
		s.roleMu.Lock()
		s.roleToAgentID[role] = existing
		s.roleMu.Unlock()
		return existing, nil
	case err != sql.ErrNoRows:
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.resolveLegacyRoleAgentID: cannot search agents_software for name %q", name),
			Why:      err.Error(),
			Impact:   "the event cannot be attributed; RecordEvent for role %q is failing until the lookup recovers",
			Fix:      "verify the SQLite file is readable and Provenance's agents/agents_software tables exist; run 'pasture migrate' if the schema is below v3",
		}
	}

	// Create branch — first event for this role since the schema reached v3.
	// Mint a fresh UUIDv7 in the same shape as the migration: pasture-namespaced.
	newUUID, err := uuid.NewV7()
	if err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.resolveLegacyRoleAgentID: cannot generate UUIDv7 for role %q", role),
			Why:      err.Error(),
			Impact:   "the event cannot be attributed; RecordEvent for role %q is failing until UUID generation recovers",
			Fix:      "this is unexpected — UUIDv7 generation has no external dependencies; check that the OS clock is not catastrophically broken and rerun",
		}
	}
	agentID := legacyRoleAgentNamespace + "--" + newUUID.String()

	if _, err := s.db.Exec(
		`INSERT INTO agents (id, kind_id) VALUES (?, 2)`,
		agentID,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.resolveLegacyRoleAgentID: cannot insert agents row for role %q (agent_id=%q)", role, agentID),
			Why:      err.Error(),
			Impact:   "the legacy-role SoftwareAgent could not be registered; RecordEvent for this role will keep failing",
			Fix:      "verify the SQLite file is writable and that Provenance's agents table is intact (run 'pasture migrate'); if the error mentions UNIQUE, another writer raced us and a retry will succeed via the find branch",
		}
	}

	if _, err := s.db.Exec(
		`INSERT INTO agents_software (agent_id, name, version, source) VALUES (?, ?, ?, ?)`,
		agentID, name, legacyRoleAgentVersion, legacyRoleAgentSource,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.resolveLegacyRoleAgentID: cannot insert agents_software row for role %q (name=%q)", role, name),
			Why:      err.Error(),
			Impact:   "the legacy-role SoftwareAgent registration is half-complete; subsequent RecordEvent calls will see the agents row but no agents_software match",
			Fix:      "verify the SQLite file is writable and that Provenance's agents_software table is intact; the orphan agents row is harmless but can be cleaned up via 'DELETE FROM agents WHERE id = ? AND NOT EXISTS (SELECT 1 FROM agents_software WHERE agent_id = ?)'",
		}
	}

	s.roleMu.Lock()
	s.roleToAgentID[role] = agentID
	s.roleMu.Unlock()
	return agentID, nil
}

// QueryEvents returns audit events matching the given filters in chronological
// order (ascending row id, which equals insertion order).
//
// epochID is required and is always part of the WHERE clause. phase and role
// are optional; nil means "no filter".
//
// Legacy-role compatibility: the v3 schema dropped audit_events.role and
// replaced it with agent_id. To preserve the existing API where callers
// filter by role and read event.Role on the result, this method LEFT JOINs
// audit_events with agents_software (via agent_id) and:
//
//   - When role != nil, restricts the JOIN target to s.name = "pasture/legacy-role/<role>".
//   - When reading rows, strips the "pasture/legacy-role/" prefix from the
//     joined name to repopulate event.Role. Agents whose name does not match
//     the legacy prefix (e.g. S7 well-known automaton agents) report the
//     full name as-is so the caller still gets a non-empty Role.
//
// LEFT JOIN (rather than INNER JOIN) defends against orphan agent_id values
// that have no agents_software row — those rows are returned with an empty
// Role rather than dropped silently.
func (s *SqliteAuditTrail) QueryEvents(_ context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	var clauses []string
	var args []any

	clauses = append(clauses, "ae.epoch_id = ?")
	args = append(args, epochID)

	if phase != nil {
		clauses = append(clauses, "ae.phase = ?")
		args = append(args, string(*phase))
	}
	if role != nil {
		clauses = append(clauses, "asw.name = ?")
		args = append(args, legacyRoleAgentNamePrefix+*role)
	}

	query := `SELECT ae.epoch_id, ae.phase, COALESCE(asw.name, ''), ae.event_type, ae.payload, ae.timestamp
	          FROM audit_events ae
	          LEFT JOIN agents_software asw ON asw.agent_id = ae.agent_id
	          WHERE ` + strings.Join(clauses, " AND ") + `
	          ORDER BY ae.id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.QueryEvents: query failed for epoch=%q", epochID),
			Why:      err.Error(),
			Impact:   "no events can be returned for this epoch",
			Fix:      "verify the database file is accessible and the schema is at v3 or higher; run 'pasture migrate' if the schema is older",
		}
	}
	defer rows.Close()

	var events []protocol.AuditEvent
	for rows.Next() {
		var (
			epochIDCol   string
			phaseCol     string
			agentName    string
			eventTypeCol string
			payloadCol   string
			tsNano       int64
		)
		if err := rows.Scan(&epochIDCol, &phaseCol, &agentName, &eventTypeCol, &payloadCol, &tsNano); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("audit.SqliteAuditTrail.QueryEvents: row scan failed for epoch=%q", epochID),
				Why:      err.Error(),
				Impact:   "partial result; the event listing cannot be returned reliably",
				Fix:      "re-run the query; if the error persists, inspect the audit_events row layout via 'sqlite3 <db> .schema audit_events'",
			}
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadCol), &payload); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("audit.SqliteAuditTrail.QueryEvents: payload unmarshal failed for epoch=%q event_type=%q", epochIDCol, eventTypeCol),
				Why:      err.Error(),
				Impact:   "the row cannot be returned because its JSON payload is corrupt",
				Fix:      "inspect the row directly via 'sqlite3 <db> \"SELECT payload FROM audit_events WHERE id = ...\"' and either repair the JSON or drop the row",
			}
		}

		events = append(events, protocol.AuditEvent{
			EpochID:   epochIDCol,
			Phase:     protocol.PhaseId(phaseCol),
			Role:      stripLegacyRolePrefix(agentName),
			EventType: protocol.EventType(eventTypeCol),
			Payload:   payload,
			Timestamp: time.Unix(0, tsNano).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.SqliteAuditTrail.QueryEvents: row iteration error for epoch=%q", epochID),
			Why:      err.Error(),
			Impact:   "partial result; the event listing cannot be returned reliably",
			Fix:      "re-run the query; if the error persists, the SQLite file may be corrupt — check 'PRAGMA integrity_check'",
		}
	}
	return events, nil
}

// stripLegacyRolePrefix returns the role substring from a synthetic
// legacy-role agent name (pasture/legacy-role/<role> → <role>). Agent
// names that don't carry the prefix (e.g. S7 well-known automaton agents
// like "pasture/automaton/check-constraints") are returned as-is so the
// caller gets a non-empty Role for those events too — the original Trail
// API contract.
func stripLegacyRolePrefix(name string) string {
	if strings.HasPrefix(name, legacyRoleAgentNamePrefix) {
		return name[len(legacyRoleAgentNamePrefix):]
	}
	return name
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

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

	"github.com/google/uuid"

	"github.com/dayvidpham/pasture/internal/dbconn"
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
// The mapping is cached in roleToAgentId so a write-heavy workload pays
// the find-or-create cost only on the first event per role per process.
//
// All methods are safe for concurrent use; SQLite WAL mode is enabled to
// allow concurrent readers alongside a single writer. The roleToAgentId
// cache is guarded by roleMu.
type SqliteAuditTrail struct {
	db *sql.DB

	// roleMu guards roleToAgentId. The map is populated lazily on first
	// RecordEvent for each distinct role (cache hit on subsequent writes).
	roleMu        sync.Mutex
	roleToAgentId map[string]string
}

type sqliteAuditTrailOptions struct {
	skipMigrations bool
}

// SqliteAuditTrailOption configures NewSqliteAuditTrailWithOptions.
type SqliteAuditTrailOption func(*sqliteAuditTrailOptions)

// WithSkipMigrations skips audit DDL/migration and asserts that the file is
// already at MaxKnownSchemaVersion. It is intended for tests that copy a
// pre-migrated golden database; migration tests should keep using
// NewSqliteAuditTrail so they exercise the real migrator.
func WithSkipMigrations() SqliteAuditTrailOption {
	return func(o *sqliteAuditTrailOptions) {
		o.skipMigrations = true
	}
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
	return NewSqliteAuditTrailWithOptions(dbPath)
}

// NewSqliteAuditTrailWithOptions opens the SQLite audit trail with explicit
// opt-in behavior for tests that need to bypass migrations on a known-current
// golden database.
func NewSqliteAuditTrailWithOptions(dbPath string, opts ...SqliteAuditTrailOption) (*SqliteAuditTrail, error) {
	cfg := sqliteAuditTrailOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}

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
	// The shared DSN encodes the full concurrency contract as connection-string
	// params (WAL, busy_timeout=5000, synchronous=NORMAL, foreign_keys=ON,
	// _txlock=immediate), applied to every connection in the pool.
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot open database at %q: %w — "+
				"verify the path is valid and the process has read/write permission",
			dbPath, err,
		)
	}

	// This is a pasture-owned WRITE handle: every audit transaction (RecordEvent,
	// RecordSessionEntries, the migrator's BEGIN IMMEDIATE) is a writer. Capping
	// the pool to one connection serializes those writers at the Go level, which
	// is the proven model the cross-subsystem race test exercises. The shared
	// DSN supplies the same WAL/busy_timeout pragmas the DBOS engine handle uses,
	// but the engine handle is intentionally UNcapped because the DBOS
	// notification poller needs a second concurrent connection to make progress;
	// pasture's own audit writes do not, and removing the cap reintroduces the
	// SQLITE_BUSY contention busy_timeout cannot always absorb under the file's
	// multi-handle write load.
	db.SetMaxOpenConns(1)

	if cfg.skipMigrations {
		if err := assertCurrentSchema(db); err != nil {
			db.Close()
			return nil, err
		}
		return &SqliteAuditTrail{
			db:            db,
			roleToAgentId: make(map[string]string),
		}, nil
	}

	// Disable foreign-key enforcement for the migration window. The DSN turns
	// foreign_keys ON, but the v3→v4 migration rebuilds audit_events by DROP +
	// RENAME, and with enforcement ON that DROP would cascade-delete the
	// context_edges rows that reference audit_events(id) ON DELETE CASCADE —
	// silently discarding every epoch↔event link. Toggling enforcement off for
	// the rebuild and back on afterward is the documented SQLite procedure for
	// ALTER-by-rebuild. Safe here because this handle is single-connection, so
	// the PRAGMA and the migration run on the same connection.
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		db.Close()
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot relax foreign-key enforcement for migration on %q: %w",
			dbPath, err,
		)
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

	// Restore foreign-key enforcement now that the rebuild migrations are done.
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot restore foreign-key enforcement after migration on %q: %w",
			dbPath, err,
		)
	}

	return &SqliteAuditTrail{
		db:            db,
		roleToAgentId: make(map[string]string),
	}, nil
}

func assertCurrentSchema(db *sql.DB) error {
	version, err := ReadSchemaVersion(db)
	if err != nil {
		return err
	}
	if version != MaxKnownSchemaVersion {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The copied pasture database is at schema version %d, not %d.", version, MaxKnownSchemaVersion),
			Why:      "A caller opened the database with migrations explicitly disabled, so the file must already be current.",
			Where:    "Opening a pre-migrated audit database (internal/audit/sqlite.go in audit.assertCurrentSchema).",
			Impact:   "The database was not opened; continuing could make tests pass against the wrong schema.",
			Fix:      "Rebuild the golden database through the normal migrator, or open this file without WithSkipMigrations.",
		}
	}
	return nil
}

// Close releases the underlying database connection. Must be called when the
// trail is no longer needed to avoid resource leaks.
func (s *SqliteAuditTrail) Close() error {
	return s.db.Close()
}

// RecordEvent persists a single AuditEvent to the SQLite database, discarding
// the inserted row id. It is a thin wrapper over RecordEventReturningId for
// callers that record-and-forget; callers that need the inserted id (e.g. to
// follow up with AttachContext) MUST use RecordEventReturningId directly.
//
// See RecordEventReturningId for the full INSERT semantics, transaction
// boundary, validation rules, and error categories.
func (s *SqliteAuditTrail) RecordEvent(ctx context.Context, event protocol.AuditEvent) error {
	_, err := s.RecordEventReturningId(ctx, event)
	return err
}

// RecordEventReturningId persists a single AuditEvent and returns the
// audit_events.id of the newly-inserted row.
//
// Race safety: the row id is recovered via sql.Result.LastInsertId on the SAME
// INSERT statement that wrote the row, INSIDE the same transaction. This is
// race-free under any level of write contention — the driver tracks the rowid
// per-statement, not per-connection — and replaces the older "RecordEvent
// then SELECT MAX(id)" workaround that could return a row id belonging to a
// concurrent writer (PROPOSAL-2 §7.11 future-work, realised in Phase 11 R1-B).
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
// s.roleToAgentId for write-amortised cost.
//
// An empty event.Role is rejected with CategoryValidation; the v3 schema
// requires a non-NULL agent_id and there is no sensible default.
//
// Returns (id, nil) on success or (0, *pasterrors.StructuredError) on failure.
func (s *SqliteAuditTrail) RecordEventReturningId(_ context.Context, event protocol.AuditEvent) (int64, error) {
	if event.Role == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What: fmt.Sprintf(
				"This audit event has no role attached, so we can't tell who did it (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why: "Every audit event must be attributed to an agent (architect, supervisor, worker,\n" +
				"reviewer, or one of pasture's built-in automaton agents). The event you sent has an\n" +
				"empty role, so we can't link it to anyone.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event was not recorded. The audit trail for this epoch will be missing this entry\n" +
				"until the caller resends it with the role filled in.",
			Fix: "1. Set the event's role to the originating role name before sending it again, e.g.\n" +
				"   \"architect\", \"supervisor\", \"worker\", or \"reviewer\".\n" +
				"2. For events emitted by pasture's built-in automatons, use the well-known automaton\n" +
				"   name (something starting with \"pasture/automaton/\").",
		}
	}

	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't convert this audit event's payload to JSON (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why:   "The payload contains a value that can't be encoded as JSON.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event was not recorded. The audit trail for this epoch will be missing this entry\n" +
				"until the caller resends it with a serialisable payload.",
			Fix: "1. Make sure the event's payload only contains values that can be encoded as JSON\n" +
				"   (strings, numbers, booleans, lists, and maps). Channels, functions, and cyclic\n" +
				"   data structures are not allowed.\n" +
				"2. To pinpoint the bad field, serialise the payload yourself in the caller — the\n" +
				"   encoder will name the offending field.",
			Cause: err,
		}
	}

	agentId, err := s.resolveLegacyRoleAgentId(event.Role)
	if err != nil {
		return 0, err
	}

	// Post-v4 schema: audit_events.epoch_id is gone (dropped by S4's
	// migrate_v3_v4.go). Epoch attachment is now expressed as a
	// context_edges (event_id, 'EpochContext', epoch_id) row written
	// inside the same transaction so a crash between INSERT and
	// AttachContext cannot leave a row without its epoch correlation.
	//
	// Caller-side compatibility: protocol.AuditEvent still carries an
	// EpochId field for the existing public API. SqliteAuditTrail
	// bridges the two by writing the audit_events row first, then the
	// context_edges row if EpochId is non-empty. QueryEvents recovers
	// EpochId by joining context_edges with kind='EpochContext'.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't start the database write that would record this audit event (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why:   "The database refused to start the write transaction.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event was not recorded. The audit trail for this epoch will be missing this entry\n" +
				"until the underlying database problem is fixed and the caller retries.",
			Fix: "1. Confirm the audit database file is accessible and not locked by another writer:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     lsof <path-to-audit.db>\n" +
				"2. Check the file isn't corrupted:\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"3. Retry the operation once the file is healthy and unlocked.",
			Cause: err,
		}
	}
	defer func() { _ = tx.Rollback() }()

	// dedup_key is NULL for ordinary callers (the partial unique index ignores
	// NULLs, so every legacy write inserts a fresh row as before). The durable
	// engine supplies a deterministic key; the ON CONFLICT clause targets the
	// partial index (matched by repeating its WHERE predicate) so a replayed
	// emission is a silent no-op instead of a duplicate row.
	var dedupKey any
	if event.DedupKey != "" {
		dedupKey = event.DedupKey
	}
	res, err := tx.Exec(
		`INSERT INTO audit_events (phase, agent_id, event_type, payload, timestamp, dedup_key)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
		string(event.Phase),
		agentId,
		string(event.EventType),
		string(payload),
		event.Timestamp.UTC().UnixNano(),
		dedupKey,
	)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't write this audit event into the database (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why:   "The database refused the write into the audit-events table.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event was not recorded. The audit trail for this epoch will be missing this entry\n" +
				"until the underlying database problem is fixed and the caller retries.",
			Fix: "1. Confirm the audit database file is accessible and not locked by another writer:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     lsof <path-to-audit.db>\n" +
				"2. Check the file isn't corrupted:\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"3. Retry the operation once the file is healthy and unlocked.",
			Cause: err,
		}
	}

	// Exactly-once dedup path: when the engine supplied a dedup_key and the
	// ON CONFLICT clause skipped the insert (a replay), no new row was written.
	// LastInsertId would then report a stale rowid, so recover the canonical
	// id from the already-present row by its dedup_key and commit without
	// re-attaching context (the original write already linked the epoch).
	if dedupKey != nil {
		if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
			var existingId int64
			if serr := tx.QueryRow(
				`SELECT id FROM audit_events WHERE dedup_key = ?`, event.DedupKey,
			).Scan(&existingId); serr != nil {
				return 0, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What: fmt.Sprintf(
						"An audit event was deduplicated but its existing row couldn't be found (epoch %q, event type %q).",
						event.EpochId, event.EventType,
					),
					Why: "The durable engine re-emitted an event whose deduplication key already exists, so the\n" +
						"insert was skipped, but the lookup for the original row then failed.",
					Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
					Impact: "The replayed emission can't return the original row id, so the caller can't correlate it.",
					Fix: "1. Confirm the partial unique index exists (added by the version 4 → 5 upgrade):\n" +
						"     sqlite3 <path-to-pasture.db> '.indexes audit_events'\n" +
						"2. Re-run the migration if it is missing:\n" +
						"     pasture migrate",
					Cause: serr,
				}
			}
			if err := tx.Commit(); err != nil {
				return 0, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What: fmt.Sprintf(
						"Couldn't finalize a deduplicated audit event (epoch %q, event type %q).",
						event.EpochId, event.EventType,
					),
					Why:    "The database refused to commit the read-only transaction that resolved the existing row.",
					Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
					Impact: "The original row is intact; only this replayed call could not complete cleanly.",
					Fix:    "Retry the operation once the database is healthy.",
					Cause:  err,
				}
			}
			return existingId, nil
		}
	}

	// Recover the just-inserted row id from THIS INSERT statement's result.
	// LastInsertId is per-statement on modernc.org/sqlite, so the returned
	// value is always the rowid of the row this Exec wrote — independent of
	// any concurrent INSERTs on the same connection. This is the property
	// that makes RecordEventReturningId race-safe and allows it to replace
	// the older SELECT MAX(id) workaround in trackerImpl + free_floating.
	eventId, err := res.LastInsertId()
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The audit event was written but we couldn't read back its row ID (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why:   "The database refused to report the last inserted row ID.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event itself was recorded, but anything that needs to attach extra context to it\n" +
				"(linking it to a session, a workflow, or another event) can't proceed because that\n" +
				"link requires the row ID.",
			Fix: "1. SQLite normally always reports the last-inserted row ID, so this is unexpected.\n" +
				"   Confirm the SQLite driver version is current:\n" +
				"     pasture --version\n" +
				"2. Retry the operation. If the error persists, please file a bug.",
			Cause: err,
		}
	}
	if eventId <= 0 {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The audit event was written but came back with an invalid row ID (%d) for epoch %q, event type %q.",
				eventId, event.EpochId, event.EventType,
			),
			Why: "Row IDs in the audit-events table count up from 1. A value of zero or below means\n" +
				"the table's auto-numbering has been reset or the table is corrupted.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "Anything that needs to link extra context to this event (sessions, workflow attachments,\n" +
				"related events) can't proceed because the link requires a valid row ID.",
			Fix: "1. Look at the most recent rows in the audit-events table to confirm the corruption:\n" +
				"     sqlite3 <path-to-audit.db> 'SELECT id FROM audit_events ORDER BY id DESC LIMIT 5'\n" +
				"2. If the IDs really are non-positive, restore from a backup and please file a bug.",
		}
	}

	if event.EpochId != "" {
		// INSERT OR IGNORE: a duplicate triple (event_id, 'EpochContext',
		// epochId) is a no-op. Production never produces duplicates here
		// because event_id is freshly minted; the OR IGNORE defends
		// against future callers that may write multi-context events
		// idempotently and still expect this method to succeed.
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
			eventId, "EpochContext", event.EpochId,
		); err != nil {
			return 0, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't link the audit event to its epoch (epoch %q, event type %q, event row %d).",
					event.EpochId, event.EventType, eventId,
				),
				Why:   "The database refused the insert that links the event to its epoch.",
				Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
				Impact: "The event itself was written, but it can't be found by queries that filter by epoch.\n" +
					"The audit trail for this epoch will be missing this entry.",
				Fix: "1. Confirm the event-to-context table exists (it's created by the version 2 → 3\n" +
					"   audit-database upgrade):\n" +
					"     sqlite3 <path-to-audit.db> '.schema context_edges'\n" +
					"2. If it's missing, the database is older than version 3 — upgrade it:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't save this audit event (epoch %q, event type %q).",
				event.EpochId, event.EventType,
			),
			Why:   "The database refused to commit the write transaction.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
			Impact: "The event was not recorded (everything was rolled back). The audit trail for this\n" +
				"epoch will be missing this entry until the caller retries.",
			Fix: "1. Confirm the audit database file is writable and not corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Retry the operation once the file is healthy.",
			Cause: err,
		}
	}
	return eventId, nil
}

// resolveLegacyRoleAgentId returns the agent_id for a given role, minting a
// SoftwareAgent on first use. The mapping is cached in s.roleToAgentId so
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
func (s *SqliteAuditTrail) resolveLegacyRoleAgentId(role string) (string, error) {
	s.roleMu.Lock()
	if cached, ok := s.roleToAgentId[role]; ok {
		s.roleMu.Unlock()
		return cached, nil
	}
	s.roleMu.Unlock()

	// Well-known direct lookup (PROPOSAL-2 §7.7.2 + S8 wiring): if the role
	// string already names a registered well-known automaton (prefix
	// "pasture/automaton/..." per the canonical registry in
	// internal/tasks/well_known_registry.go), skip the legacy-role prefix
	// dance and bind directly to the existing agents_software row. S7
	// registered the well-known agent at daemon startup; S8's
	// Activities.RecordTransition / RecordAuditEvent set
	// event.Role = <well-known-name> so this branch fires for every workflow
	// event from S8 onward, without producing the SHADOW
	// "pasture/legacy-role/pasture/automaton/.." rows that would otherwise
	// pollute agents_software and break the §11 Scenario 8a–8e attribution
	// JOINs (which assert agents_software.name == "pasture/automaton/..").
	//
	// If the direct lookup misses (the role looks well-known but no row
	// exists, e.g. tests with an unpopulated cache) we fall through to the
	// legacy-role find-or-create path so the call still succeeds.
	if strings.HasPrefix(role, "pasture/automaton/") {
		var directExisting string
		derr := s.db.QueryRow(
			`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
			 WHERE a.kind_id = 2 AND s.name = ? LIMIT 1`,
			role,
		).Scan(&directExisting)
		switch {
		case derr == nil:
			s.roleMu.Lock()
			s.roleToAgentId[role] = directExisting
			s.roleMu.Unlock()
			return directExisting, nil
		case derr != sql.ErrNoRows:
			// Real DB error — surface it; do not silently fall through to
			// the legacy-role path because that would mask storage problems.
			return "", &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't look up the built-in agent %q in the agent registry.",
					role,
				),
				Why:   "The database refused the query into the agent registry table.",
				Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.resolveLegacyRoleAgentId).",
				Impact: fmt.Sprintf(
					"The audit event can't be attributed to its agent. Recording events for the built-in\n"+
						"role %q will keep failing until the lookup recovers.",
					role,
				),
				Fix: "1. Confirm the audit database file is readable:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. Confirm the agent registry tables still exist:\n" +
					"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
					"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
					"3. If they're missing, the database is older than version 3 — upgrade it:\n" +
					"     pasture migrate",
				Cause: derr,
			}
		}
		// derr == sql.ErrNoRows: well-known name not in agents_software
		// (e.g. tests that build Activities with WellKnownAgents but never
		// run S7's RegisterWellKnownAgents against the same DB). Fall
		// through to the legacy-role find-or-create — the resulting SHADOW
		// agent is tagged by the prefix and is harmless in test contexts.
	}

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
		s.roleToAgentId[role] = existing
		s.roleMu.Unlock()
		return existing, nil
	case err != sql.ErrNoRows:
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't look up the legacy-role agent named %q in the agent registry.",
				name,
			),
			Why:   "The database refused the query into the agent registry table.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.resolveLegacyRoleAgentId).",
			Impact: fmt.Sprintf(
				"The audit event can't be attributed to its agent. Recording events for role %q will\n"+
					"keep failing until the lookup recovers.",
				role,
			),
			Fix: "1. Confirm the audit database file is readable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent registry tables still exist:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
				"3. If they're missing, the database is older than version 3 — upgrade it:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}

	// Create branch — first event for this role since the schema reached v3.
	// Mint a fresh UUIDv7 in the same shape as the migration: pasture-namespaced.
	newUUID, err := uuid.NewV7()
	if err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't generate a unique ID for the new agent for role %q.",
				role,
			),
			Why:   "The unique-ID generator returned an unexpected error.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.resolveLegacyRoleAgentId).",
			Impact: fmt.Sprintf(
				"The audit event can't be attributed to a new agent for role %q. Recording events for\n"+
					"this role will keep failing until ID generation recovers.",
				role,
			),
			Fix: "1. ID generation is built-in and almost never fails — this usually means the system\n" +
				"   clock is unreadable or set to a wildly invalid value. Check the clock:\n" +
				"     date -u\n" +
				"2. Fix any clock or NTP problems, then retry.",
			Cause: err,
		}
	}
	agentId := legacyRoleAgentNamespace + "--" + newUUID.String()

	if _, err := s.db.Exec(
		`INSERT INTO agents (id, kind_id) VALUES (?, 2)`,
		agentId,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't register a new agent for role %q.",
				role,
			),
			Why:   "The database refused the insert into the agent registry.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.resolveLegacyRoleAgentId).",
			Impact: "The new agent for this role couldn't be created, so audit events from this role can't\n" +
				"be attributed. Recording events for this role will keep failing.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent registry table still exists and is intact:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
				"     pasture migrate\n" +
				"3. If the underlying error mentions a uniqueness conflict, another writer beat us to\n" +
				"   creating this agent. Retry — the second attempt will find the existing agent and\n" +
				"   succeed.",
			Cause: err,
		}
	}

	if _, err := s.db.Exec(
		`INSERT INTO agents_software (agent_id, name, version, source) VALUES (?, ?, ?, ?)`,
		agentId, name, legacyRoleAgentVersion, legacyRoleAgentSource,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't record the new agent's name (%q) for role %q.",
				name, role,
			),
			Why:   "The database refused the insert into the agent details table.",
			Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.resolveLegacyRoleAgentId).",
			Impact: "The agent registration is half-complete: the agent exists in the registry but has no\n" +
				"name, so subsequent lookups by role won't find it. Recording events for this role\n" +
				"will keep failing.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent details table still exists and is intact:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
				"3. The orphan agent row is harmless, but if you want to clean it up:\n" +
				"     sqlite3 <path-to-audit.db> 'DELETE FROM agents WHERE id NOT IN (SELECT agent_id FROM agents_software)'",
			Cause: err,
		}
	}

	s.roleMu.Lock()
	s.roleToAgentId[role] = agentId
	s.roleMu.Unlock()
	return agentId, nil
}

// QueryEvents returns audit events matching the given filters in chronological
// order (ascending row id, which equals insertion order).
//
// epochId is required and is always part of the WHERE clause. phase and role
// are optional; nil means "no filter".
//
// Delegates to QueryEventsOn, the single canonical query implementation.
// Both SqliteAuditTrail.QueryEvents and StatusReader.QueryEvents use the same
// shared function so any schema change (v5+) is applied in one place. See
// QueryEventsOn for the legacy-role compatibility and LEFT JOIN mechanics.
func (s *SqliteAuditTrail) QueryEvents(ctx context.Context, epochId string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	return QueryEventsOn(ctx, s.db, epochId, phase, role)
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
			e.SessionId, e.EntryIndex, e.Provider, e.EntryType, e.Role,
			e.TimestampMs, e.ContentPreview, e.TokensIn, e.TokensOut,
			boolToInt(e.HasToolUse), e.ToolKind, e.ToolNamesCsv,
			boolToInt(e.HasThinking), boolToInt(e.IsError), e.StopReason, e.RawByteLength,
			e.ToolCallId, e.EntryId, e.ParentEntryId, e.Depth,
			e.ParentIndex, e.ToolInput, e.ToolOutput, e.Extra,
		)
		if err != nil {
			return fmt.Errorf(
				"audit.SqliteAuditTrail.RecordSessionEntries: INSERT failed for entry[%d] "+
					"(sessionId=%q, entryIndex=%d): %w — "+
					"check that the database file is still accessible",
				i, e.SessionId, e.EntryIndex, err,
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

// QuerySessionEntries returns all session entries for the given sessionId in
// ascending entry_index order (matching insertion order for well-formed data).
//
// Returns an empty (non-nil) slice when no entries exist for sessionId.
func (s *SqliteAuditTrail) QuerySessionEntries(_ context.Context, sessionId string) ([]protocol.SessionEntry, error) {
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
		ORDER BY entry_index ASC, id ASC
	`, sessionId)
	if err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QuerySessionEntries: query failed for sessionId=%q: %w — "+
				"verify the database file is accessible and the schema is up to date",
			sessionId, err,
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
			&e.SessionId, &e.EntryIndex, &e.Provider, &e.EntryType, &e.Role,
			&e.TimestampMs, &e.ContentPreview, &e.TokensIn, &e.TokensOut,
			&hasToolUse, &e.ToolKind, &e.ToolNamesCsv,
			&hasThinking, &isError, &e.StopReason, &e.RawByteLength,
			&e.ToolCallId, &e.EntryId, &e.ParentEntryId, &e.Depth,
			&e.ParentIndex, &e.ToolInput, &e.ToolOutput, &e.Extra,
		); err != nil {
			return nil, fmt.Errorf(
				"audit.SqliteAuditTrail.QuerySessionEntries: row scan failed for sessionId=%q: %w",
				sessionId, err,
			)
		}
		e.HasToolUse = hasToolUse != 0
		e.HasThinking = hasThinking != 0
		e.IsError = isError != 0
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"audit.SqliteAuditTrail.QuerySessionEntries: row iteration error for sessionId=%q: %w",
			sessionId, err,
		)
	}
	return result, nil
}

// ensureSchema creates the audit_events and session_entries tables (and indexes)
// if they do not exist.
//
// # Why we still create the legacy v1 audit_events shape
//
// On a brand-new SQLite file, ensureSchema runs FIRST (before Migrate)
// and seeds the table with the v1 layout (epoch_id NOT NULL, role NOT
// NULL). Migrate then walks v1→v2→v3→v4 forward steps, dropping role
// (in v3 via S3's table-rebuild) and dropping epoch_id (in v4 via
// migrate_v3_v4.go's table-rebuild). The end-state on a fresh DB is
// identical to the post-v4 reopen state.
//
// On a REOPEN of an already-migrated v4 file, the CREATE TABLE IF NOT
// EXISTS is a no-op (the table exists with the post-v4 shape). Migrate
// then observes MAX(version)=4 and exits without work.
//
// # Why the legacy idx_epoch_id and idx_phase indexes are NOT created here
//
// PROPOSAL-2 §7.10.1 v3 + v4 migrations drop these indexes implicitly
// (the SQLite table-rebuild pattern drops the table along with its
// attached indexes). The post-v3 schema replaces idx_epoch_id with
// idx_audit_events_agent + idx_audit_events_timestamp; the post-v4
// schema additionally relies on context_edges + idx_context_edges_lookup
// for epoch-by-id queries.
//
// Recreating idx_epoch_id and idx_phase here would crash on the REOPEN
// path of a post-v4 file because the underlying columns are gone. The
// fresh-DB path doesn't need these legacy indexes either: any rows
// written between ensureSchema and Migrate's first step would be
// preserved through the table rebuild, and there is no production
// caller that writes during this window (NewSqliteAuditTrail runs
// ensureSchema then Migrate then returns the trail handle).
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

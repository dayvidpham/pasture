package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
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
	busyTier      time.Duration
	writerPolicy  auditWriterPolicy
	logger        *slog.Logger
}

type sqliteAuditTrailOptions struct {
	skipMigrations bool
	timeouts       timeouts.Profile
	logger         *slog.Logger
	// ceiling, when non-zero, stops the migrator short of
	// MaxKnownSchemaVersion so the offline cutover's catch-up runs to the
	// deferred target and the conversion transaction applies the remaining
	// step(s) atomically with the marker and the receipt.
	ceiling int
	// busyRetry bounds the retry the open runs around the statements outside
	// the migrator's transaction (open_retry.go). The zero value means the
	// migrator's own budget; only in-package tests set another.
	busyRetry busyRetryPolicy
}

func WithTimeoutProfile(profile timeouts.Profile) SqliteAuditTrailOption {
	return func(o *sqliteAuditTrailOptions) { o.timeouts = profile }
}

// WithLogger routes committed-cleanup warnings to the caller's diagnostics.
// Without this option, the process's default structured logger owns them.
// Disabled or failed warning delivery retains the committed-cleanup error.
func WithLogger(logger *slog.Logger) SqliteAuditTrailOption {
	return func(o *sqliteAuditTrailOptions) { o.logger = logger }
}

// WithDeferredCeiling stops the audit migrator short of the identity-format
// ceiling. The offline cutover's catch-up phase uses it so the ceiling step is
// applied by the conversion transaction, atomically with the marker and the
// receipt, never by a plain open.
func WithDeferredCeiling(ceiling int) SqliteAuditTrailOption {
	return func(o *sqliteAuditTrailOptions) { o.ceiling = ceiling }
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
	return NewSqliteAuditTrailContext(context.Background(), dbPath)
}

// NewSqliteAuditTrailContext uses ctx for connection setup and migrations only.
func NewSqliteAuditTrailContext(ctx context.Context, dbPath string) (*SqliteAuditTrail, error) {
	return NewSqliteAuditTrailContextWithOptions(ctx, dbPath)
}

// NewSqliteAuditTrailWithOptions opens the SQLite audit trail with explicit
// opt-in behavior for tests that need to bypass migrations on a known-current
// golden database.
func NewSqliteAuditTrailWithOptions(dbPath string, opts ...SqliteAuditTrailOption) (*SqliteAuditTrail, error) {
	return NewSqliteAuditTrailContextWithOptions(context.Background(), dbPath, opts...)
}

// NewSqliteAuditTrailContextWithOptions refuses nil ctx and does not retain it.
func NewSqliteAuditTrailContextWithOptions(ctx context.Context, dbPath string, opts ...SqliteAuditTrailOption) (*SqliteAuditTrail, error) {
	if ctx == nil {
		return nil, contextValidationError("NewSqliteAuditTrailContextWithOptions")
	}
	if err := ctx.Err(); err != nil {
		return nil, openCancelledError(dbPath, "start audit setup", 0, err, nil)
	}
	cfg := sqliteAuditTrailOptions{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.logger == nil {
		return nil, fmt.Errorf("audit.NewSqliteAuditTrail: cleanup diagnostics require a non-nil logger; omit WithLogger for the process default or supply the owner's logger")
	}
	if cfg.timeouts.IsZero() {
		cfg.timeouts = timeouts.ProductionProfile()
	}
	if err := cfg.timeouts.Validate(); err != nil {
		return nil, fmt.Errorf("audit.NewSqliteAuditTrail: invalid timeout profile: %w", err)
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
	// The connector applies the same static concurrency contract to every
	// connection, with setup SQL governed by that connection's acquisition ctx.
	db, err := dbconn.NewSharedDBWithProfile(dbPath, cfg.timeouts)
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
		if err := assertCurrentSchemaContext(ctx, db); err != nil {
			db.Close()
			return nil, err
		}
		return &SqliteAuditTrail{
			db:            db,
			roleToAgentId: make(map[string]string),
			busyTier:      cfg.timeouts.SQLiteBusy(),
			logger:        cfg.logger,
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
	//
	// This is the FIRST statement on the handle, so it also opens the first
	// connection and runs the connection string's pragmas, one of which
	// switches a rollback-journal file to write-ahead logging. Two processes
	// opening the same such file at the same moment both try that switch, and
	// the one that loses gets a SQLITE_BUSY that no busy handler retries (see
	// open_retry.go). The retry opens a fresh connection each time, which finds
	// the file already switched, or the lock cleared, and proceeds.
	busyRetry := cfg.busyRetry
	if busyRetry == (busyRetryPolicy{}) {
		busyRetry = defaultBusyRetryPolicy()
	}
	openCtx := ctx
	if err := execWithBusyRetry(openCtx, busyRetry, db, dbPath,
		"relax foreign-key enforcement for the migration window", `PRAGMA foreign_keys=OFF`); err != nil {
		db.Close()
		if isStructuredError(err) {
			return nil, err
		}
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot relax foreign-key enforcement for migration on %q: %w",
			dbPath, err,
		)
	}

	// The base tables are created with IF NOT EXISTS, so a create that met a
	// lock the other process held for longer than one busy window is safe to
	// run again.
	if err := retryOnBusy(openCtx, busyRetry, dbPath, "create the audit tables", func() error {
		return ensureSchemaContext(ctx, db)
	}); err != nil {
		db.Close()
		if isStructuredError(err) {
			return nil, err
		}
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
	if err := MigrateContextToCeiling(ctx, db, cfg.ceiling); err != nil {
		db.Close()
		return nil, err
	}

	// Restore foreign-key enforcement now that the rebuild migrations are done.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf(
			"audit.NewSqliteAuditTrail: cannot restore foreign-key enforcement after migration on %q: %w",
			dbPath, err,
		)
	}

	return &SqliteAuditTrail{
		db:            db,
		roleToAgentId: make(map[string]string),
		busyTier:      cfg.timeouts.SQLiteBusy(),
		logger:        cfg.logger,
	}, nil
}

func assertCurrentSchema(db *sql.DB) error {
	return assertCurrentSchemaContext(context.Background(), db)
}

func assertCurrentSchemaContext(ctx context.Context, db *sql.DB) error {
	version, err := readVersionContext(ctx, db)
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
// concurrent writer.
//
// Timestamp is stored as Unix nanoseconds (INTEGER) for exact round-trip
// without format parsing overhead.
//
// Legacy-role compatibility: the v3 schema dropped audit_events.role and
// replaced it with agent_id. Until callers are wired to pass agent_id
// directly, this method bridges by resolving Role to a SoftwareAgent named
// "pasture/legacy-role/<role>" via raw SQL (find or create) and writes that
// agent_id into the new column. The role→agent_id mapping is cached AFTER a
// successful commit so that a rolled-back write never populates the cache.
//
// All DB work (agent lookup/insert + event insert + context_edges insert) runs
// inside a single atomic BEGIN IMMEDIATE transaction via withAuditWrite, which
// retries on SQLite contention and honours the caller's context deadline.
//
// An empty event.Role is rejected with CategoryValidation; the v3 schema
// requires a non-NULL agent_id and there is no sensible default.
//
// Returns (id, nil) on success or (0, error) on failure.
func (s *SqliteAuditTrail) RecordEventReturningId(ctx context.Context, event protocol.AuditEvent) (int64, error) {
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

	// Closure-scoped state shared between the callback and post-commit work.
	// The callback saves agent/event IDs here; they are consumed only after
	// withAuditWrite returns nil (i.e. the commit succeeded), so a rollback
	// never publishes stale values.
	var (
		resolvedAgentId string
		resolvedEventId int64
	)

	// dedup_key is NULL for ordinary callers (the partial unique index ignores
	// NULLs, so every legacy write inserts a fresh row as before). The durable
	// engine supplies a deterministic key; the ON CONFLICT clause targets the
	// partial index so a replayed emission is a silent no-op instead of a
	// duplicate row.
	var dedupKey any
	if event.DedupKey != "" {
		dedupKey = event.DedupKey
	}

	writeErr := s.withAuditWrite(ctx, "RecordEvent", func(ctx context.Context, tx *sql.Tx) error {
		// Resolve (or mint) the agent_id inside the transaction so the
		// agent registration and the event row are atomic. roleMu is NOT
		// held across this call — see resolveLegacyRoleAgentId for details.
		agentId, err := s.resolveLegacyRoleAgentId(ctx, tx, event.Role)
		if err != nil {
			return err
		}
		resolvedAgentId = agentId

		// Post-v4 schema: audit_events.epoch_id is gone. Epoch attachment is
		// expressed as a context_edges (event_id, 'EpochContext', epoch_id)
		// row written inside the same transaction so a crash between INSERT
		// and AttachContext cannot leave a row without its epoch correlation.
		res, err := tx.ExecContext(ctx,
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
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't write this audit event into the database (epoch %q, event type %q).",
					event.EpochId, event.EventType,
				),
				Why:   "The database refused the write into the audit-events table.",
				Where: "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
				Impact: "The event was not recorded. The audit trail for this epoch will be missing this entry\n" +
					"until the underlying database problem is fixed and the caller retries.",
				Fix: "1. Confirm the audit database file is accessible:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. Check the file isn't corrupted:\n" +
					"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
					"3. Retry the operation once the file is healthy.",
				Cause: err,
			}
		}

		// Exactly-once dedup path: when the engine supplied a dedup_key and
		// the ON CONFLICT clause skipped the insert (a replay), no new row
		// was written. Recover the canonical id from the existing row by its
		// dedup_key. Do NOT insert another context_edges row — the original
		// write already linked the epoch.
		if dedupKey != nil {
			affected, aerr := res.RowsAffected()
			if aerr != nil {
				return &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What: fmt.Sprintf(
						"Couldn't determine whether the audit event was a dedup replay (epoch %q, event type %q).",
						event.EpochId, event.EventType,
					),
					Why:    "The database driver returned an error when querying the affected row count.",
					Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
					Impact: "The write cannot proceed safely without knowing whether to skip context attachment.",
					Fix:    "Retry the operation. If the error persists, check the database integrity.",
					Cause:  aerr,
				}
			}
			if affected == 0 {
				// Replay: recover the existing row id.
				var existingId int64
				if serr := tx.QueryRowContext(ctx,
					`SELECT id FROM audit_events WHERE dedup_key = ?`, event.DedupKey,
				).Scan(&existingId); serr != nil {
					return &pasterrors.StructuredError{
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
				resolvedEventId = existingId
				return nil
			}
		}

		// Recover the just-inserted row id from THIS INSERT statement's
		// result. LastInsertId is per-statement on modernc.org/sqlite, so the
		// returned value is always the rowid of the row this Exec wrote —
		// independent of any concurrent INSERTs on the same connection.
		eventId, err := res.LastInsertId()
		if err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't recover the uncommitted audit event's row ID (epoch %q, event type %q).",
					event.EpochId, event.EventType,
				),
				Why:    "The database refused to report the last inserted row ID.",
				Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
				Impact: "This transaction cannot complete; the event and any new agent registration will be rolled back.",
				Fix: "1. SQLite normally always reports the last-inserted row ID, so this is unexpected.\n" +
					"   Confirm the SQLite driver version is current:\n" +
					"     pasture --version\n" +
					"2. Retry the operation. If the error persists, please file a bug.",
				Cause: err,
			}
		}
		if eventId <= 0 {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"The uncommitted audit event has an invalid row ID (%d) for epoch %q, event type %q.",
					eventId, event.EpochId, event.EventType,
				),
				Why: "Row IDs in the audit-events table count up from 1. A value of zero or below means\n" +
					"the table's auto-numbering has been reset or the table is corrupted.",
				Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
				Impact: "The transaction cannot safely attach context or commit; its event and any new agent registration will be rolled back.",
				Fix: "1. Look at the most recent rows in the audit-events table to confirm the corruption:\n" +
					"     sqlite3 <path-to-audit.db> 'SELECT id FROM audit_events ORDER BY id DESC LIMIT 5'\n" +
					"2. If the IDs really are non-positive, restore from a backup and please file a bug.",
			}
		}
		resolvedEventId = eventId

		if event.EpochId != "" {
			// INSERT OR IGNORE: a duplicate triple (event_id, 'EpochContext',
			// epochId) is a no-op. Production never produces duplicates here
			// because event_id is freshly minted; the OR IGNORE defends
			// against future callers that may write multi-context events
			// idempotently and still expect this method to succeed.
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
				eventId, "EpochContext", event.EpochId,
			); err != nil {
				return &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What: fmt.Sprintf(
						"Couldn't link the audit event to its epoch (epoch %q, event type %q, event row %d).",
						event.EpochId, event.EventType, eventId,
					),
					Why:    "The database refused the insert that links the event to its epoch.",
					Where:  "Recording an audit event (internal/audit/sqlite.go in audit.SqliteAuditTrail.RecordEventReturningId).",
					Impact: "The event, its epoch link and any new agent registration will be rolled back together; no partial event will be published.",
					Fix: "1. Confirm the event-to-context table exists (it's created by the version 2 → 3\n" +
						"   audit-database upgrade):\n" +
						"     sqlite3 <path-to-audit.db> '.schema context_edges'\n" +
						"2. If it's missing, the database is older than version 3 — upgrade it:\n" +
						"     pasture migrate",
					Cause: err,
				}
			}
		}
		return nil
	})

	if writeErr != nil {
		return 0, writeErr
	}

	// The commit succeeded. Publish the role→agent_id mapping to the cache
	// so subsequent writes for the same role skip the DB lookup. We do this
	// AFTER the commit (never inside the callback) so a rolled-back write
	// never populates the cache with a stale or partial registration.
	if resolvedAgentId != "" {
		s.roleMu.Lock()
		s.roleToAgentId[event.Role] = resolvedAgentId
		s.roleMu.Unlock()
	}
	return resolvedEventId, nil
}

// resolveLegacyRoleAgentId returns the agent_id for a given role, minting a
// SoftwareAgent on first use. All DB operations run inside the provided
// transaction (tx) so agent registration and the parent event INSERT are
// fully atomic — a rollback of the parent transaction also rolls back any
// newly-minted agent rows.
//
// Cache lookup happens under the transaction's write lock to
// amortise the DB cost on repeated writes. When a cache hit is found, no
// DB work is done at all. When there is no cache hit, the function queries
// and (if necessary) inserts INSIDE tx without holding roleMu — the tx
// serialises the DB work, and the caller publishes to the cache only after
// the parent withAuditWrite commit succeeds.
//
// The well-known direct-lookup path (pasture/automaton/ prefix) also runs
// inside tx so it participates in the same atomicity contract. If the lookup
// misses (agent not yet registered), it falls through to the legacy-role
// find-or-create path.
func (s *SqliteAuditTrail) resolveLegacyRoleAgentId(ctx context.Context, tx *sql.Tx, role string) (string, error) {
	// Fast path: cache hit avoids any DB work.
	s.roleMu.Lock()
	if cached, ok := s.roleToAgentId[role]; ok {
		s.roleMu.Unlock()
		return cached, nil
	}
	s.roleMu.Unlock()

	// Well-known direct lookup: if the role names a registered well-known
	// automaton (prefix "pasture/automaton/..."), bind directly to the
	// existing agents_software row without the legacy-role prefix dance.
	// This prevents shadow "pasture/legacy-role/pasture/automaton/.." rows.
	//
	// If the lookup misses (not yet registered, e.g. test context without
	// daemon startup), fall through to legacy-role find-or-create.
	if strings.HasPrefix(role, "pasture/automaton/") {
		var directExisting string
		derr := tx.QueryRowContext(ctx,
			`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
			 WHERE a.kind_id = 2 AND s.name = ? LIMIT 1`,
			role,
		).Scan(&directExisting)
		switch {
		case derr == nil:
			// Found — return without publishing to cache; the caller
			// publishes after a successful commit.
			return directExisting, nil
		case derr != sql.ErrNoRows:
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
		// derr == sql.ErrNoRows: fall through to legacy-role find-or-create.
	}

	name := legacyRoleAgentNamePrefix + role

	// Find branch — common case after the first write for this role.
	var existing string
	err := tx.QueryRowContext(ctx,
		`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
		 WHERE a.kind_id = 2 AND s.name = ? LIMIT 1`,
		name,
	).Scan(&existing)
	switch {
	case err == nil:
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
	agentId := legacyRoleAgentNamespace + "/" + newUUID.String()

	if _, err := tx.ExecContext(ctx,
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
				"3. Check uniqueness and foreign-key failures before retrying; they are not transient\n" +
				"   lock contention and must be repaired first.",
			Cause: err,
		}
	}

	if _, err := tx.ExecContext(ctx,
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
			Impact: "The agent registration is within the transaction: if the parent write rolls back, the\n" +
				"agent row rolls back with it. No orphan will remain.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent details table still exists and is intact:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
				"3. Retry the operation once the file is healthy.",
			Cause: err,
		}
	}

	// Return the newly-minted agent_id. The caller (RecordEventReturningId)
	// will publish it to the cache only after withAuditWrite commits.
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
// database in a single atomic transaction via withAuditWrite. Nil or empty
// slices are accepted as no-ops.
//
// All entries are prepared once with tx.PrepareContext and executed with
// stmt.ExecContext so the caller's context governs each row write. If any
// INSERT fails the entire batch is rolled back (withAuditWrite owns the
// transaction boundary; the callback must not commit or rollback).
func (s *SqliteAuditTrail) RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	if len(entries) == 0 {
		return nil
	}

	return s.withAuditWrite(ctx, "RecordSessionEntries", func(ctx context.Context, tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
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
			if _, err := stmt.ExecContext(ctx,
				e.SessionId, e.EntryIndex, e.Provider, e.EntryType, e.Role,
				e.TimestampMs, e.ContentPreview, e.TokensIn, e.TokensOut,
				boolToInt(e.HasToolUse), e.ToolKind, e.ToolNamesCsv,
				boolToInt(e.HasThinking), boolToInt(e.IsError), e.StopReason, e.RawByteLength,
				e.ToolCallId, e.EntryId, e.ParentEntryId, e.Depth,
				e.ParentIndex, e.ToolInput, e.ToolOutput, e.Extra,
			); err != nil {
				return fmt.Errorf(
					"audit.SqliteAuditTrail.RecordSessionEntries: INSERT failed for entry[%d] "+
						"(sessionId=%q, entryIndex=%d): %w — "+
						"check that the database file is still accessible",
					i, e.SessionId, e.EntryIndex, err,
				)
			}
		}
		return nil
	})
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
func (s *SqliteAuditTrail) QuerySessionEntries(ctx context.Context, sessionId string) ([]protocol.SessionEntry, error) {
	if ctx == nil {
		return nil, contextValidationError("QuerySessionEntries")
	}
	rows, err := s.db.QueryContext(ctx, `
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
	return ensureSchemaContext(context.Background(), db)
}

func ensureSchemaContext(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
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

	_, err = db.ExecContext(ctx, `
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

	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_session_id ON session_entries (session_id)`)
	if err != nil {
		return fmt.Errorf("create index idx_session_id: %w", err)
	}

	return nil
}

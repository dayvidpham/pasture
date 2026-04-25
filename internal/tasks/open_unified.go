// Package tasks — OpenTaskTracker constructor (PROPOSAL-2 §7.4 + §7.10).
//
// open_unified.go pairs with tracker.go: the latter declares the wrapper type;
// this file constructs it. The constructor:
//
//  1. Resolves dbPath (empty → DefaultDBPath).
//  2. Creates the parent directory if missing.
//  3. Opens the audit subsystem (NewSqliteAuditTrail), which internally calls
//     audit.Migrate to bring the file from v1 → MaxKnownSchemaVersion.
//  4. Opens the Provenance subsystem on the same file.
//  5. Defensively ensures the post-v2 pasture tables (context_edges,
//     pasture_agent_categories, pasture_well_known_agents) exist. S2 owns
//     these table creations long-term; the defensive CREATE IF NOT EXISTS
//     here unblocks S5's race test before S2 lands and is a no-op once S2's
//     migrator does the same work in the proper migration step.
//  6. Wires the trio (provenance.Tracker, audit.Trail, *sql.DB) into a
//     trackerImpl and returns it.
//
// init() registers the constructor with pkg/protocol so external callers can
// use protocol.OpenTaskTracker without importing internal/tasks directly.

package tasks

import (
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite" // pure-Go driver; CGO_ENABLED=0 compatible

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// init wires our OpenTaskTracker into pkg/protocol so external packages that
// import only the public façade can call protocol.OpenTaskTracker. The
// indirection is necessary because pkg/protocol cannot import internal/tasks.
func init() {
	protocol.RegisterOpenTaskTracker(openTaskTrackerImpl)
}

// OpenTaskTracker opens the unified pasture.db SQLite file at dbPath, runs
// the audit migrator, opens the Provenance tracker on the same file, and
// returns a wrapped protocol.TaskTracker.
//
// dbPath: filesystem path to the unified database. Empty string resolves to
// DefaultDBPath (honours $PASTURE_DB_PATH and $XDG_DATA_HOME). Parent
// directories are created if they do not exist.
//
// Errors are *pasterrors.StructuredError with category:
//   - CategoryConnection (exit 2): file/dir cannot be opened.
//   - CategoryStorage    (exit 5): migration or DDL failure.
//   - CategoryValidation (exit 1): newer-schema rejection.
//
// Callers MUST call Close on the returned tracker to release file handles.
//
// This is the in-package entry point. External callers should use
// protocol.OpenTaskTracker, which is wired through init() above.
func OpenTaskTracker(dbPath string) (protocol.TaskTracker, error) {
	return openTaskTrackerImpl(dbPath)
}

// openTaskTrackerImpl is the actual constructor. Split out so init() can
// register it without recursive resolution of the public OpenTaskTracker
// symbol via the protocol package.
func openTaskTrackerImpl(dbPath string) (protocol.TaskTracker, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("tasks.OpenTaskTracker: could not create parent directory for %q", dbPath),
			Why:      err.Error(),
			Impact:   "the unified pasture database cannot be opened until the parent directory is writable",
			Fix:      fmt.Sprintf("create the directory manually with `mkdir -p %q`, or override with --db <path> / $%s", filepath.Dir(dbPath), DBPathEnv),
		}
	}

	// Open audit first so its migrator runs and creates audit_schema_meta
	// (and any of its own tables). NewSqliteAuditTrail invokes audit.Migrate
	// internally; if Migrate returns a CategoryStorage / CategoryValidation
	// error (newer-schema rejection), it propagates out unchanged via %w.
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		return nil, wrapOpenError(dbPath, "audit subsystem", err)
	}

	// Open the audit *sql.DB handle for our pasture-only methods. We open a
	// fresh handle (not reuse trail's) to avoid taking a hidden reference
	// into audit's struct internals — the modernc.org/sqlite driver shares
	// the underlying file (via WAL) so writes through either handle hit the
	// same disk state. SetMaxOpenConns(1) on this handle plus busy_timeout
	// gives us the same single-writer serialisation as the audit handle.
	auditDB, err := openAuditHandle(dbPath)
	if err != nil {
		_ = trail.Close()
		return nil, err
	}

	// Defensive: ensure the post-v2 pasture tables exist. S2 will land the
	// proper v2→v3 migration; until then this CREATE IF NOT EXISTS unblocks
	// S5's race test (which writes to context_edges) without forking the
	// schema. Once S2 lands the migrator runs the same DDL inside its
	// transaction and this call is a no-op.
	if err := ensurePastureTables(auditDB); err != nil {
		_ = auditDB.Close()
		_ = trail.Close()
		return nil, err
	}

	// Open Provenance on the same file. provenance.OpenSQLite manages its
	// own *sql.DB handle (separate from auditDB). Both handles target the
	// same on-disk file via the modernc/sqlite driver; WAL mode + a
	// 5000ms busy_timeout (set by NewSqliteAuditTrail's pragmas) provide
	// cross-handle serialisation.
	prov, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		_ = auditDB.Close()
		_ = trail.Close()
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("tasks.OpenTaskTracker: could not open Provenance subsystem at %q", dbPath),
			Why:      err.Error(),
			Impact:   "the unified tracker cannot be returned because the Provenance task graph is unreachable",
			Fix:      fmt.Sprintf("verify the file exists, is a valid SQLite database, and is not held open by another process; override the path with --db <path> or $%s", DBPathEnv),
		}
	}

	return newTrackerImpl(prov, trail, auditDB), nil
}

// openAuditHandle opens a private *sql.DB on the same SQLite file used by
// audit and applies the same pragmas (WAL + busy_timeout=5000) so writes
// from this handle serialise correctly against audit and Provenance writes.
func openAuditHandle(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("tasks.OpenTaskTracker: could not open auxiliary sql.DB handle at %q", dbPath),
			Why:      err.Error(),
			Impact:   "the unified tracker's pasture-side methods (AttachContext, SetAgentCategories, Timeline) cannot reach the database",
			Fix:      "verify the file exists and is readable; if the audit subsystem opened cleanly the same path should work here",
		}
	}

	// Single-writer serialisation. Multiple handles into modernc/sqlite share
	// the file via WAL, so this max-conns=1 prevents *this* handle from
	// queuing writers against itself. Cross-handle (this vs audit vs
	// Provenance) serialisation comes from SQLite's file lock + WAL.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.OpenTaskTracker: PRAGMA %q failed on %q", p, dbPath),
				Why:      err.Error(),
				Impact:   "the unified tracker's auxiliary handle does not have the expected concurrency settings; cross-subsystem writes may deadlock",
				Fix:      "verify the SQLite file is writable; if this fails reproducibly the file may be on a filesystem that doesn't support WAL",
			}
		}
	}
	return db, nil
}

// wrapOpenError preserves an upstream *StructuredError if the underlying
// error already has the right shape, else wraps it as CategoryConnection.
// We use it for the audit subsystem because audit.NewSqliteAuditTrail's
// errors are not yet *StructuredError (a future S1 follow-up) but Migrate's
// errors are.
func wrapOpenError(dbPath, subsystem string, err error) error {
	// If the underlying error is already a StructuredError (e.g. from the
	// migrator), surface it unchanged so callers see the right Category and
	// exit code. errors.As walks any %w chain.
	var se *pasterrors.StructuredError
	if stderrors.As(err, &se) {
		return se
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryConnection,
		What:     fmt.Sprintf("tasks.OpenTaskTracker: could not open %s at %q", subsystem, dbPath),
		Why:      err.Error(),
		Impact:   fmt.Sprintf("the unified tracker cannot be returned because the %s is unreachable", subsystem),
		Fix:      fmt.Sprintf("verify the file exists, the parent directory is writable, and no other pasture process is holding the file open; override the path with --db <path> or $%s", DBPathEnv),
	}
}

// ─── ensurePastureTables: defensive DDL until S2's migrator lands ────────────

// ensurePastureTables creates context_edges, pasture_agent_categories, and
// pasture_well_known_agents if they do not already exist. Idempotent.
//
// This is a temporary bridge: S2 owns these table creations long-term as part
// of the v2→v3 migration. Until S2 lands, S5 needs them present for the race
// test (which writes to context_edges) and for SetAgentCategories /
// AttachContext callers. Once S2 lands its migrator runs the same DDL inside
// the migration transaction and this call becomes a no-op (CREATE IF NOT
// EXISTS is the contract).
//
// Schema is verbatim from PROPOSAL-2 §7.2. Indexes are included so query
// performance does not regress when S6 wires up the CLI subcommands.
//
// Called from:
//   - openTaskTrackerImpl (once per Open)
//   - SetAgentCategories / AgentCategories / AttachContext / EventContexts /
//     Timeline (defensively, in case the wrapper is constructed via test
//     helpers that bypass openTaskTrackerImpl).
func ensurePastureTables(db *sql.DB) error {
	if db == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "tasks.ensurePastureTables: db handle is nil",
			Why:      "the auxiliary *sql.DB was not opened before ensurePastureTables was called",
			Impact:   "the pasture-side tables (context_edges, pasture_agent_categories, pasture_well_known_agents) cannot be created",
			Fix:      "this is a programming error — open the database via OpenTaskTracker before calling pasture-side methods",
		}
	}

	statements := []struct {
		name string
		ddl  string
	}{
		{
			name: "context_edges",
			ddl: `CREATE TABLE IF NOT EXISTS context_edges (
				event_id     INTEGER NOT NULL REFERENCES audit_events(id) ON DELETE CASCADE,
				context_kind TEXT    NOT NULL,
				context_id   TEXT    NOT NULL,
				PRIMARY KEY (event_id, context_kind, context_id)
			)`,
		},
		{
			name: "idx_context_edges_lookup",
			ddl:  `CREATE INDEX IF NOT EXISTS idx_context_edges_lookup ON context_edges (context_kind, context_id)`,
		},
		{
			name: "idx_context_edges_event",
			ddl:  `CREATE INDEX IF NOT EXISTS idx_context_edges_event ON context_edges (event_id)`,
		},
		{
			name: "pasture_agent_categories",
			ddl: `CREATE TABLE IF NOT EXISTS pasture_agent_categories (
				agent_id        TEXT PRIMARY KEY,
				automaton_role  TEXT NOT NULL DEFAULT 'None',
				pasture_role    TEXT NOT NULL DEFAULT 'None'
			)`,
		},
		{
			name: "pasture_well_known_agents",
			ddl: `CREATE TABLE IF NOT EXISTS pasture_well_known_agents (
				agent_id  TEXT PRIMARY KEY,
				name      TEXT NOT NULL UNIQUE
			)`,
		},
	}

	for _, s := range statements {
		if _, err := db.Exec(s.ddl); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.ensurePastureTables: failed to create %s", s.name),
				Why:      err.Error(),
				Impact:   "pasture-side tables are not available; SetAgentCategories / AttachContext / EventContexts / Timeline will all fail with storage errors",
				Fix:      "verify the SQLite file is writable and the disk has space; if the DDL itself is rejected, run 'pasture migrate' to apply the latest schema",
			}
		}
	}
	return nil
}

// ─── decodeAuditEvent: shared row-scan helper for Timeline + future queries ──

// decodeAuditEvent reconstructs a protocol.AuditEvent from raw row values.
// Centralised here so Timeline, future event-listing queries, and any future
// CLI-side decoders share the same JSON-unmarshal + UTC reconciliation logic.
func decodeAuditEvent(epochID, phaseStr, role, eventTypeStr, payloadJSON string, tsNano int64) (protocol.AuditEvent, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return protocol.AuditEvent{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.decodeAuditEvent: payload unmarshal failed for epoch=%q event_type=%q", epochID, eventTypeStr),
			Why:      err.Error(),
			Impact:   "the row cannot be returned because its JSON payload is corrupt",
			Fix:      "inspect the row directly via 'sqlite3 <db> \"SELECT payload FROM audit_events WHERE id = ...\"' and either repair the JSON or drop the row",
		}
	}
	return protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     protocol.PhaseId(phaseStr),
		Role:      role,
		EventType: protocol.EventType(eventTypeStr),
		Payload:   payload,
		Timestamp: time.Unix(0, tsNano).UTC(),
	}, nil
}

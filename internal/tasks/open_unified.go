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
	"strings"
	"time"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite" // pure-Go driver; CGO_ENABLED=0 compatible

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
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
			What:     "Couldn't create the folder for the pasture database.",
			Why: fmt.Sprintf(
				"Tried to create %q so the database file %q could live there, but the\n"+
					"operating system rejected it.",
				filepath.Dir(dbPath), dbPath,
			),
			Where:  "Opening the pasture database (internal/tasks/open_unified.go in tasks.openTaskTrackerImpl).",
			Impact: "The pasture database can't be opened until that folder exists and is writable.",
			Fix: fmt.Sprintf("1. Create the folder yourself:\n"+
				"     mkdir -p %q\n"+
				"2. Or point pasture at a folder you can write to:\n"+
				"     pasture task --db <writable-path> ...\n"+
				"   You can also set the environment variable %s.",
				filepath.Dir(dbPath), DBPathEnv),
			Cause: err,
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
	// same disk state. The shared DSN (WAL + busy_timeout + _txlock=immediate)
	// gives this handle the same write serialisation as the audit handle.
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
	// 5000ms busy_timeout (applied via the shared DSN on the pasture handles)
	// provide cross-handle serialisation.
	prov, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		_ = auditDB.Close()
		_ = trail.Close()
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't open the part of the database that holds your tasks.",
			Why: fmt.Sprintf(
				"Tried to open the database file at %q for task storage, but it failed.",
				dbPath,
			),
			Where:  "Opening the pasture database (internal/tasks/open_unified.go in tasks.openTaskTrackerImpl).",
			Impact: "Without the task store, no task commands can run and the daemon can't start.",
			Fix: fmt.Sprintf("1. Confirm the file is a valid SQLite database and isn't already open in\n"+
				"   another pasture process:\n"+
				"     sqlite3 %q .schema\n"+
				"     pgrep -af pastured\n"+
				"2. If the schema looks wrong, run a migration:\n"+
				"     pasture migrate\n"+
				"3. Or point pasture at a different file:\n"+
				"     pasture task --db <path> ...\n"+
				"   You can also set the environment variable %s.",
				dbPath, DBPathEnv),
			Cause: err,
		}
	}

	return newTrackerImpl(prov, trail, auditDB), nil
}

// openAuditHandle opens a private *sql.DB on the same SQLite file used by
// audit, configured via the shared DSN (WAL + busy_timeout=5000 +
// synchronous=NORMAL + foreign_keys=ON + _txlock=immediate) so writes from this
// handle serialise correctly against audit and Provenance writes. The WAL
// multi-writer model + busy_timeout replaces the former single-connection cap.
func openAuditHandle(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't open a second connection to the pasture database.",
			Why: fmt.Sprintf(
				"Tried to open another database handle on %q (used for audit-context and\n"+
					"agent-category writes), but it failed.",
				dbPath,
			),
			Where: "Opening the pasture database (internal/tasks/open_unified.go in tasks.openAuditHandle).",
			Impact: "Some pasture features (linking events to epochs, recording agent categories,\n" +
				"and reading back epoch timelines) won't work until this connection opens.",
			Fix: fmt.Sprintf("1. Confirm the database file is readable:\n"+
				"     ls -l %q\n"+
				"     sqlite3 %q .schema\n"+
				"2. If something else is holding it exclusively, stop that process and retry:\n"+
				"     pgrep -af pastured",
				dbPath, dbPath),
			Cause: err,
		}
	}

	// The shared DSN already applied WAL + busy_timeout + synchronous +
	// foreign_keys + _txlock=immediate to every connection in the pool, so no
	// runtime PRAGMAs are needed. This pasture-owned write handle keeps the
	// single-connection cap (like the audit handle) to serialise its writers at
	// the Go level — the proven model the cross-subsystem race test exercises.
	// Only the DBOS engine handle is uncapped, because its poller needs a second
	// concurrent connection.
	db.SetMaxOpenConns(1)
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
		What:     fmt.Sprintf("Couldn't open the %s in the pasture database.", subsystem),
		Why: fmt.Sprintf(
			"Tried to open the database file at %q for the %s, but it failed.",
			dbPath, subsystem,
		),
		Where: "Opening the pasture database (internal/tasks/open_unified.go in tasks.wrapOpenError).",
		Impact: fmt.Sprintf(
			"Pasture needs the %s to function — no commands can run until it opens.",
			subsystem,
		),
		Fix: fmt.Sprintf("1. Confirm the file exists and isn't held by another pasture process:\n"+
			"     ls -l %q\n"+
			"     pgrep -af pastured\n"+
			"2. Make sure the folder it lives in is writable:\n"+
			"     mkdir -p %q\n"+
			"3. If the file looks corrupt, move it aside and let pasture rebuild it:\n"+
			"     mv %q %q.broken\n"+
			"4. Or point pasture at a different file:\n"+
			"     pasture task --db <path> ...\n"+
			"   You can also set the environment variable %s.",
			dbPath, filepath.Dir(dbPath), dbPath, dbPath, DBPathEnv),
		Cause: err,
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
			What:     "Pasture tried to set up its database tables without an open connection.",
			Why: "An internal helper was called before the database was opened. This is a\n" +
				"bug in the code that constructed the task tracker — a real connection\n" +
				"should always be passed in first.",
			Where: "Setting up the pasture-side tables (internal/tasks/open_unified.go in tasks.ensurePastureTables).",
			Impact: "Pasture's audit-link, agent-category, and well-known-agent tables can't\n" +
				"be created, so anything that writes to them will fail right after.",
			Fix: "1. Open the database through the supported entry point first.\n" +
				"2. Then use that tracker for any pasture-side calls.\n" +
				"   If you hit this from the CLI rather than from your own code, please\n" +
				"   file a bug — it shouldn't be reachable in normal use.",
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
				What:     fmt.Sprintf("Pasture couldn't create the %q table in the database.", s.name),
				Why:      "Tried to create the table (or its index) but the database refused.",
				Where:    "Setting up the pasture-side tables (internal/tasks/open_unified.go in tasks.ensurePastureTables).",
				Impact: "Pasture features that depend on this table (linking events to epochs,\n" +
					"recording agent categories, looking up well-known agents, and reading\n" +
					"epoch timelines) will fail until the table can be created.",
				Fix: "1. Make sure the database file is writable and the disk has free space:\n" +
					"     df -h .\n" +
					"2. If the schema looks out of date, run a migration:\n" +
					"     pasture migrate\n" +
					"3. Retry the command once the database is healthy.",
				Cause: err,
			}
		}
	}
	return nil
}

// ─── decodeAuditEvent: shared row-scan helper for Timeline + future queries ──

// legacyRoleAgentNamePrefix is the canonical prefix for SoftwareAgent names
// minted from legacy audit_events.role values during the v3 backfill (see
// audit/migrate_v3_backfill.go). decodeAuditEvent strips this prefix from
// the joined agents_software.name when populating event.Role so callers
// see the original role string ("supervisor") rather than the synthetic
// fully-qualified name ("pasture/legacy-role/supervisor").
const legacyRoleAgentNamePrefix = "pasture/legacy-role/"

// decodeAuditEvent reconstructs a protocol.AuditEvent from raw row values.
// Centralised here so Timeline, future event-listing queries, and any future
// CLI-side decoders share the same JSON-unmarshal + UTC reconciliation logic.
//
// The roleOrAgentName argument is either:
//   - The legacy v1/v2 audit_events.role string (legacy DBs), or
//   - The agents_software.name joined via audit_events.agent_id (post-v3).
//
// In the post-v3 case, names with the "pasture/legacy-role/" prefix are
// stripped to recover the original free-string role; other names (S7
// well-known automaton agents, future live SoftwareAgents) are returned
// as-is so the caller still sees a stable, non-empty Role.
func decodeAuditEvent(epochId, phaseStr, roleOrAgentName, eventTypeStr, payloadJSON string, tsNano int64) (protocol.AuditEvent, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return protocol.AuditEvent{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "An audit event in the database has a corrupted payload.",
			Why: fmt.Sprintf(
				"Reading an event of type %q for epoch %q from the database, the saved\n"+
					"payload couldn't be parsed as JSON.",
				eventTypeStr, epochId,
			),
			Where: "Decoding an audit event (internal/tasks/open_unified.go in tasks.decodeAuditEvent).",
			Impact: "This event can't be returned in queries or timelines until the payload\n" +
				"is repaired or the row is removed.",
			Fix: "1. Look at the broken row directly to see what's stored:\n" +
				"     sqlite3 <db> \"SELECT id, payload FROM audit_events \\\n" +
				"                    WHERE event_type = '<event-type>'\"\n" +
				"2. Either fix the JSON in place or remove the row, then retry the query.\n" +
				"   Removing rows is destructive — back up the database file first.",
			Cause: err,
		}
	}
	role := roleOrAgentName
	if strings.HasPrefix(role, legacyRoleAgentNamePrefix) {
		role = role[len(legacyRoleAgentNamePrefix):]
	}
	return protocol.AuditEvent{
		EpochId:   epochId,
		Phase:     protocol.PhaseId(phaseStr),
		Role:      role,
		EventType: protocol.EventType(eventTypeStr),
		Payload:   payload,
		Timestamp: time.Unix(0, tsNano).UTC(),
	}, nil
}

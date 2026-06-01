// Package audit — migrate_v3_v4.go
//
// S4 EpochContext migration: extends the schema chain with the v3→v4 step.
//
// The v4 step does two pieces of work in the SAME BEGIN IMMEDIATE
// transaction owned by migrate.runStep:
//
//  1. Backfill context_edges from audit_events.epoch_id. For every row in
//     audit_events with non-NULL epoch_id, INSERT a context_edges
//     (event_id, 'EpochContext', epoch_id) row. Uses INSERT OR IGNORE so a
//     defensive replay (impossible in practice — the whole step is one
//     transaction — but safe defensively) is a no-op against the
//     composite PK (event_id, context_kind, context_id).
//  2. SQLite table-rebuild to drop the epoch_id column from audit_events:
//     CREATE NEW (without epoch_id) → INSERT SELECT → DROP OLD → RENAME →
//     recreate post-v3 indexes.
//
// The audit_schema_meta version bump from 3 to 4 is the LAST statement in
// the transaction (per BLOCKER A1 / PROPOSAL-2 §7.10.1 v4 row). A crash
// before tx.Commit rolls everything back, leaving the file observably at
// v3; the next open re-runs the v4 step from scratch (idempotent because
// step 1 uses INSERT OR IGNORE and step 2 lives entirely inside the
// rolled-back transaction).
//
// Migration-note guarantee (PROPOSAL-2 §7.12, last paragraph)
//
// Legacy audit_events.epoch_id values are migrated AS-IS into
// context_edges, regardless of whether the string parses as a Provenance
// TaskId. Free-string epoch IDs (e.g. "epoch-2026-04-22-mvp") and valid
// TaskIDs (e.g. "aura-plugins--01968a3c-1234-7000-8000-...") are both
// preserved because they are historical records and rejecting them would
// lose audit data. The §7.12 ParseTaskId validation applies only to NEW
// workflow starts post-migration; that validation is owned by S8.
//
// Pseudocode parity with PROPOSAL-2 §7.10.1 v4 row:
//
//  1. INSERT INTO context_edges (event_id, context_kind, context_id)
//     SELECT id, 'EpochContext', epoch_id FROM audit_events
//     WHERE epoch_id IS NOT NULL
//  2. CREATE TABLE audit_events_new (no epoch_id column)
//  3. INSERT INTO audit_events_new SELECT (no epoch_id) FROM audit_events
//  4. DROP TABLE audit_events
//  5. ALTER TABLE audit_events_new RENAME TO audit_events
//  6. CREATE INDEX idx_audit_events_agent ON audit_events (agent_id)
//  7. CREATE INDEX idx_audit_events_timestamp ON audit_events (timestamp)
//  8. (Caller — migrate.runStep wrapper — calls writeVersion(4, ...) and Commit.)
package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// migrateV3toV4 advances the audit database from schema version 3 to
// version 4 in the supplied transaction. The transaction MUST already
// hold the SQLite write lock (BEGIN IMMEDIATE from runStep). Caller
// (migrate.runStep) commits the transaction after this function returns
// nil and after writeVersion(4, ...) has been called as the final
// statement.
//
// # Bail-out: missing audit_events
//
// If audit_events does not exist (e.g. a caller invoked audit.Migrate
// against a brand-new SQLite file without first running ensureSchema —
// most fresh-DB tests do this for schema_meta-only assertions), there is
// nothing to backfill OR rebuild. We return nil so the migration
// framework still bumps audit_schema_meta to 4, advertising that the
// binary supports the v4 schema even though no audit_events table exists
// yet. The next NewSqliteAuditTrail open against the same file will
// create audit_events at the post-v3 shape via ensureSchema (which knows
// only the v1 shape; the v3 backfill has its own bail-out for fresh
// files), and the v4 step's bail-out keeps the file internally
// consistent.
//
// # Idempotency on partial-run replay
//
// Step 1 (INSERT OR IGNORE) is individually idempotent: the composite PK
// (event_id, context_kind, context_id) on context_edges rejects
// duplicates without raising, so a partial run that wrote some context
// edges before crashing (impossible — the whole step is one transaction
// — but safe defensively) re-applies as a no-op.
//
// Step 2 (table-rebuild) is NOT individually idempotent: re-running it on
// a v4 schema where epoch_id is already gone would fail at "INSERT INTO
// audit_events_new SELECT id, phase, agent_id, ... FROM audit_events"
// because the SELECT has no epoch_id to copy. This is fine: the whole
// step lives in one transaction, so a partial run is rolled back
// atomically and the retry starts from the v3 state where epoch_id still
// exists.
func migrateV3toV4(tx *sql.Tx, _ int64) error {
	// Bail-out: if audit_events does not exist, the v4 step has no rows
	// to migrate and no column to drop. Returning nil here lets the
	// framework still bump audit_schema_meta to 4 (the writeVersion call
	// in the caller below this function).
	exists, err := auditEventsTableExists(tx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	// Step 1: backfill context_edges from audit_events.epoch_id.
	if err := backfillEpochContext(tx); err != nil {
		return err
	}

	// Step 2: SQLite table-rebuild to drop the epoch_id column.
	if err := rebuildAuditEventsWithoutEpochId(tx); err != nil {
		return err
	}

	// Caller (migrateV3toV4Step in migrate.go) calls writeVersion(4, ...)
	// as the final statement before tx.Commit; see the wrapper.
	return nil
}

// backfillEpochContext copies every audit_events row with non-NULL
// epoch_id into context_edges with kind=EpochContext. Uses INSERT OR
// IGNORE so the composite PK on context_edges silently absorbs any
// duplicate triple (event_id, 'EpochContext', context_id) that a prior
// partial run may have written — though in practice the whole v4 step
// runs in one rolled-back-or-committed transaction, so duplicates are
// not possible across runs of this step against an unchanged file.
//
// Migration-note guarantee (PROPOSAL-2 §7.12 last paragraph): legacy
// free-string epoch IDs (e.g. "epoch-2026-04-22-mvp-042") are copied as
// the literal context_id without any TaskId validation. Rejecting them
// would lose audit history. The §7.12 ParseTaskId validation applies
// only to NEW workflow starts; S8 owns that validation at the workflow
// boundary.
//
// The context_kind value is hardcoded to the string form of
// protocol.ContextEpoch ("EpochContext") so the migration does not
// import a package that itself imports audit (no cycle), and so the
// stored value stays stable even if the protocol enum's internal
// representation is later refactored. The protocol.ContextEpoch
// constant is referenced via a compile-time check below to catch any
// future drift.
func backfillEpochContext(tx *sql.Tx) error {
	// Compile-time anchor: if protocol.ContextEpoch's wire value ever
	// changes from "EpochContext", this function will continue to write
	// the literal "EpochContext" and the test in migrate_v3_v4_test.go
	// will detect the divergence. Capturing the value here also keeps
	// the import live so refactors that drop ContextEpoch fail to
	// compile here too.
	const epochContextLiteral = "EpochContext"
	if string(protocol.ContextEpoch) != epochContextLiteral {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The label this build uses for epoch contexts (%q) doesn't match what the upgrade expects (%q).",
				string(protocol.ContextEpoch), epochContextLiteral,
			),
			Why: "The version 3 → 4 upgrade tags every legacy epoch attachment with the literal label\n" +
				"\"EpochContext\" so older audit events stay findable. Pasture's epoch-context label was\n" +
				"changed in code, but no follow-up upgrade was added to relabel the existing rows.\n" +
				"Running the upgrade as-is would orphan the legacy events.",
			Impact: "The version 3 → 4 upgrade was stopped to avoid making old audit events invisible.\n" +
				"The audit database stays at version 3.",
			Fix: "1. Either revert the epoch-context label in code back to \"EpochContext\" so legacy and\n" +
				"   live events share the same label.\n" +
				"2. Or add a new version 4 → 5 upgrade that rewrites every existing\n" +
				fmt.Sprintf("   context_edges row's label from \"EpochContext\" to %q before the new label\n", string(protocol.ContextEpoch)) +
				"   takes effect. Don't change the constant on its own.",
		}
	}

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO context_edges (event_id, context_kind, context_id)
		 SELECT id, ?, epoch_id FROM audit_events WHERE epoch_id IS NOT NULL`,
		epochContextLiteral,
	); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't copy legacy epoch attachments into the new event-to-context table during the version 3 → 4 upgrade.",
			Why: "The database refused the bulk-copy that moves each audit event's old epoch reference\n" +
				"into the new context-edges table.",
			Where: "Upgrading the audit database from version 3 to 4 (internal/audit/migrate_v3_v4.go in audit.migrateV3toV4Step).",
			Impact: "The version 3 → 4 upgrade can't preserve the link between past audit events and the\n" +
				"epochs they belong to, so it was rolled back. The audit database stays at version 3.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the upgrade chain ran cleanly up to version 3 — the new context-edges table\n" +
				"   is created in version 2 → 3, and the legacy epoch column is still present until\n" +
				"   this step finishes:\n" +
				"     sqlite3 <path-to-audit.db> '.schema audit_events'\n" +
				"     sqlite3 <path-to-audit.db> '.schema context_edges'\n" +
				"3. Re-run the migration once the underlying problem is resolved:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}
	return nil
}

// rebuildAuditEventsWithoutEpochId performs the SQLite table-rebuild
// dance to drop the epoch_id column from audit_events, mirroring S3's
// rebuildAuditEventsWithoutRole pattern (migrate_v3_backfill.go:422-474).
//
// Modern SQLite (>=3.35.0) supports ALTER TABLE DROP COLUMN directly,
// but the documented table-rebuild pattern is more portable across
// SQLite-driver versions and lets us re-create the post-v3 indexes
// explicitly:
//
//  1. CREATE TABLE audit_events_new (post-v4 shape, no epoch_id).
//  2. INSERT INTO audit_events_new SELECT (no epoch_id) FROM audit_events.
//  3. DROP TABLE audit_events.
//  4. ALTER TABLE audit_events_new RENAME TO audit_events.
//  5. CREATE INDEX idx_audit_events_agent ON the renamed table.
//  6. CREATE INDEX idx_audit_events_timestamp ON the renamed table.
//
// The new shape preserves the v3 column ordering with epoch_id removed:
//
//	id INTEGER PRIMARY KEY AUTOINCREMENT
//	phase TEXT
//	agent_id TEXT NOT NULL
//	event_type TEXT NOT NULL
//	payload TEXT NOT NULL
//	timestamp INTEGER NOT NULL
//
// agent_id stays NOT NULL (S3 enforced this); phase stays NULLABLE (S3
// relaxed this from v1's NOT NULL because future free-floating events
// may not have a phase, per §7.10.1 v3 row).
//
// Indexes recreated:
//
//   - idx_audit_events_agent (on agent_id) — primary lookup for
//     attribution queries (S3 created this in v3).
//   - idx_audit_events_timestamp (on timestamp) — supports timeline
//     queries from S6 (S3 created this in v3).
//
// The v1 schema's idx_epoch_id is intentionally NOT recreated — the
// epoch_id column is gone, replaced by context_edges + its
// idx_context_edges_lookup (which S2 already created on
// (context_kind, context_id) — the optimal lookup shape for "events
// for epoch X").
func rebuildAuditEventsWithoutEpochId(tx *sql.Tx) error {
	statements := []struct {
		what string
		sql  string
	}{
		{
			what: "create audit_events_new (post-v4 shape, no epoch_id)",
			sql: `CREATE TABLE audit_events_new (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				phase      TEXT,
				agent_id   TEXT NOT NULL,
				event_type TEXT NOT NULL,
				payload    TEXT NOT NULL,
				timestamp  INTEGER NOT NULL
			)`,
		},
		{
			what: "copy rows from audit_events to audit_events_new (dropping epoch_id)",
			sql: `INSERT INTO audit_events_new (id, phase, agent_id, event_type, payload, timestamp)
			      SELECT id, phase, agent_id, event_type, payload, timestamp FROM audit_events`,
		},
		{
			what: "drop the old audit_events table",
			sql:  `DROP TABLE audit_events`,
		},
		{
			what: "rename audit_events_new to audit_events",
			sql:  `ALTER TABLE audit_events_new RENAME TO audit_events`,
		},
		{
			what: "create idx_audit_events_agent",
			sql:  `CREATE INDEX IF NOT EXISTS idx_audit_events_agent ON audit_events (agent_id)`,
		},
		{
			what: "create idx_audit_events_timestamp",
			sql:  `CREATE INDEX IF NOT EXISTS idx_audit_events_timestamp ON audit_events (timestamp)`,
		},
	}

	for _, s := range statements {
		if _, err := tx.Exec(s.sql); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't %s while rebuilding the audit-events table for the version 3 → 4 upgrade.",
					s.what,
				),
				Why: "The database refused one of the steps in the table-rebuild dance (create new table,\n" +
					"copy rows, drop old, rename, recreate indexes).",
				Where: "Upgrading the audit database from version 3 to 4 (internal/audit/migrate_v3_v4.go in audit.rebuildAuditEventsWithoutEpochId).",
				Impact: "The audit-events table rebuild stopped midway, so the entire version 3 → 4 upgrade\n" +
					"was rolled back. The audit database stays at version 3.",
				Fix: "1. Confirm the audit database file is writable:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. This usually means the audit-events table is in an unexpected shape after version\n" +
					"   3. Inspect it before retrying:\n" +
					"     sqlite3 <path-to-audit.db> '.schema audit_events'\n" +
					"   The expected version 3 columns are: id, epoch_id, phase, agent_id, event_type,\n" +
					"   payload, timestamp.\n" +
					"3. Re-run the migration once the table shape matches:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
	}
	return nil
}

// migrateV3toV4Step is the migration framework's entry point for the
// v3→v4 hop. It runs the migrateV3toV4 body, then bumps
// audit_schema_meta from 3 to 4 as the LAST statement before the
// caller's tx.Commit (per BLOCKER A1 / PROPOSAL-2 §7.10.1 v4 row crash-
// safety contract).
//
// A crash anywhere inside this function — including between the body
// and the writeVersion call — rolls back the entire transaction
// atomically because the caller (migrate.runStep) holds the
// BEGIN IMMEDIATE lock and only commits if this function returns nil.
// The file remains observably at v3; the next open re-runs the v4 step
// from scratch.
func migrateV3toV4Step(tx *sql.Tx, nowUnixNano int64) error {
	if err := migrateV3toV4(tx, nowUnixNano); err != nil {
		return err
	}
	// Final statement before commit: bump audit_schema_meta to 4.
	return writeVersion(tx, 4, nowUnixNano)
}

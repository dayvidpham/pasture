// Package audit — migrate_v3_backfill.go
//
// S3 backfill: extends the v2→v3 transition (begun by S2 in migrate_v2_v3.go)
// with the audit_events.agent_id add + role-backfill + role-drop triple.
//
// All five steps below run inside the SAME BEGIN IMMEDIATE transaction owned
// by migrate.runStep — including the audit_schema_meta version bump from 2
// to 3, which is the LAST statement before tx.Commit per BLOCKER A1
// (PROPOSAL-2 §7.10.2). A crash mid-way leaves the file at v2 (the
// transaction is rolled back); on the next open, Migrate observes
// MAX(version)=2 and re-runs the v3 step from scratch with no orphan
// agents_software rows (idempotency guaranteed by the find-or-create
// branch in step 3).
//
// Pseudocode parity with PROPOSAL-2 §7.10.2:
//
//  1. ALTER TABLE audit_events ADD COLUMN agent_id TEXT
//  2. SELECT DISTINCT role FROM audit_events
//  3. For each role: find-or-create a SoftwareAgent in agents_software named
//     "pasture/legacy-role/<role>" via raw SQL (no Provenance Go API call).
//     Find query: SELECT a.id FROM agents a JOIN agents_software s ...
//     WHERE a.kind_id = 2 AND s.name = ? LIMIT 1. If absent, INSERT into
//     agents (id, kind_id=2) and agents_software (agent_id, name, version,
//     source). The INSERT shape MUST match Provenance's
//     RegisterSoftwareAgent (provenance/internal/sqlite/agents.go:96-121)
//     byte-for-byte so a future Provenance lookup of the agent succeeds.
//  4. UPDATE audit_events SET agent_id = ? WHERE role = ? AND agent_id IS NULL
//  5. SQLite table-rebuild to drop the role column: CREATE NEW → INSERT
//     SELECT → DROP OLD → RENAME → recreate indexes.
//
// C4 compliance: Provenance is NOT modified. The audit migrator opens its
// own *sql.DB against the same shared SQLite file and writes raw SQL that
// matches Provenance's INSERT shape exactly. Find-or-create on
// (kind_id=2, name) is keyed precisely so a partial run that crashed after
// agent creation but before the version bump reuses the existing row on
// retry rather than creating a duplicate.
package audit

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// legacyRoleAgentNamePrefix is the canonical prefix for SoftwareAgent names
// minted from legacy audit_events.role values during the v3 backfill.
//
// The full name is "pasture/legacy-role/<role>" — e.g.
// "pasture/legacy-role/architect". Tests assert this exact prefix because:
//
//   - It scopes the legacy-role agents into a documented namespace so
//     operators can grep them out of agents_software (e.g.
//     SELECT * FROM agents_software WHERE name LIKE 'pasture/legacy-role/%').
//   - It is distinct from S7's well-known agent names (pasture/automaton/...,
//     etc.) so the two lifecycles never collide.
//   - PROPOSAL-2 §7.10.2 specifies this exact string in pseudocode; changing
//     it would silently invalidate Scenario 4's idempotency proof.
const legacyRoleAgentNamePrefix = "pasture/legacy-role/"

// legacyRoleAgentVersion and legacyRoleAgentSource fill the (version, source)
// columns of agents_software when minting a new legacy-role agent.
//
// The values are deliberately stable strings — operators reading
// agents_software see a clear provenance for these synthetic agents
// ("v0" + module path) without needing to consult the migration code.
const (
	legacyRoleAgentVersion = "v0"
	legacyRoleAgentSource  = "pasture/internal/audit/migrate"
)

// legacyRoleAgentNamespace is the AgentID namespace minted for synthetic
// legacy-role SoftwareAgents during the v3 backfill.
//
// Provenance's AgentID String() returns "<namespace>--<uuid>" (see
// provenance/pkg/ptypes/types.go:91). The migrator uses "pasture" as the
// namespace because (a) the agent is owned by the pasture binary, not by
// any external system, and (b) Provenance's RegisterSoftwareAgent
// (agents.go:97) takes a namespace argument and pasture's S7 wiring will
// also use "pasture" — keeping the legacy and live agents in the same
// namespace for query symmetry.
const legacyRoleAgentNamespace = "pasture"

// migrateV3Backfill performs the full audit_events.agent_id add + role
// backfill + role drop in the supplied transaction. Caller (migrateV2toV3)
// is responsible for the writeVersion(3, ...) call as the LAST statement
// before commit, per the migration framework contract.
//
// The transaction MUST already hold the SQLite write lock (BEGIN IMMEDIATE
// from runStep). Caller commits.
//
// # Idempotency on partial-run replay
//
// Steps 1, 3, 4 are individually idempotent:
//
//   - Step 1 uses ALTER TABLE ADD COLUMN; SQLite returns "duplicate column
//     name" if the column already exists. We tolerate that error so a
//     re-run after partial commit (impossible in practice — the whole step
//     is one transaction — but safe defensively) is a no-op.
//   - Step 3's find branch covers any prior partial run: if a previous
//     attempt created the agent but rolled back before the version bump,
//     the find query reuses the existing row on retry. (Per §7.10.2
//     paragraph 3, the rollback removes the inserted row, so the find
//     misses on retry and a fresh agent is created — also fine; either
//     branch produces the correct end state.)
//   - Step 4's WHERE includes "AND agent_id IS NULL" so a re-run after a
//     partial agent_id population leaves already-attributed rows alone.
//
// Step 5 (table rebuild) is NOT individually idempotent: re-running it on
// a v3 schema where the role column is already gone would fail because
// "DROP TABLE audit_events" then "ALTER TABLE audit_events_new RENAME"
// has no source. This is fine: the whole step lives in one transaction,
// so a partial run is rolled back atomically and the retry starts from
// the v2 state where the role column still exists.
func migrateV3Backfill(tx *sql.Tx, _ int64) error {
	// Bail-out: if audit_events does not exist (e.g., a caller invoked
	// audit.Migrate against a brand-new SQLite file without first running
	// the v1 ensureSchema bootstrap — most tests do this for the
	// schema_meta-only assertions), there is nothing to backfill. Returning
	// nil here lets the migration framework still bump audit_schema_meta to
	// 3, advertising that the binary supports the v3 schema even though no
	// audit_events table exists yet. The next NewSqliteAuditTrail open
	// against the same file will create audit_events at the post-v3 shape
	// directly via ensureSchema (which has been updated to know about
	// agent_id NOT NULL), and the file will be internally consistent.
	exists, err := auditEventsTableExists(tx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	// Step 0 (defensive): ensure Provenance's required tables exist before
	// we INSERT into them. In production OpenTaskTracker opens Provenance
	// FIRST, so agents/agents_software/agent_kinds are already present
	// when the audit migrator runs. For unit tests that exercise the
	// audit subsystem in isolation (NewSqliteAuditTrail without the
	// Provenance wrapper), and for any future caller that runs the
	// migration outside the standard open path, we create these tables
	// idempotently here.
	//
	// DDL is verbatim from provenance/internal/sqlite/db.go:122-156. The
	// agent_kinds seed (`(0,'human'), (1,'machine_learning'), (2,'software')`)
	// is required because step 3 below INSERTs agents with kind_id=2 which
	// FK-references agent_kinds.id. C4 compliance: this is raw-SQL access
	// to the same on-disk tables Provenance owns, not a modification of
	// the Provenance Go API; the proposal §7.10.2 explicitly sanctions
	// this access pattern.
	if err := ensureProvenanceAgentTables(tx); err != nil {
		return err
	}

	// Step 1: ALTER TABLE audit_events ADD COLUMN agent_id TEXT.
	if err := addAgentIDColumn(tx); err != nil {
		return err
	}

	// Step 2: SELECT DISTINCT role FROM audit_events.
	roles, err := distinctRoles(tx)
	if err != nil {
		return err
	}

	// Step 3: find-or-create SoftwareAgent per role; map role → agent_id.
	roleToAgentID := make(map[string]string, len(roles))
	for _, role := range roles {
		agentID, err := findOrCreateLegacyRoleAgent(tx, role)
		if err != nil {
			return err
		}
		roleToAgentID[role] = agentID
	}

	// Step 4: UPDATE audit_events SET agent_id by role.
	if err := backfillAgentIDByRole(tx, roleToAgentID); err != nil {
		return err
	}

	// Step 5: SQLite table-rebuild to drop the role column.
	if err := rebuildAuditEventsWithoutRole(tx); err != nil {
		return err
	}

	return nil
}

// addAgentIDColumn issues ALTER TABLE audit_events ADD COLUMN agent_id TEXT.
// The column is NULLABLE during the transition so step 4 can populate it
// row-by-row; the table rebuild in step 5 promotes it to NOT NULL.
//
// SQLite ALTER TABLE ADD COLUMN is supported since 3.0; modernc.org/sqlite
// returns "duplicate column name: agent_id" if the column already exists.
// We treat that as success (idempotent retry semantics).
func addAgentIDColumn(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE audit_events ADD COLUMN agent_id TEXT`); err != nil {
		// Tolerate "already exists" so a defensive replay is a no-op.
		if isDuplicateColumnError(err) {
			return nil
		}
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't add the new agent column to the audit-events table during the version 2 → 3 upgrade.",
			Why: fmt.Sprintf(
				"SQLite refused our ALTER TABLE statement that adds the agent column: %s",
				err,
			),
			Impact: "The version 2 → 3 upgrade can't move audit events from being labelled by role text\n" +
				"to being attributed to specific agents. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is writable and not corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-run the migration once the underlying problem is resolved:\n" +
				"     pasture migrate",
		}
	}
	return nil
}

// distinctRoles scans audit_events for unique role values, returning them
// sorted (so subsequent agent creation is deterministic across runs — the
// agent_id minted is non-deterministic per UUIDv7 but the iteration order
// is, which makes test assertions and logs reproducible).
//
// Filters out NULL roles defensively: v1 schema declared role NOT NULL
// (see sqlite.go:391), but a corrupted db could in principle have NULLs.
// A NULL role would map to no agent and the row would remain unattributed,
// which the table rebuild's NOT NULL constraint on agent_id would reject;
// rather than silently lose those rows we exclude them here so the rebuild
// surfaces a clean error if any NULL roles are detected.
func distinctRoles(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT role FROM audit_events WHERE role IS NOT NULL`)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't list the unique role names in the audit-events table during the version 2 → 3 upgrade.",
			Why: fmt.Sprintf(
				"SQLite refused our query that reads the legacy role column from the audit-events table: %s",
				err,
			),
			Impact: "The version 2 → 3 upgrade can't figure out which legacy roles need to become agents,\n" +
				"so it was abandoned. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is readable and not corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-run the migration once the underlying problem is resolved:\n" +
				"     pasture migrate",
		}
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Couldn't read one of the role names from the audit-events table during the version 2 → 3 upgrade.",
				Why: fmt.Sprintf(
					"A row in the legacy role column couldn't be decoded as text: %s",
					err,
				),
				Impact: "The version 2 → 3 upgrade can't finish listing the legacy roles, so it was rolled\n" +
					"back. The audit database stays at version 2.",
				Fix: "1. This usually means a row has a role value that isn't plain text. Inspect the bad\n" +
					"   rows directly:\n" +
					"     sqlite3 <path-to-audit.db> 'SELECT DISTINCT role, typeof(role) FROM audit_events'\n" +
					"2. Fix or delete the offending rows, then re-run the migration:\n" +
					"     pasture migrate",
			}
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Lost the connection to the audit-events table while listing role names during the version 2 → 3 upgrade.",
			Why: fmt.Sprintf(
				"SQLite reported an error part-way through reading the legacy role rows: %s",
				err,
			),
			Impact: "The version 2 → 3 upgrade can't finish listing the legacy roles, so it was rolled\n" +
				"back. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is readable and not corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-run the migration once the underlying problem is resolved:\n" +
				"     pasture migrate",
		}
	}

	sort.Strings(roles)
	return roles, nil
}

// findOrCreateLegacyRoleAgent looks up the SoftwareAgent for legacy role.
// If one exists in agents_software with name "pasture/legacy-role/<role>"
// and kind_id=2, returns its agent_id. Otherwise mints a new UUIDv7,
// inserts (id, kind_id=2) into agents and (agent_id, name, "v0",
// "pasture/internal/audit/migrate") into agents_software, and returns the
// new id.
//
// The find query uses an explicit JOIN+WHERE rather than relying on a
// (kind_id, name) index because Provenance's schema does not declare one
// (db.go:151 — agents_software.name has no UNIQUE constraint). This is
// O(N) where N is the number of agents_software rows; acceptable because
// the migration runs once and the row count is small for legacy
// databases. Future v* migrations could add an index but that lives in
// Provenance, not here, per C4.
//
// AgentID format matches Provenance's mintage exactly:
// "<namespace>--<uuid>" via ptypes.AgentID.String() — see
// provenance/pkg/ptypes/types.go:91. We use namespace "pasture" because
// these agents are owned by the pasture binary and S7's live well-known
// agents will use the same namespace.
func findOrCreateLegacyRoleAgent(tx *sql.Tx, role string) (string, error) {
	name := legacyRoleAgentNamePrefix + role

	var existing string
	err := tx.QueryRow(
		`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
		 WHERE a.kind_id = 2 AND s.name = ? LIMIT 1`,
		name,
	).Scan(&existing)
	switch {
	case err == nil:
		// Found a prior-run agent (or one created by a concurrent
		// Provenance writer using the same name shape). Reuse it.
		return existing, nil
	case err != sql.ErrNoRows:
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't look up the agent named %q in the agent registry during the version 2 → 3 upgrade.",
				name,
			),
			Why: fmt.Sprintf(
				"SQLite refused our query into the agent registry table: %s",
				err,
			),
			Impact: "The upgrade can't tell whether an agent for this legacy role already exists, so it\n" +
				"can't safely create a new one. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is readable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent registry tables (created by the Provenance library) still exist:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
				"3. Re-run the migration once verified:\n" +
				"     pasture migrate",
		}
	}

	// No prior agent — mint a fresh UUIDv7 and insert.
	newUUID, err := uuid.NewV7()
	if err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't generate a unique ID for the legacy role %q during the version 2 → 3 upgrade.",
				role,
			),
			Why: fmt.Sprintf(
				"The UUID generator returned an unexpected error: %s",
				err,
			),
			Impact: "The upgrade can't mint an agent ID for this legacy role, so it was abandoned. The\n" +
				"audit database stays at version 2.",
			Fix: "1. UUID generation is built-in and almost never fails — this usually means the system\n" +
				"   clock is unreadable or set to a wildly invalid value. Check the clock:\n" +
				"     date -u\n" +
				"2. Fix any clock or NTP problems, then re-run:\n" +
				"     pasture migrate",
		}
	}
	agentID := legacyRoleAgentNamespace + "--" + newUUID.String()

	if _, err := tx.Exec(
		`INSERT INTO agents (id, kind_id) VALUES (?, 2)`,
		agentID,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't register the agent for legacy role %q during the version 2 → 3 upgrade.",
				role,
			),
			Why: fmt.Sprintf(
				"SQLite refused our insert into the agent registry (agent ID %q): %s",
				agentID, err,
			),
			Impact: "The upgrade can't add the new agent that legacy events will be attributed to, so it\n" +
				"was abandoned. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the agent registry table (created by the Provenance library) still exists\n" +
				"   and is intact:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
				"3. Re-run the migration once verified:\n" +
				"     pasture migrate",
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO agents_software (agent_id, name, version, source) VALUES (?, ?, ?, ?)`,
		agentID, name, legacyRoleAgentVersion, legacyRoleAgentSource,
	); err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"Couldn't record the agent's name (%q) for legacy role %q during the version 2 → 3 upgrade.",
				name, role,
			),
			Why: fmt.Sprintf(
				"SQLite refused our insert into the software-agent details table: %s",
				err,
			),
			Impact: "The upgrade can't finish registering the agent for this legacy role, so it was\n" +
				"rolled back. The audit database stays at version 2.",
			Fix: "1. Confirm the audit database file is writable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"2. Confirm the software-agent details table (created by the Provenance library) still\n" +
				"   exists and is intact:\n" +
				"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
				"3. Re-run the migration once verified:\n" +
				"     pasture migrate",
		}
	}

	return agentID, nil
}

// backfillAgentIDByRole runs an UPDATE per role mapping audit_events.role to
// the freshly-minted (or reused) agent_id. Only rows with NULL agent_id are
// touched, so a partial-replay scenario where some rows already have an
// attribution leaves them alone.
//
// Iteration order is the sorted slice from distinctRoles → deterministic
// for reproducibility under -race.
func backfillAgentIDByRole(tx *sql.Tx, roleToAgentID map[string]string) error {
	roles := make([]string, 0, len(roleToAgentID))
	for role := range roleToAgentID {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	for _, role := range roles {
		agentID := roleToAgentID[role]
		if _, err := tx.Exec(
			`UPDATE audit_events SET agent_id = ? WHERE role = ? AND agent_id IS NULL`,
			agentID, role,
		); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't attribute audit events for legacy role %q to its new agent during the version 2 → 3 upgrade.",
					role,
				),
				Why: fmt.Sprintf(
					"SQLite refused our update that points each event with role %q at agent ID %q: %s",
					role, agentID, err,
				),
				Impact: "The upgrade can't finish linking legacy events to their new agent records, so it\n" +
					"was rolled back. The audit database stays at version 2.",
				Fix: "1. Confirm the audit database file is writable:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. Re-run the migration once the underlying problem is resolved:\n" +
					"     pasture migrate",
			}
		}
	}
	return nil
}

// rebuildAuditEventsWithoutRole performs the SQLite table-rebuild dance to
// drop the role column. Modern SQLite (>=3.35.0) supports ALTER TABLE DROP
// COLUMN directly; modernc.org/sqlite v1.46.1 ships SQLite 3.43+ which
// handles it. However, ALTER TABLE DROP COLUMN cannot drop columns that
// have CHECK constraints, indexes, or NOT NULL+default that conflict —
// rather than rely on the rule subset we use the documented table-rebuild
// pattern (https://sqlite.org/lang_altertable.html#otheralter):
//
//  1. CREATE TABLE audit_events_new with the target shape.
//  2. INSERT INTO audit_events_new SELECT ... FROM audit_events.
//  3. DROP TABLE audit_events.
//  4. ALTER TABLE audit_events_new RENAME TO audit_events.
//  5. Recreate indexes on the renamed table.
//
// The new shape promotes agent_id to NOT NULL — backfillAgentIDByRole is
// expected to have populated every row, and this constraint is the
// safety net that fails the migration if any row was missed.
//
// The new shape also keeps phase as TEXT (NULLABLE) deliberately:
// PROPOSAL-2 §7.10.1 v3 row shows "phase TEXT" without NOT NULL because
// future free-floating events may not have a phase. The legacy v1 schema
// (sqlite.go:390) had phase NOT NULL; relaxing it here is a conscious
// schema evolution captured in §7.10.2 step 5's pseudocode.
//
// Indexes recreated:
//
//   - idx_audit_events_agent (new, on agent_id) — replaces idx_phase as the
//     primary lookup pattern shifts from "events by phase" to "events by
//     agent" in the unified workflow record (PROPOSAL-2 §7.2).
//   - idx_audit_events_timestamp (new, on timestamp) — supports timeline
//     queries from S6.
//
// idx_epoch_id and idx_phase from the v1 schema are dropped intentionally:
// epoch_id will be replaced by context_edges (S4), and phase becomes a
// free-floating attribute that's queried via context_edges joins, not a
// direct WHERE filter on audit_events.
func rebuildAuditEventsWithoutRole(tx *sql.Tx) error {
	statements := []struct {
		what string
		sql  string
	}{
		{
			what: "create audit_events_new (post-v3 shape, agent_id NOT NULL)",
			sql: `CREATE TABLE audit_events_new (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				epoch_id   TEXT,
				phase      TEXT,
				agent_id   TEXT NOT NULL,
				event_type TEXT NOT NULL,
				payload    TEXT NOT NULL,
				timestamp  INTEGER NOT NULL
			)`,
		},
		{
			what: "copy rows from audit_events to audit_events_new",
			sql: `INSERT INTO audit_events_new (id, epoch_id, phase, agent_id, event_type, payload, timestamp)
			      SELECT id, epoch_id, phase, agent_id, event_type, payload, timestamp FROM audit_events`,
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
					"Couldn't %s while rebuilding the audit-events table for the version 2 → 3 upgrade.",
					s.what,
				),
				Why: fmt.Sprintf(
					"SQLite refused one of the steps in the table-rebuild dance (create new table, copy\n"+
						"rows, drop old, rename, recreate indexes): %s",
					err,
				),
				Impact: "The audit-events table rebuild stopped midway, so the entire version 2 → 3 upgrade\n" +
					"was rolled back. The audit database stays at version 2.",
				Fix: "1. Confirm the audit database file is writable:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. The new table requires every row to have an agent. If the previous step missed\n" +
					"   any rows, the new table's not-null check on the agent column will reject the copy.\n" +
					"   Look for orphan events:\n" +
					"     sqlite3 <path-to-audit.db> 'SELECT id, role FROM audit_events WHERE agent_id IS NULL'\n" +
					"3. Re-run the migration once verified:\n" +
					"     pasture migrate",
			}
		}
	}
	return nil
}

// isDuplicateColumnError reports whether err matches the modernc.org/sqlite
// "duplicate column name" error. Substring match — the driver does not
// expose a typed sentinel for SQLITE_ERROR-class messages.
func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite: "SQL logic error: duplicate column name: agent_id (1)"
	return strings.Contains(err.Error(), "duplicate column name")
}

// auditEventsTableExists reports whether the audit_events table is present
// in the database. The transaction-scoped probe avoids opening a second
// connection that would deadlock against the migrator's IMMEDIATE write
// lock.
//
// Used by migrateV3Backfill as a bail-out: if the table is missing (a
// test that invoked Migrate against a fresh file without ensureSchema, or
// a future caller that explicitly cleared audit data) we skip the
// backfill rather than raise an "ALTER TABLE on missing table" error.
func auditEventsTableExists(tx *sql.Tx) (bool, error) {
	var name string
	err := tx.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_events'`,
	).Scan(&name)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't check whether the audit-events table exists during the version 2 → 3 upgrade.",
			Why: fmt.Sprintf(
				"SQLite refused our query against its internal table catalog: %s",
				err,
			),
			Impact: "We can't safely tell whether there are events to upgrade, so the migration was\n" +
				"abandoned to avoid corrupting the database. The database itself is unchanged.",
			Fix: "1. Check the audit database file isn't corrupted:\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-run the migration once the file is healthy:\n" +
				"     pasture migrate",
		}
	}
	return true, nil
}

// ensureProvenanceAgentTables idempotently creates the agent_kinds, agents,
// and agents_software tables (plus the agent_kinds seed rows) that the
// v3 backfill INSERTs into.
//
// # Why this lives here, not in Provenance
//
// PROPOSAL-2 C4 binds the Provenance Go library to "unmodified" — no PRs
// against github.com/dayvidpham/provenance for this work. The escape hatch
// the proposal grants (§7.10.2 paragraph 2) is raw-SQL access to the
// agents/agents_software tables on the shared SQLite file using the same
// shape Provenance uses internally. This function is the materialisation
// of that escape hatch's pre-requisite: if the audit migrator runs before
// any Provenance code has touched the file (e.g., audit-only unit tests,
// or a future `pasture migrate` command that operates without instantiating
// the full Provenance tracker), the tables must still exist for our
// INSERTs to succeed.
//
// The DDL is byte-for-byte from provenance/internal/sqlite/db.go (lines
// 122, 137-140, 151-156, 235). If Provenance ever evolves its agent
// schema (adds a column, changes a type), this function MUST be updated to
// match — otherwise the audit migrator and Provenance writer would race
// to define incompatible CREATE TABLE statements (last-writer-wins, with
// hard-to-debug downstream errors). The CREATE TABLE IF NOT EXISTS
// semantics protect us from duplicate-table errors when Provenance opens
// first; they do NOT protect against schema-shape divergence.
//
// Called from migrateV3Backfill as Step 0 (before the audit-side work).
func ensureProvenanceAgentTables(tx *sql.Tx) error {
	statements := []struct {
		what string
		sql  string
	}{
		{
			what: "create agent_kinds (Provenance shape)",
			sql:  `CREATE TABLE IF NOT EXISTS agent_kinds (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE) STRICT`,
		},
		{
			what: "seed agent_kinds with (0,human), (1,machine_learning), (2,software)",
			// INSERT OR IGNORE so re-running on a partially-seeded
			// Provenance file (e.g. an older Provenance version that
			// only seeded human + ml) is a no-op for the existing rows
			// and additive for the missing ones.
			sql: `INSERT OR IGNORE INTO agent_kinds (id, name) VALUES (0,'human'),(1,'machine_learning'),(2,'software')`,
		},
		{
			what: "create agents (Provenance shape)",
			sql: `CREATE TABLE IF NOT EXISTS agents (
				id      TEXT PRIMARY KEY,
				kind_id INTEGER NOT NULL REFERENCES agent_kinds(id)
			) STRICT`,
		},
		{
			what: "create agents_software (Provenance shape)",
			sql: `CREATE TABLE IF NOT EXISTS agents_software (
				agent_id TEXT PRIMARY KEY REFERENCES agents(id),
				name     TEXT NOT NULL,
				version  TEXT NOT NULL DEFAULT '',
				source   TEXT NOT NULL DEFAULT ''
			) STRICT, WITHOUT ROWID`,
		},
	}

	for _, s := range statements {
		if _, err := tx.Exec(s.sql); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't %s before starting the version 2 → 3 upgrade.",
					s.what,
				),
				Why: fmt.Sprintf(
					"SQLite refused our setup statement that ensures the agent registry tables exist: %s",
					err,
				),
				Impact: "The version 2 → 3 upgrade can't start because the agent registry tables aren't\n" +
					"available in the expected shape. The audit database stays at version 2.",
				Fix: "1. Confirm the audit database file is writable:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. If the agent registry tables already exist with a different shape (e.g. an older\n" +
					"   or newer Provenance library wrote them), pin pasture to a version compatible\n" +
					"   with the database file and try again.\n" +
					"3. Inspect the existing tables to compare shapes:\n" +
					"     sqlite3 <path-to-audit.db> '.schema agents'\n" +
					"     sqlite3 <path-to-audit.db> '.schema agents_software'\n" +
					"     sqlite3 <path-to-audit.db> '.schema agent_kinds'",
			}
		}
	}
	return nil
}

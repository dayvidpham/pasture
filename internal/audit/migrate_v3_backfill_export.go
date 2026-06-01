// Package audit — migrate_v3_backfill_export.go
//
// Test-only public entry point so the cmd/pasture-migrate-crash binary
// can run the v3 backfill body (steps 0–5: ensure Provenance tables,
// add agent_id column, distinct roles, find-or-create agents, backfill
// agent_id, table-rebuild) WITHOUT the audit_schema_meta version bump
// or tx.Commit. The crash binary uses this to stage the partial v3
// transaction state Scenario 11 needs to assert on.
//
// Why this isn't gated by build tags
//
// The cmd/pasture-migrate-crash binary is built by the standard `make
// build` target (HANDOFF §7) so it ships in dist/ alongside the
// production binaries. Build-tag gating would require a parallel build
// path; given the binary is small, useful only in test contexts, and
// imports from internal/audit (so accidental production use in any
// surface besides cmd/pasture-migrate-crash is not a concern), we
// expose this function unconditionally.
//
// Workers extending the v3 backfill MUST keep the body of
// migrateV3Backfill in sync with what this function exposes — it is a
// thin re-export of the same private helper.
package audit

import "database/sql"

// MigrateV3BackfillForCrashTest runs the full v3 backfill body
// (migrateV3Backfill) against the supplied transaction WITHOUT calling
// writeVersion(3) and WITHOUT committing the transaction.
//
// The caller is responsible for either:
//
//   - Calling writeVersion(3, ...) and tx.Commit() — production parity, or
//   - Calling tx.Commit() / tx.Rollback() / os.Exit() to inject a crash
//     between the body's last statement and the commit (Scenario 11
//     test path).
//
// nowUnixNano is reserved for future signature parity with
// migrateV3Backfill; this entry point ignores it.
//
// This is the ONLY exported function in the audit package that bypasses
// the migration framework's transaction lifecycle. Production callers
// MUST use audit.Migrate or NewSqliteAuditTrail; this entry point is
// reserved for the crash-test binary.
func MigrateV3BackfillForCrashTest(tx *sql.Tx) error {
	return migrateV3Backfill(tx, 0)
}

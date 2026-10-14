package audit_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
)

// cutoverTarget is the last audit schema version the offline identity cutover's
// catch-up phase reaches before the identity-format ceiling step. The ceiling
// step refuses a populated store that does not carry the slash identity marker,
// so a legacy store's plain migration stops here; the cutover transaction applies
// the remaining step atomically with the marker and the receipt.
const cutoverTarget = audit.MaxKnownSchemaVersion - 1

// migratePopulatedToCutoverTarget migrates a store whose identities populate the
// Provenance tables through the deferred ceiling, the way the cutover's catch-up
// phase does. It is the migration the migrator tests exercise now that the
// identity-format ceiling step requires the slash marker on a populated store.
func migratePopulatedToCutoverTarget(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := audit.MigrateContextToCeiling(context.Background(), db, cutoverTarget); err != nil {
		t.Fatalf("audit.MigrateContextToCeiling: %v", err)
	}
}

// openCutoverTargetStore opens the audit trail with the deferred ceiling, so a
// populated legacy store stops at cutoverTarget.
func openCutoverTargetStore(t *testing.T, dbPath string) *audit.SqliteAuditTrail {
	t.Helper()
	trail, err := audit.NewSqliteAuditTrailWithOptions(dbPath, audit.WithDeferredCeiling(cutoverTarget))
	if err != nil {
		t.Fatalf("audit.NewSqliteAuditTrailWithOptions: %v", err)
	}
	return trail
}

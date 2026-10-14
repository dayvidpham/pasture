package audit_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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

// TestTheDeferredCeilingStopsAPopulatedStoreAtTheCutoverTarget pins the first
// half of the ceiling deferral: a populated, unmarked store's catch-up stops at
// the cutover target instead of being refused at the ceiling, because the
// identity-format ceiling step is deferred out of the ordinary migrator. The
// next test shows the same step applied directly IS refused, which is what makes
// the deferral load-bearing rather than cosmetic.
func TestTheDeferredCeilingStopsAPopulatedStoreAtTheCutoverTarget(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "deferred.db")
	seedLegacyV1DB(t, dbPath)

	trail, err := audit.NewSqliteAuditTrailWithOptions(dbPath, audit.WithDeferredCeiling(cutoverTarget))
	require.NoError(t, err)
	require.NoError(t, trail.Close())

	db := openDB(t, dbPath)
	defer db.Close()
	var version int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version))
	require.Equal(t, cutoverTarget, version, "the deferred catch-up must stop at the cutover target")
}

// TestTheCeilingStepRefusesAPopulatedUnmarkedStore pins the second half of the
// ceiling deferral: applying the identity-format ceiling step without the slash
// marker is refused on a populated store, so no path can raise the floor without
// the marker.
func TestTheCeilingStepRefusesAPopulatedUnmarkedStore(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "unmarked.db")
	seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	defer db.Close()
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, cutoverTarget))

	err := audit.MigrateContextToCeiling(context.Background(), db, audit.MaxKnownSchemaVersion)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be raised to the identity-format schema ceiling")
}

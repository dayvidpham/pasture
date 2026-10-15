package audit_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/provenance"
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

// markStoreAsSlash installs the dependency's slash identity-format marker, the
// evidence the identity-format ceiling step requires on a populated store.
func markStoreAsSlash(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS provenance_id_format (singleton INTEGER PRIMARY KEY, format TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT OR REPLACE INTO provenance_id_format(singleton, format) VALUES(1, ?)`, string(provenance.IDFormatSlash))
	require.NoError(t, err)
}

// requireRecordedVersion asserts the store's recorded audit schema version.
func requireRecordedVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	require.NoError(t, db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&got))
	require.Equal(t, want, got, "recorded audit schema version")
}

// populatedStoreAtCeiling builds a populated store recorded at the given
// version by applying the forward steps directly, then marking it slash so the
// identity-format ceiling step is admitted.
func populatedStoreAtCeiling(t *testing.T, version int) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "at-ceiling.db")
	seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	markStoreAsSlash(t, db)
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, version))
	requireRecordedVersion(t, db, version)
	return db, dbPath
}

// TestTheDeferredCeilingNoOpsAStoreAlreadyPastTheTarget pins the deferral rule a
// deferred-ceiling open depends on: a store already recorded in
// (cutoverTarget, MaxKnown] is a NO-OP success. A deferral implemented by
// comparing the recorded version against the target instead of the constant
// would refuse such a store as "written by a newer pasture", so the deferral
// could not finish a store it was built to finish.
func TestTheDeferredCeilingNoOpsAStoreAlreadyPastTheTarget(t *testing.T) {
	t.Parallel()
	db, dbPath := populatedStoreAtCeiling(t, audit.MaxKnownSchemaVersion)

	// The recorded version is in (cutoverTarget, MaxKnown]: the deferred-ceiling
	// migrator must return success and leave the version untouched.
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, cutoverTarget))
	requireRecordedVersion(t, db, audit.MaxKnownSchemaVersion)

	// The production deferred-ceiling open (the cutover catch-up path) must also
	// accept the store rather than reporting a newer schema.
	trail, err := audit.NewSqliteAuditTrailWithOptions(dbPath, audit.WithDeferredCeiling(cutoverTarget))
	require.NoError(t, err)
	require.NoError(t, trail.Close())
	requireRecordedVersion(t, db, audit.MaxKnownSchemaVersion)
}

// TestTheDeferredWorkIsVersionOnly pins that the work deferred past the cutover
// target is version-only TODAY: applying the deferred ceiling to a store at the
// target changes only the recorded schema version, never another table's schema
// or content. A future real migration step landing in (target, MaxKnown] would
// change something else and turn this pin red, so a real step cannot be silently
// stamped as the deferred work instead of being applied.
func TestTheDeferredWorkIsVersionOnly(t *testing.T) {
	t.Parallel()
	db, _ := populatedStoreAtCeiling(t, cutoverTarget)

	schemaBefore := schemaDigest(t, db)
	contentBefore := tableDigests(t, db)

	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, audit.MaxKnownSchemaVersion))
	requireRecordedVersion(t, db, audit.MaxKnownSchemaVersion)

	require.Equal(t, schemaBefore, schemaDigest(t, db), "the deferred ceiling must add no schema object")
	contentAfter := tableDigests(t, db)
	require.Len(t, contentAfter, len(contentBefore), "the deferred ceiling must add no table")
	for name, before := range contentBefore {
		if name == "audit_schema_meta" {
			continue
		}
		require.Equal(t, before, contentAfter[name], "the deferred ceiling must not change table %q", name)
	}
	require.NotEqual(t, contentBefore["audit_schema_meta"], contentAfter["audit_schema_meta"], "the deferred ceiling records the new version")
}

// schemaDigest hashes every schema object's type, name and DDL, so a deferred
// step that creates, drops or alters any object changes the digest.
func schemaDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	require.NoError(t, err)
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var kind, name, ddl string
		require.NoError(t, rows.Scan(&kind, &name, &ddl))
		fmt.Fprintf(h, "%s\x1f%s\x1f%s\x1e", kind, name, ddl)
	}
	require.NoError(t, rows.Err())
	return hex.EncodeToString(h.Sum(nil))
}

// tableDigests hashes the content of every user table, so a deferred step that
// writes data anywhere other than audit_schema_meta changes a digest.
func tableDigests(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	require.NoError(t, err)
	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = tableDigest(t, db, name)
	}
	return out
}

func tableDigest(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM "` + name + `"`)
	require.NoError(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	require.NoError(t, err)
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	h := sha256.New()
	for rows.Next() {
		require.NoError(t, rows.Scan(ptrs...))
		for _, v := range values {
			switch typed := v.(type) {
			case nil:
				_, _ = io.WriteString(h, "\x00")
			case []byte:
				_, _ = h.Write(typed)
			case string:
				_, _ = io.WriteString(h, typed)
			default:
				fmt.Fprintf(h, "%v", typed)
			}
			_, _ = h.Write([]byte{0x1f})
		}
		_, _ = h.Write([]byte{0x1e})
	}
	require.NoError(t, rows.Err())
	return hex.EncodeToString(h.Sum(nil))
}

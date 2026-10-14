package handlers_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// buildRetiredPopulatedStoreWithClaim builds a current production store, strips
// its fence evidence so it presents as an unmarked, populated store, and
// installs the authentic opaque claimant row ("pasture-system" over ordinal range
// [0,1023]). It is the store shape the operator's live database has when the
// offline cutover meets it.
func buildRetiredPopulatedStoreWithClaim(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM provenance_id_format`)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE IF EXISTS pasture_id_cutover`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM audit_schema_meta WHERE version >= ?`, audit.MaxKnownSchemaVersion)
	require.NoError(t, err)
	minBody := make([]byte, 16)
	maxBody := make([]byte, 16)
	maxBody[14], maxBody[15] = 0x03, 0xff // 1023, big-endian
	_, err = db.Exec(
		`INSERT INTO actor_namespace_claims (namespace, claimant_id, range_min, range_max, codec) VALUES (?,?,?,?,?)`,
		"pasture-system", "pasture-system", minBody, maxBody, "provenance.ordinal.v1")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	return path
}

// TestMigrateIDsDryRunReportsOpaqueClaimantPreserved pins the user-visible dry
// run: the opaque claimant label is named as PRESERVED (kept byte-exact, never
// converted), so the operator's plan and the real conversion agree.
func TestMigrateIDsDryRunReportsOpaqueClaimantPreserved(t *testing.T) {
	t.Parallel()
	path := buildRetiredPopulatedStoreWithClaim(t)

	var out bytes.Buffer
	code, err := handlers.MigrateIDs(&out, handlers.MigrateIDsInput{DBPath: path, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "preserved (opaque labels kept byte-exact, never converted):")
	require.Contains(t, out.String(), "actor_namespace_claims.claimant_id")
}

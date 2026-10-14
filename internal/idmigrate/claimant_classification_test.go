package idmigrate_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// insertAuthenticClaim writes the exact claimant class a real writer stores:
// the bare namespace "pasture-system" over the ordinal range [0,1023] under the
// ordinal codec. actor_namespace_claims.claimant_id is an opaque, equality-only
// label — NOT a namespaced-UUID identity — so it must survive conversion
// byte-exact and never be parsed.
func insertAuthenticClaim(t *testing.T, db *sql.DB) {
	t.Helper()
	min := make([]byte, 16)
	max := make([]byte, 16)
	max[14], max[15] = 0x03, 0xff // 1023, big-endian
	_, err := db.Exec(
		`INSERT INTO actor_namespace_claims (namespace, claimant_id, range_min, range_max, codec) VALUES (?,?,?,?,?)`,
		"pasture-system", "pasture-system", min, max, "provenance.ordinal.v1")
	require.NoError(t, err)
}

// claimantLabel reads the preserved claimant label back.
func claimantLabel(t *testing.T, db *sql.DB) string {
	t.Helper()
	var label string
	require.NoError(t, db.QueryRow(
		`SELECT claimant_id FROM actor_namespace_claims WHERE namespace='pasture-system'`).Scan(&label))
	return label
}

// makeRetiredPopulatedStore builds a current production store, then strips its
// fence evidence (the slash marker, the cutover receipt and the deferred
// ceiling step) so it presents as an unmarked, populated store, and installs the
// authentic claim row: the shape the live operator store has when the offline
// cutover meets it.
func makeRetiredPopulatedStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`DELETE FROM provenance_id_format`)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE IF EXISTS pasture_id_cutover`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM audit_schema_meta WHERE version >= ?`, audit.MaxKnownSchemaVersion)
	require.NoError(t, err)
	insertAuthenticClaim(t, db)
	return path, db
}

// TestDryRunReportsOpaqueClaimantAsPreservedNotConverted pins that the dry-run
// plan and the real conversion agree on the R9 classification: the opaque
// claimant label is named as PRESERVED, and it is NOT among the columns the dry
// run reports as convertible.
func TestDryRunReportsOpaqueClaimantAsPreservedNotConverted(t *testing.T) {
	t.Parallel()
	path, db := makeRetiredPopulatedStore(t)
	preview, err := idmigrate.DryRun(context.Background(), db, path)
	require.NoError(t, err)
	require.Contains(t, preview.Preserved, "actor_namespace_claims.claimant_id",
		"the dry run must name the opaque claimant column as preserved")
	require.NotContains(t, preview.Columns, "actor_namespace_claims.claimant_id",
		"the dry run must not report the opaque claimant column as convertible")
}

// TestConversionPreservesOpaqueClaimantLabel is the Pasture-side must-pass
// regression for the R9 classification. Before the dependency fix,
// ConvertIDsInTx's preflight parsed claimant_id as an owned identity and refused
// this store (a bare namespace cannot be an identity). Now the column is OUT of
// the owned set: the conversion completes, publishes the receipt, and the claim
// row is preserved byte-exact.
func TestConversionPreservesOpaqueClaimantLabel(t *testing.T) {
	t.Parallel()
	path, db := makeRetiredPopulatedStore(t)
	before := claimantLabel(t, db)
	require.Equal(t, "pasture-system", before)

	report, err := idmigrate.Convert(context.Background(), db, idmigrate.ConvertOptions{StorePath: path})
	require.NoError(t, err, "a store carrying the real pasture-system claimant label must convert without refusal")
	require.True(t, report.ReceiptWritten)

	after := claimantLabel(t, db)
	require.Equal(t, before, after, "the opaque claimant label must be preserved byte-exact")
}

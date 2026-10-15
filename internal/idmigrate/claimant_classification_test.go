package idmigrate_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// makeRetiredPopulatedStore returns the shared operator-store shape: a current
// production store with its fence evidence stripped and the authentic opaque
// claimant row installed (see testutil.RetiredPopulatedStoreWithClaim). Both the
// idmigrate and handlers tests use the one builder, so their copies cannot drift.
func makeRetiredPopulatedStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	return testutil.RetiredPopulatedStoreWithClaim(t)
}

// TestDryRunReportsOpaqueClaimantAsPreservedNotConverted pins that the dry-run
// plan and the real conversion agree on the opaque-claimant classification: the
// opaque claimant label is named as PRESERVED, and it is NOT among the columns
// the dry run reports as convertible.
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
// regression for the opaque-claimant classification. Before the dependency fix,
// ConvertIDsInTx's preflight parsed claimant_id as an owned identity and refused
// this store (a bare namespace cannot be an identity). Now the column is OUT of
// the owned set: the conversion completes, publishes the receipt, and the claim
// row is preserved byte-exact.
func TestConversionPreservesOpaqueClaimantLabel(t *testing.T) {
	t.Parallel()
	path, db := makeRetiredPopulatedStore(t)
	before := readClaimRow(t, db)
	require.Equal(t, "pasture-system", before.claimantID)

	report, err := idmigrate.Convert(context.Background(), db, idmigrate.ConvertOptions{StorePath: path})
	require.NoError(t, err, "a store carrying the real pasture-system claimant label must convert without refusal")
	require.True(t, report.ReceiptWritten)

	after := readClaimRow(t, db)
	require.Equal(t, before, after, "the opaque claimant row must be preserved byte-exact")
}

// claimRow is every column of the authentic claim row, so a comparison proves
// the whole row survived rather than one field.
type claimRow struct {
	namespace  string
	claimantID string
	rangeMin   []byte
	rangeMax   []byte
	codec      string
}

// readClaimRow reads the authentic claim row back.
func readClaimRow(t *testing.T, db *sql.DB) claimRow {
	t.Helper()
	var row claimRow
	require.NoError(t, db.QueryRow(
		`SELECT namespace, claimant_id, range_min, range_max, codec FROM actor_namespace_claims WHERE namespace='pasture-system'`).
		Scan(&row.namespace, &row.claimantID, &row.rangeMin, &row.rangeMax, &row.codec))
	return row
}

// TestConvertedClaimStoreReopensAndActivatesWithoutDrift is the end-to-end
// acceptance for the opaque claimant: after the offline conversion the store
// reopens through the real production opener and activation stays idempotent.
// The failure this pins is the one the classification exists to prevent — a
// converted claimant label that no longer matches what the writer registers, so
// every open after conversion refuses with a namespace-drift error.
func TestConvertedClaimStoreReopensAndActivatesWithoutDrift(t *testing.T) {
	t.Parallel()
	path, db := makeRetiredPopulatedStore(t)
	before := readClaimRow(t, db)

	_, err := idmigrate.Convert(context.Background(), db, idmigrate.ConvertOptions{StorePath: path})
	require.NoError(t, err)
	require.NoError(t, db.Close(), "the conversion handle must close before the reopen proof")

	// The real production opener runs activation (RegisterFixedSoftwareAgent,
	// which preflights Equal against the stored claim). A drift refusal would
	// surface here.
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err, "a converted store must reopen and activate without a namespace-drift refusal")
	require.NoError(t, tracker.Close())

	reopened, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	require.Equal(t, before, readClaimRow(t, reopened), "every claim column must be byte-identical after reopen")
}

package handlers_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
)

// TestMigrateIDsDryRunReportsOpaqueClaimantPreserved pins the user-visible dry
// run: the opaque claimant label is named as PRESERVED (kept byte-exact, never
// converted), so the operator's plan and the real conversion agree. The store
// comes from the shared builder, so the idmigrate and handlers copies cannot
// drift.
func TestMigrateIDsDryRunReportsOpaqueClaimantPreserved(t *testing.T) {
	t.Parallel()
	path, db := testutil.RetiredPopulatedStoreWithClaim(t)
	require.NoError(t, db.Close(), "the dry run opens its own handle, so release the builder's")

	var out bytes.Buffer
	code, err := handlers.MigrateIDs(&out, handlers.MigrateIDsInput{DBPath: path, DryRun: true}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "preserved (opaque labels kept byte-exact, never converted):")
	require.Contains(t, out.String(), "actor_namespace_claims.claimant_id")
}

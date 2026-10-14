package idmigrate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPastureClassificationMirrorsKeepTheOpaqueClaimantOut pins the Pasture-side
// mirrors of the dependency's identity classification against the R9 amendment.
// actor_namespace_claims.claimant_id is an opaque, equality-only label: it must
// be declared in the preserved mirror and absent from both the convertible-column
// mirror and the payload identity-key mirror. This is the defect class that
// produced the mis-classification — a hand-maintained mirror drifting from the
// dependency set — pinned so it cannot drift back silently.
func TestPastureClassificationMirrorsKeepTheOpaqueClaimantOut(t *testing.T) {
	t.Parallel()

	for _, col := range previewDependencyColumns {
		require.False(t, col.table == "actor_namespace_claims" && col.column == "claimant_id",
			"the opaque claimant column must not be a convertible dependency column")
	}

	found := false
	for _, col := range preservedDependencyColumns {
		if col.table == "actor_namespace_claims" && col.column == "claimant_id" {
			found = true
		}
	}
	require.True(t, found, "the opaque claimant column must be declared preserved")

	_, ok := auditPayloadIdentityKeys["claimantid"]
	require.False(t, ok, "claimantId is an opaque label, not a payload identity key")
}

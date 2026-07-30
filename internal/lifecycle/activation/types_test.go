package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestClaudeActivationIsCompleteDisjointAndProgressive(t *testing.T) {
	t.Parallel()
	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	require.Len(t, entries, 30)
	seen := make(map[uint16]struct{}, len(entries))
	enabled := 0
	for _, entry := range entries {
		_, duplicate := seen[uint16(entry.Event)]
		require.False(t, duplicate)
		seen[uint16(entry.Event)] = struct{}{}
		switch entry.State {
		case activation.Enabled:
			enabled++
			require.Zero(t, entry.Reason)
		case activation.Withheld:
			require.Equal(t, activation.WithheldMissingFixture, entry.Reason)
		default:
			t.Fatalf("unexpected activation state %d", entry.State)
		}
	}
	require.Equal(t, 1, enabled)
	require.Equal(t, registration.EventSessionStart, entries[0].Event)
}

func TestEnabledRequiresIndependentEvidenceAndProductionProof(t *testing.T) {
	t.Parallel()
	_, err := activation.NewEnabled(registration.EventSessionStart, activation.FixtureEvidenceMissing, activation.ProductionProofPassing)
	require.ErrorContains(t, err, "authentic exact-version capture")
	_, err = activation.NewEnabled(registration.EventSessionStart, activation.FixtureEvidenceAuthentic, activation.ProductionProofMissing)
	require.ErrorContains(t, err, "production-path proof")
}

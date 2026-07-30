package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestClaudeActivationIsCompleteDisjointAndProgressive(t *testing.T) {
	t.Parallel()
	entries := activation.ClaudeCode2_1_210()
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
			if entry.Event == registration.EventSessionStart {
				require.Equal(t, activation.WithheldProductionProofMissing, entry.Reason)
			} else {
				require.Equal(t, activation.WithheldMissingFixture, entry.Reason)
			}
		default:
			t.Fatalf("unexpected activation state %d", entry.State)
		}
	}
	require.Zero(t, enabled)
	require.Equal(t, registration.EventSessionStart, entries[0].Event)
}

func TestEnabledRequiresIndependentEvidenceAndProductionProof(t *testing.T) {
	t.Parallel()
	_, err := activation.NewEnabled(registration.EventSessionStart, false, true)
	require.ErrorContains(t, err, "authentic exact-version capture")
	_, err = activation.NewEnabled(registration.EventSessionStart, true, false)
	require.ErrorContains(t, err, "production-path proof")
}

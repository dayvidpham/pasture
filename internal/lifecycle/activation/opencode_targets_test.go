package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestOpenCodeActivationRequiresBothExactProofs(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode1_18_29()
	require.NoError(t, err)
	require.Len(t, entries, 47)

	enabled := make([]model.ContractEventKind, 0, 2)
	for _, entry := range entries {
		require.True(t, entry.IsValid())
		if entry.State == activation.Enabled {
			enabled = append(enabled, entry.Event)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
			continue
		}
		require.Equal(t, activation.WithheldOutsideTargetSet, entry.Reason)
		require.Zero(t, entry.CaptureProof)
		require.Zero(t, entry.ProductionProof)
	}
	require.Equal(t, []model.ContractEventKind{
		registration.EventOpenCodeSessionCreated,
		registration.EventOpenCodeToolExecuteBefore,
	}, enabled)
}

func TestOpenCodeCatalogMembershipDoesNotActivateAnotherEvent(t *testing.T) {
	t.Parallel()
	entries, err := activation.OpenCode1_18_29()
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Event == registration.EventOpenCodeSessionUpdated {
			require.Equal(t, activation.Withheld, entry.State)
			return
		}
	}
	t.Fatal("generated source catalog omitted session.updated")
}

func TestOpenCodeActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.OpenCode1_18_29TargetEvents()
	require.Len(t, targets, 2)
	targets[0] = 0
	require.Equal(t, registration.EventOpenCodeSessionCreated, activation.OpenCode1_18_29TargetEvents()[0])
}

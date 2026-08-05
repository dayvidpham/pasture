package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestClaudeActivationIsCompleteAndExactlyPartitioned(t *testing.T) {
	t.Parallel()
	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	require.Len(t, entries, 30)
	targets := make(map[model.ContractEventKind]struct{})
	for _, event := range activation.ClaudeCode2_1_210TargetEvents() {
		targets[event] = struct{}{}
	}
	require.Len(t, targets, 10)
	enabledTargets := map[model.ContractEventKind]struct{}{
		registration.EventSessionStart: {}, registration.EventSessionEnd: {},
		registration.EventPreToolUse: {}, registration.EventPostToolUse: {},
		registration.EventPostToolUseFailure: {}, registration.EventPostToolBatch: {},
		registration.EventPreCompact: {}, registration.EventPostCompact: {},
	}
	seen := make(map[model.ContractEventKind]struct{}, len(entries))
	enabled := 0
	missingCorrelation := 0
	outsideTarget := 0
	for _, entry := range entries {
		_, duplicate := seen[entry.Event]
		require.False(t, duplicate)
		seen[entry.Event] = struct{}{}
		require.True(t, entry.IsValid())
		switch entry.State {
		case activation.Enabled:
			enabled++
			require.Zero(t, entry.Reason)
			require.Contains(t, enabledTargets, entry.Event)
			captureEvent, captureOK := entry.CaptureProof.Event()
			productionEvent, productionOK := entry.ProductionProof.Event()
			require.True(t, captureOK)
			require.True(t, productionOK)
			require.Equal(t, entry.Event, captureEvent)
			require.Equal(t, entry.Event, productionEvent)
			require.NotEmpty(t, entry.CaptureProof.Name())
			require.NotEmpty(t, entry.ProductionProof.Name())
		case activation.Withheld:
			require.Zero(t, entry.CaptureProof)
			require.Zero(t, entry.ProductionProof)
			if _, target := targets[entry.Event]; target {
				missingCorrelation++
				require.Contains(t, []model.ContractEventKind{registration.EventElicitation, registration.EventElicitationResult}, entry.Event)
				require.Equal(t, activation.WithheldMissingRequestCorrelation, entry.Reason)
			} else {
				outsideTarget++
				require.Equal(t, activation.WithheldOutsideTargetSet, entry.Reason)
			}
		default:
			t.Fatalf("unexpected activation state %d", entry.State)
		}
	}
	require.Equal(t, 8, enabled)
	require.Equal(t, 2, missingCorrelation)
	require.Equal(t, 20, outsideTarget)
	require.Equal(t, registration.EventSessionStart, entries[0].Event)
}

func TestActivationTargetEventsReturnsDefensiveCopyAndManifestIsFresh(t *testing.T) {
	t.Parallel()
	targets := activation.ClaudeCode2_1_210TargetEvents()
	require.Len(t, targets, 10)
	targets[0] = model.ContractEventKind(999)
	freshTargets := activation.ClaudeCode2_1_210TargetEvents()
	require.Equal(t, registration.EventSessionStart, freshTargets[0])

	entries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	entries[0].State = activation.Withheld
	freshEntries, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	require.Equal(t, activation.Enabled, freshEntries[0].State)
}

func TestEnabledRequiresEventBoundProofs(t *testing.T) {
	t.Parallel()
	_, err := activation.NewEnabled(registration.EventSessionStart, 0, activation.ProductionProofSessionStart)
	require.ErrorContains(t, err, "event-bound authentic capture proof")
	_, err = activation.NewEnabled(registration.EventSessionEnd, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.ErrorContains(t, err, "bound to event")
	_, err = activation.NewEnabled(registration.EventSessionStart, activation.CaptureProofSessionStart, 0)
	require.ErrorContains(t, err, "event-bound production proof")
	_, err = activation.NewEnabled(registration.EventSessionStart, activation.CaptureProofSessionStart, activation.ProductionProofSessionStart)
	require.NoError(t, err)
}

func TestActivationConstructorsRejectReservedZeroAndInvalidCombinations(t *testing.T) {
	t.Parallel()
	_, err := activation.NewWithheld(0, activation.WithheldMissingFixture)
	require.Error(t, err)
	_, err = activation.NewWithheld(registration.EventSessionStart, 0)
	require.Error(t, err)
	require.False(t, (activation.Entry{}).IsValid())
	require.False(t, (activation.CaptureProof(99)).IsValid())
	require.False(t, (activation.ProductionProof(99)).IsValid())
	require.Equal(t, "missing-request-correlation", activation.WithheldMissingRequestCorrelation.String())
}

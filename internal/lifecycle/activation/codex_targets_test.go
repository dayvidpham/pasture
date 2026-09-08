package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Every registered event in this capture batch has its own event-bound proofs.
func TestCodexActivationEnablesClearedEvents(t *testing.T) {
	t.Parallel()
	entries, err := activation.Codex0_153_0()
	require.NoError(t, err)
	require.Len(t, entries, len(registration.Codex0_153_0().Entries()), "the manifest must be exhaustive over the generated Codex catalog")

	enabled := make([]model.ContractEventKind, 0, len(entries))
	for _, entry := range entries {
		require.True(t, entry.IsValid(), "every derived activation entry must be valid")
		require.Equal(t, activation.Enabled, entry.State, "registered event %d needs its cleared production proof", entry.Event)
		enabled = append(enabled, entry.Event)
		require.Equal(t, activation.WithheldReasonInvalid, entry.Reason, "an enabled entry carries no withholding reason")
		require.NotEmpty(t, entry.CaptureProof.Name(), "an enabled entry must name its authentic capture proof")
		require.NotEmpty(t, entry.ProductionProof.Name(), "an enabled entry must name its production proof")
		captureEvent, ok := entry.CaptureProof.Event()
		require.True(t, ok)
		require.Equal(t, entry.Event, captureEvent, "the capture proof must be bound to this exact event")
		productionEvent, ok := entry.ProductionProof.Event()
		require.True(t, ok)
		require.Equal(t, entry.Event, productionEvent, "the production proof must be bound to this exact event")
	}
	var registered []model.ContractEventKind
	for _, event := range registration.Codex0_153_0().Entries() {
		registered = append(registered, event.Kind)
	}
	require.Equal(t, registered, enabled)
}

func TestCodexActivationRejectsCrossEventProofs(t *testing.T) {
	t.Parallel()
	entries, err := activation.Codex0_153_0()
	require.NoError(t, err)
	require.Greater(t, len(entries), 1)
	for i, entry := range entries {
		other := entries[(i+1)%len(entries)]
		_, err := activation.NewEnabled(entry.Event, other.CaptureProof, entry.ProductionProof)
		require.ErrorContains(t, err, "is bound to event")
		_, err = activation.NewEnabled(entry.Event, entry.CaptureProof, other.ProductionProof)
		require.ErrorContains(t, err, "is bound to event")
	}
}

// TestCodexActivationTargetEventsAreDefensive proves the exported target-set
// accessor returns an independent copy that cannot mutate the static table.
func TestCodexActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.Codex0_153_0TargetEvents()
	require.Len(t, targets, len(registration.Codex0_153_0().Entries()))
	targets[0] = 0
	require.Equal(t, registration.EventCodexSessionStart, activation.Codex0_153_0TargetEvents()[0], "the target table must be immune to caller mutation")
}

// TestCodexEvidenceLeavesOpenCodeAndClaudeActivationUnchanged is the M3-P4
// isolation obligation at the derivation layer: deriving the Codex catalog does
// not change the accepted OpenCode or Claude enabled sets, and the Codex catalog
// is disjoint from both provider event spaces, so Codex evidence can never
// enable an OpenCode or Claude entry.
func TestCodexEvidenceLeavesOpenCodeAndClaudeActivationUnchanged(t *testing.T) {
	t.Parallel()

	openCode, err := activation.OpenCode1_18_29()
	require.NoError(t, err)
	require.Equal(t, []model.ContractEventKind{
		registration.EventOpenCodeSessionCreated,
		registration.EventOpenCodeToolExecuteBefore,
	}, enabledEvents(openCode), "the accepted OpenCode enabled set must be unchanged")

	claude, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	require.Equal(t, []model.ContractEventKind{
		registration.EventSessionStart,
		registration.EventSessionEnd,
		registration.EventPreToolUse,
		registration.EventPostToolUse,
		registration.EventPostToolUseFailure,
		registration.EventPostToolBatch,
		registration.EventFileChanged,
		registration.EventPreCompact,
		registration.EventPostCompact,
	}, enabledEvents(claude), "the accepted Claude enabled set must be unchanged")

	codex, err := activation.Codex0_153_0()
	require.NoError(t, err)
	codexEvents := make(map[model.ContractEventKind]struct{}, len(codex))
	for _, entry := range codex {
		codexEvents[entry.Event] = struct{}{}
	}
	for _, foreign := range append(append([]activation.Entry{}, openCode...), claude...) {
		_, collides := codexEvents[foreign.Event]
		require.False(t, collides, "the Codex catalog must be disjoint from OpenCode and Claude event kinds")
	}
}

func enabledEvents(entries []activation.Entry) []model.ContractEventKind {
	out := make([]model.ContractEventKind, 0)
	for _, entry := range entries {
		if entry.State == activation.Enabled {
			out = append(out, entry.Event)
		}
	}
	return out
}

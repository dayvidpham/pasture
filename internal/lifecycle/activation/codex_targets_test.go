package activation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// TestCodexActivationEnablesExactlyTheTwoProvenEvents proves the derived Codex
// 0.146.0 activation manifest is exhaustive over the generated catalog and
// enables exactly the two authentically-proven events (SessionStart,
// PreToolUse), each with both event-bound proofs, while every other generated
// Codex event is Withheld (outside-target-set) with zero proofs. This is the
// M3-P4 derivation obligation for the Codex catalog.
func TestCodexActivationEnablesExactlyTheTwoProvenEvents(t *testing.T) {
	t.Parallel()
	entries, err := activation.Codex0_146_0()
	require.NoError(t, err)
	require.Len(t, entries, 10, "the manifest must be exhaustive over the generated Codex 0.146.0 catalog")

	enabled := make([]model.ContractEventKind, 0, 2)
	for _, entry := range entries {
		require.True(t, entry.IsValid(), "every derived activation entry must be valid")
		if entry.State == activation.Enabled {
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
			continue
		}
		require.Equal(t, activation.Withheld, entry.State)
		require.Equal(t, activation.WithheldOutsideTargetSet, entry.Reason, "a non-target Codex event is withheld outside-target-set")
		require.Zero(t, entry.CaptureProof, "a withheld entry carries no capture proof")
		require.Zero(t, entry.ProductionProof, "a withheld entry carries no production proof")
	}
	require.Equal(t, []model.ContractEventKind{
		registration.EventCodexSessionStart,
		registration.EventCodexPreToolUse,
	}, enabled, "exactly the two authentically-proven Codex events are enabled")
}

// TestCodexActivationWithholdsEveryNonTargetGeneratedEvent pins each of the
// eight non-target generated Codex events to Withheld, guarding against a future
// catalog change silently activating an unproven event.
func TestCodexActivationWithholdsEveryNonTargetGeneratedEvent(t *testing.T) {
	t.Parallel()
	entries, err := activation.Codex0_146_0()
	require.NoError(t, err)
	stateByEvent := make(map[model.ContractEventKind]activation.State, len(entries))
	for _, entry := range entries {
		stateByEvent[entry.Event] = entry.State
	}
	for _, event := range []model.ContractEventKind{
		registration.EventCodexUserPromptSubmit,
		registration.EventCodexPermissionRequest,
		registration.EventCodexPostToolUse,
		registration.EventCodexPreCompact,
		registration.EventCodexPostCompact,
		registration.EventCodexSubagentStart,
		registration.EventCodexSubagentStop,
		registration.EventCodexStop,
	} {
		require.Equal(t, activation.Withheld, stateByEvent[event], "non-target generated Codex event %d must be withheld", event)
	}
}

// TestCodexActivationTargetEventsAreDefensive proves the exported target-set
// accessor returns an independent copy that cannot mutate the static table.
func TestCodexActivationTargetEventsAreDefensive(t *testing.T) {
	t.Parallel()
	targets := activation.Codex0_146_0TargetEvents()
	require.Equal(t, []model.ContractEventKind{
		registration.EventCodexSessionStart,
		registration.EventCodexPreToolUse,
	}, targets)
	targets[0] = 0
	require.Equal(t, registration.EventCodexSessionStart, activation.Codex0_146_0TargetEvents()[0], "the target table must be immune to caller mutation")
}

// TestCodexEvidenceLeavesOpenCodeAndClaudeActivationUnchanged is the M3-P4
// isolation obligation at the derivation layer: deriving the Codex catalog does
// not change the accepted OpenCode or Claude enabled sets, and the Codex catalog
// is disjoint from both provider event spaces, so Codex evidence can never
// enable an OpenCode or Claude entry.
func TestCodexEvidenceLeavesOpenCodeAndClaudeActivationUnchanged(t *testing.T) {
	t.Parallel()

	openCode, err := activation.OpenCode1_18_10()
	require.NoError(t, err)
	require.Equal(t, []model.ContractEventKind{
		registration.EventOpenCodeSessionCreated,
		registration.EventOpenCodeToolExecuteBefore,
	}, enabledEvents(openCode), "the accepted OpenCode enabled set must be unchanged")

	claude, err := activation.ClaudeCode2_1_210()
	require.NoError(t, err)
	require.Equal(t, []model.ContractEventKind{
		registration.EventSessionStart,
		registration.EventSessionEnd,
		registration.EventPreToolUse,
		registration.EventPostToolUse,
		registration.EventPostToolUseFailure,
		registration.EventPostToolBatch,
		registration.EventPreCompact,
		registration.EventPostCompact,
	}, enabledEvents(claude), "the accepted Claude enabled set must be unchanged")

	codex, err := activation.Codex0_146_0()
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

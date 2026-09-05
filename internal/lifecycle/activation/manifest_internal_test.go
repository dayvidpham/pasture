package activation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// The shared manifest derivation is white-box tested here because its inputs
// are the unexported target tables: a harness worker edits one table row, and
// these cases say what each row shape derives to or why it is refused.

func TestDeriveManifestRefusesADecisionRowWithoutAClearance(t *testing.T) {
	t.Parallel()
	events := registration.ClaudeCode2_1_261().Entries()
	const clearance = "internal/lifecycle/ingress/claude/testdata/CLEARANCE.md"

	for _, reason := range []WithheldReason{
		WithheldNoReachableTrigger, WithheldUnclearablePayload,
		WithheldProviderHook, WithheldNotEmittedByHost, WithheldEmittedOutsideTransport,
	} {
		_, err := deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSetup, withheldReason: reason}})
		require.ErrorContains(t, err, `target event "Setup" withheld as "`+reason.String()+`" records a user decision and must name the committed CLEARANCE.md`)
		require.ErrorContains(t, err, "clearance is empty")

		_, err = deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSetup, withheldReason: reason, clearance: "tracker-item-ab12cd"}})
		require.ErrorContains(t, err, "bare task-tracker id")

		entries, err := deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSetup, withheldReason: reason, clearance: clearance}})
		require.NoError(t, err)
		setup := entryFor(t, entries, registration.EventSetup)
		require.Equal(t, Withheld, setup.State)
		require.Equal(t, reason, setup.Reason)
		require.Equal(t, clearance, setup.Clearance)
		require.True(t, setup.IsValid())
	}

	_, err := deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSetup, withheldReason: WithheldMissingRequestCorrelation, clearance: clearance}})
	require.ErrorContains(t, err, `names clearance "`+clearance+`", but that reason is not a user decision`)

	_, err = deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSessionStart, captureProof: CaptureProofSessionStart, productionProof: ProductionProofSessionStart, clearance: clearance}})
	require.ErrorContains(t, err, "is enabled by proofs and also names clearance")

	_, err = deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSessionStart, captureProof: CaptureProofSessionStart, productionProof: ProductionProofSessionStart, withheldReason: WithheldMissingFixture}})
	require.ErrorContains(t, err, "has both proofs and withholding reason")
}

func TestDeriveManifestDefaultsAnUnprovenTargetRowToMissingFixture(t *testing.T) {
	t.Parallel()
	events := registration.ClaudeCode2_1_261().Entries()
	entries, err := deriveManifest("test", events, []targetEventDeclaration{{event: registration.EventSetup}})
	require.NoError(t, err)
	setup := entryFor(t, entries, registration.EventSetup)
	require.Equal(t, Withheld, setup.State)
	require.Equal(t, WithheldMissingFixture, setup.Reason)
	require.Len(t, entries, len(events), "every generated event gets exactly one entry")
	session := entryFor(t, entries, registration.EventSessionStart)
	require.Equal(t, WithheldOutsideTargetSet, session.Reason, "an event outside the table is withheld outside the target set")
}

func entryFor(t *testing.T, entries []Entry, event model.ContractEventKind) Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Event == event {
			return entry
		}
	}
	t.Fatalf("no entry for event %d", event)
	return Entry{}
}

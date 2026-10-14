package tasks

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/idmigrate"
)

// TestRefuseIncompleteFenceOnStateIsTheSharedOutcomeMapping pins that the
// normal-open guard renders the ONE shared Outcome->refusal mapping
// (idmigrate.OutcomeRefusal with a populated store NOT convertible) for every
// verdict, so the guard cannot drift from the command frontends.
func TestRefuseIncompleteFenceOnStateIsTheSharedOutcomeMapping(t *testing.T) {
	t.Parallel()
	states := []idmigrate.CutoverState{
		{StorePath: "/tmp/pasture.db", Outcome: idmigrate.OutcomeNormal},
		{StorePath: "/tmp/pasture.db", Outcome: idmigrate.OutcomeCutover},
		{StorePath: "/tmp/pasture.db", Outcome: idmigrate.OutcomeRefusePopulated},
		{StorePath: "/tmp/pasture.db", Outcome: idmigrate.OutcomeRefuseUnbackedMark},
		{StorePath: "/tmp/pasture.db", RecordedVersion: 99, Outcome: idmigrate.OutcomeNewerSchema},
	}
	for _, state := range states {
		want := idmigrate.OutcomeRefusal(state, false)
		got := refuseIncompleteFenceOnState(state)
		if want == nil {
			require.NoError(t, got, "outcome %s", state.Outcome)
			continue
		}
		require.Equal(t, want.Error(), got.Error(), "outcome %s", state.Outcome)
	}
}

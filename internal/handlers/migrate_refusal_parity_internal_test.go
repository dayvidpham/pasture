package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/idmigrate"
)

// TestPlainMigrateRefusalIsTheSharedOutcomeMapping pins that the plain-migrate
// precheck renders the ONE shared Outcome->refusal mapping
// (idmigrate.OutcomeRefusal with a populated store NOT convertible), and that
// only the CUTOVER verdict adds the plain-migrate guidance.
func TestPlainMigrateRefusalIsTheSharedOutcomeMapping(t *testing.T) {
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
		if want == nil {
			want = idmigrate.CutoverGuidanceRefusal(state.StorePath)
		}
		require.Equal(t, want.Error(), plainMigrateRefusal(state).Error(), "outcome %s", state.Outcome)
	}
}

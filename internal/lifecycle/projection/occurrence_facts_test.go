package projection_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
)

func TestReadOccurrenceFactsReturnsJournalTruth(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	ref := seedBlob(t, db, []byte("session facts body"), time.Now())
	commitOccurrenceWithBindings(t, tracker, ref, "session.facts", []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "session_id", Value: "facts-session"},
	})

	facts, err := projection.ReadOccurrenceFacts(tracker.Journal())
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.NotEmpty(t, facts[0].Contract.String())
	require.Equal(t, model.ContractEventKind(1), facts[0].Event)
	require.Len(t, facts[0].Bindings, 1)
	require.Equal(t, "facts-session", facts[0].Bindings[0].Value)
}

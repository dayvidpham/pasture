package projection_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/provenance"
)

func TestReadOccurrenceFactsReturnsJournalTruth(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	ref := seedBlob(t, db, []byte("session facts body"), time.Now())
	commitOccurrenceWithBindings(t, tracker, ref, "session.facts", []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "session_id", Value: "facts-session"},
	})

	facts, err := projection.ReadOccurrenceFacts(context.Background(), tracker.Journal())
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.NotEmpty(t, facts[0].Contract.String())
	require.Equal(t, model.ContractEventKind(1), facts[0].Event)
	require.Len(t, facts[0].Bindings, 1)
	require.Equal(t, "facts-session", facts[0].Bindings[0].Value)
}

func TestOccurrenceFactPagesPinOneSnapshotAndCurrentSuffixDoesNotFreezeAtCursor(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	ref := seedBlob(t, db, []byte("constructed page evidence"), time.Now())
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "paged-session"}}
	commitOccurrenceWithBindings(t, tracker, ref, "page.first", bindings)
	commitOccurrenceWithBindings(t, tracker, ref, "page.second", bindings)
	first, err := projection.ReadOccurrenceFactPage(t.Context(), tracker.Journal().Facts(), provenance.FactPageRequest{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Facts, 1)
	require.NotNil(t, first.Next)
	commitOccurrenceWithBindings(t, tracker, ref, "page.after-snapshot", bindings)
	second, err := projection.ReadOccurrenceFactPage(t.Context(), tracker.Journal().Facts(), provenance.FactPageRequest{Limit: 1, SnapshotMaxJournalID: first.Next.SnapshotMaxJournalID, AfterJournalID: first.Next.AfterJournalID})
	require.NoError(t, err)
	require.Len(t, second.Facts, 1)
	require.Nil(t, second.Next)
	require.Equal(t, first.SnapshotMaxJournalID, second.SnapshotMaxJournalID)
	current, err := projection.ReadOccurrenceFactPage(t.Context(), tracker.Journal().Facts(), provenance.FactPageRequest{Limit: 1, AfterJournalID: first.SnapshotMaxJournalID})
	require.NoError(t, err)
	require.Len(t, current.Facts, 1, "CURRENT suffix must discover the post-snapshot commit")
	require.Greater(t, current.Facts[0].JournalID, first.SnapshotMaxJournalID)
	require.Equal(t, ref.String(), current.Facts[0].PayloadDigest)
	require.Equal(t, model.CaptureValid, current.Facts[0].Capture)
	kinds := projection.OccurrenceEvidenceKinds()
	kinds[0] = "mutated-local-copy"
	require.NotEqual(t, kinds, projection.OccurrenceEvidenceKinds(), "callers cannot mutate the shared codec inventory")
}

package projection_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
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

type syntheticEvidenceReader struct {
	rows  []provenance.EvidenceRow
	kinds []provenance.EvidenceKind
}

func (r *syntheticEvidenceReader) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	r.kinds = q.Kinds
	return provenance.EvidencePage{Rows: r.rows, SnapshotMaxJournalID: 3}, nil
}

func TestOccurrenceFactPageDecodesVersionsAndRejectsInvalidV3(t *testing.T) {
	t.Parallel()
	metadata, err := execution.Normalize(execution.Inputs{Kind: execution.Selection})
	require.NoError(t, err)
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, registration.ClaudeCode__2_1_261().Contract.String())
	require.NoError(t, err)
	ref := func(kind model.DefinitionKind) model.DefinitionRef {
		return model.DefinitionRef{Definition: 1, Kind: kind, Content: model.ContentIdentity{1}}
	}
	envelope := model.OccurrenceEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Contract: contract, Definition: ref(model.DefinitionRuntimeContract)},
		Schema:         model.LifecycleSchemaDefinitionRef{Definition: ref(model.DefinitionLifecycleSchema)},
		Implementation: model.EpochImplementationRef{Definition: ref(model.DefinitionEpochImplementation)},
		Retention:      model.RetentionPolicyDefinitionRef{Definition: ref(model.DefinitionRetentionPolicy)},
	}
	body := func(m any) []byte {
		raw, err := json.Marshal(map[string]any{"contract": contract.String(), "event": 1, "capture": 1, "body_digest": "sha256:synthetic", "envelope": envelope, "execution": m})
		require.NoError(t, err)
		return raw
	}
	r := &syntheticEvidenceReader{rows: []provenance.EvidenceRow{
		{JournalID: 1, EvidenceKind: "pasture.lifecycle.occurrence.v1", Payload: body(metadata)},
		{JournalID: 2, EvidenceKind: "pasture.lifecycle.occurrence.v2", Payload: body(metadata)},
		{JournalID: 3, EvidenceKind: "pasture.lifecycle.occurrence.v3", Payload: body(metadata)},
	}}
	sum := sha256.Sum256(r.rows[2].Payload)
	r.rows[2].ContentDigest = sum[:]
	page, err := projection.ReadOccurrenceFactPage(t.Context(), r, provenance.FactPageRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.occurrence.v2", "pasture.lifecycle.occurrence.v3"}, r.kinds)
	require.Len(t, page.Facts, 3)
	require.Nil(t, page.Facts[0].Execution)
	require.Nil(t, page.Facts[1].Execution)
	require.Equal(t, &metadata, page.Facts[2].Execution)
	r.rows = r.rows[2:]
	r.rows[0].ContentDigest = make([]byte, sha256.Size)
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, provenance.FactPageRequest{Limit: 10})
	require.Error(t, err)
	require.Empty(t, page.Facts)
	metadata.Kind = "unknown"
	r.rows[0].Payload = body(metadata)
	sum = sha256.Sum256(r.rows[0].Payload)
	r.rows[0].ContentDigest = sum[:]
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, provenance.FactPageRequest{Limit: 10})
	require.Error(t, err)
	require.Empty(t, page.Facts)
	// A transition on a non-resume event is never accepted as session proof.
	var invalidTransition map[string]any
	require.NoError(t, json.Unmarshal(body(metadata), &invalidTransition))
	invalidTransition["sessionTransition"] = "resume"
	metadata.Kind = execution.Selection
	invalidTransition["execution"] = metadata
	r.rows[0].Payload, err = json.Marshal(invalidTransition)
	require.NoError(t, err)
	sum = sha256.Sum256(r.rows[0].Payload)
	r.rows[0].ContentDigest = sum[:]
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, provenance.FactPageRequest{Limit: 10})
	require.Error(t, err)
	require.Empty(t, page.Facts)
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

package projection_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

func TestReadOccurrenceFactsReturnsJournalTruth(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	ref := seedBlob(t, db, []byte("session facts body"), time.Now())
	commitOccurrenceWithBindings(t, tracker, ref, "session.facts", []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "session_id", Value: "facts-session"},
	})

	page, err := readFactsPage(t, db, provenance.FactPageRequest{Limit: 10})
	require.NoError(t, err)
	facts := page.Facts
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

func readFactsPage(t *testing.T, db *sql.DB, request provenance.FactPageRequest) (projection.OccurrenceFactPage, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	reader, err := provenance.NewTransactionEvidenceReader(t.Context(), tx)
	require.NoError(t, err)
	return projection.ReadOccurrenceFactPage(t.Context(), reader, tx, request)
}

func (r *syntheticEvidenceReader) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	r.kinds = q.Kinds
	return provenance.EvidencePage{Rows: r.rows, SnapshotMaxJournalID: 3}, nil
}

func TestOccurrenceFactPageDecodesVersionsAndRejectsInvalidV3(t *testing.T) {
	t.Parallel()
	_, db := openReclaimStore(t)
	_, err := projection.ReadOccurrenceFactPage(t.Context(), &syntheticEvidenceReader{}, nil, provenance.FactPageRequest{Limit: 1})
	require.ErrorContains(t, err, "transaction")
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
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
	page, err := projection.ReadOccurrenceFactPage(t.Context(), r, tx, provenance.FactPageRequest{Limit: 10})
	require.ErrorContains(t, err, "definition owner resolution is unavailable")
	require.Equal(t, []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.occurrence.v2", "pasture.lifecycle.occurrence.v3"}, r.kinds)
	require.Empty(t, page.Facts)
	all := r.rows
	r.rows = r.rows[:2]
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, tx, provenance.FactPageRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Facts, 2)
	require.Nil(t, page.Facts[0].Execution)
	require.Nil(t, page.Facts[1].Execution)
	r.rows = all[2:]
	r.rows[0].ContentDigest = make([]byte, sha256.Size)
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, tx, provenance.FactPageRequest{Limit: 10})
	require.Error(t, err)
	require.Empty(t, page.Facts)
	metadata.Kind = "unknown"
	r.rows[0].Payload = body(metadata)
	sum = sha256.Sum256(r.rows[0].Payload)
	r.rows[0].ContentDigest = sum[:]
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, tx, provenance.FactPageRequest{Limit: 10})
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
	page, err = projection.ReadOccurrenceFactPage(t.Context(), r, tx, provenance.FactPageRequest{Limit: 10})
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
	first, err := readFactsPage(t, db, provenance.FactPageRequest{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Facts, 1)
	require.NotNil(t, first.Next)
	commitOccurrenceWithBindings(t, tracker, ref, "page.after-snapshot", bindings)
	second, err := readFactsPage(t, db, provenance.FactPageRequest{Limit: 1, SnapshotMaxJournalID: first.Next.SnapshotMaxJournalID, AfterJournalID: first.Next.AfterJournalID})
	require.NoError(t, err)
	require.Len(t, second.Facts, 1)
	require.Nil(t, second.Next)
	require.Equal(t, first.SnapshotMaxJournalID, second.SnapshotMaxJournalID)
	current, err := readFactsPage(t, db, provenance.FactPageRequest{Limit: 1, AfterJournalID: first.SnapshotMaxJournalID})
	require.NoError(t, err)
	require.Len(t, current.Facts, 1, "CURRENT suffix must discover the post-snapshot commit")
	require.Greater(t, current.Facts[0].JournalID, first.SnapshotMaxJournalID)
	require.Equal(t, ref.String(), current.Facts[0].PayloadDigest)
	require.Equal(t, model.CaptureValid, current.Facts[0].Capture)
	kinds := projection.OccurrenceEvidenceKinds()
	kinds[0] = "mutated-local-copy"
	require.NotEqual(t, kinds, projection.OccurrenceEvidenceKinds(), "callers cannot mutate the shared codec inventory")
}

type countingOwnerReader struct {
	inner   *provenance.TransactionEvidenceReader
	queries int
	lookups int
}

func (r *countingOwnerReader) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	r.queries++
	return r.inner.QueryEvidence(q)
}

func (r *countingOwnerReader) LookupCommitted(op provenance.OperationID) (provenance.CommittedResult, error) {
	r.lookups++
	return r.inner.LookupCommitted(op)
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(100, 0) }

type fixedOperation string

func (o fixedOperation) NewOperationID() (string, error) { return string(o), nil }

// commitOccurrenceV3 commits one authentic-shaped v3 occurrence carrying the
// supplied envelope, so the read path's definition resolution is exercised.
func commitOccurrenceV3(t *testing.T, tracker protocol.TaskTracker, op string, contract ir.RuntimeContractID, event model.ContractEventKind, envelope model.OccurrenceEnvelopeRef) {
	t.Helper()
	ctx := t.Context()
	payload, err := json.Marshal(map[string]any{
		"contract": contract.String(), "event": event, "capture": model.CaptureValid,
		"bindings":    []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "decision-session"}},
		"body_digest": "sha256:synthetic", "envelope": envelope,
	})
	require.NoError(t, err)
	resolver, ok := tracker.(receipt.IdentityResolver)
	require.True(t, ok)
	identity, err := resolver.ResolveLifecycleIdentity(ctx)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte(op), payload...))
	input := provenance.OperationInput{
		OperationID: provenance.OperationID(op), ActorID: identity.Actor, AuthorityJournalID: &identity.Authority,
		CommandDigest: command[:], RecordedAt: time.Unix(100, 0).UnixNano(),
		Effects: []provenance.Effect{{
			Sort: provenance.EffectEvidence, ResultSlot: "occurrence", EvidenceKind: "pasture.lifecycle.occurrence.v3",
			ContentDigest: sum[:], Payload: payload,
		}},
	}
	canonical, err := provenance.Canonicalize(input)
	require.NoError(t, err)
	input.Effects = canonical.NormalizedEffects()
	sum = sha256.Sum256(input.Effects[0].Payload)
	input.Effects[0].ContentDigest = sum[:]
	_, err = tracker.Journal().Apply(input)
	require.NoError(t, err)
}

// TestOccurrenceFactPageResolvesOneDecisionPerDistinctRefSet is the call-site
// proof for the per-page set: two v3 rows carrying the same four definition refs
// must resolve them once, not once per row. It fails if the set is removed (too
// many lookups) or if the admission call is skipped (the page would still decode,
// so the query count is asserted as well).
func TestOccurrenceFactPageResolvesOneDecisionPerDistinctRefSet(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	manifest := registration.ClaudeCode__2_1_261()
	var event model.ContractEventKind
	for _, candidate := range manifest.Events {
		if candidate.NativeName == "UserPromptSubmit" {
			event = candidate.Kind
		}
	}
	require.NotZero(t, event)
	service, err := tasks.NewLifecycleReceiptService(tracker, fixedClock{}, fixedOperation("decision.definition"))
	require.NoError(t, err)
	envelope, err := receipt.EnsureOccurrenceEnvelope(t.Context(), service, manifest.Contract, model.OccurrenceEnvelopeRef{})
	require.NoError(t, err)

	commitOccurrenceV3(t, tracker, "decision.one", manifest.Contract, event, envelope)
	commitOccurrenceV3(t, tracker, "decision.two", manifest.Contract, event, envelope)

	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	inner, err := provenance.NewTransactionEvidenceReader(t.Context(), tx)
	require.NoError(t, err)
	reader := &countingOwnerReader{inner: inner}
	page, err := projection.ReadOccurrenceFactPage(t.Context(), reader, tx, provenance.FactPageRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Facts, 2)
	require.Equal(t, 4, reader.lookups, "two rows sharing one ref set must resolve it once (four refs, one lookup each)")
	require.LessOrEqual(t, reader.queries, 10, "one page query plus at most two evidence reads per resolved ref")
}

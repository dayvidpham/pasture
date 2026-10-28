package tasks

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

type executionTestOperation string

func (o executionTestOperation) NewOperationID() (string, error) { return string(o), nil }

// Constructed journal evidence, not an authentic host capture.
func appendExecutionReplayFact(t *testing.T, tracker protocol.TaskTracker, op string, version string, metadata *execution.Metadata) provenance.JournalID {
	t.Helper()
	manifest := registration.ClaudeCode__2_1_261()
	var event model.ContractEventKind
	for _, candidate := range manifest.Events {
		if candidate.NativeName == "UserPromptSubmit" {
			event = candidate.Kind
		}
	}
	require.NotZero(t, event)
	service, err := NewLifecycleReceiptService(tracker, replayClock{}, executionTestOperation(op+".definition"))
	require.NoError(t, err)
	envelope, err := receipt.EnsureOccurrenceEnvelope(t.Context(), service, manifest.Contract, model.OccurrenceEnvelopeRef{})
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{"contract": manifest.Contract.String(), "event": event, "capture": model.CaptureValid, "bindings": []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "replay-session"}}, "body_digest": "sha256:synthetic", "envelope": envelope, "execution": metadata})
	require.NoError(t, err)
	identity, err := tracker.(lifecycleReceiptStore).ResolveLifecycleIdentity(t.Context())
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte(op), payload...))
	kind := provenance.EvidenceKind("pasture.lifecycle.occurrence." + version)
	if version == "unrelated" {
		kind = "pasture.lifecycle.occurrence-other.v1"
	}
	input := provenance.OperationInput{OperationID: provenance.OperationID(op), ActorID: identity.Actor, AuthorityJournalID: &identity.Authority, CommandDigest: command[:], RecordedAt: time.Unix(100, 0).UnixNano(), Effects: []provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: "occurrence", EvidenceKind: kind, ContentDigest: sum[:], Payload: payload}}}
	canonical, err := provenance.Canonicalize(input)
	require.NoError(t, err)
	input.Effects = canonical.NormalizedEffects()
	sum = sha256.Sum256(input.Effects[0].Payload)
	input.Effects[0].ContentDigest = sum[:]
	result, err := tracker.Journal().Apply(input)
	require.NoError(t, err)
	for _, slot := range result.ResultSlots {
		if slot.Slot == "occurrence" {
			return slot.ProducedJournalID
		}
	}
	t.Fatal("missing occurrence slot")
	return 0
}

func TestDirectReceiptExecutionRebuild(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	manifest := registration.ClaudeCode__2_1_261()
	var event model.ContractEventKind
	for _, candidate := range manifest.Events {
		if candidate.NativeName == "UserPromptSubmit" {
			event = candidate.Kind
		}
	}
	inputs := execution.Inputs{Kind: execution.Selection, Host: execution.Declaration{Model: execution.Raw{Present: true, Member: execution.HostModel, Value: "test-model"}}}
	delivery := receipt.Delivery{Contract: manifest.Contract, Event: event, Capture: model.CaptureValid, Bindings: []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "replay-session"}}, Body: []byte(`{"session_id":"replay-session","hook_event_name":"UserPromptSubmit"}`), ExecutionInputs: &inputs}
	service, err := NewLifecycleReceiptService(tracker, replayClock{}, executionTestOperation("execution.direct"))
	require.NoError(t, err)
	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	require.Nil(t, refusal)
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal)
	received, err := service.Receive(t.Context(), warrant, delivery)
	require.NoError(t, err)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	var raw []byte
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT execution_json FROM pasture_session_execution WHERE journal_id=?`, received.JournalID()).Scan(&raw))
	var metadata execution.Metadata
	require.NoError(t, json.Unmarshal(raw, &metadata))
	require.Equal(t, execution.Observed, metadata.Model.Status)
	require.Equal(t, "test-model", metadata.Model.Value)
	require.Equal(t, execution.Unavailable, metadata.Provider.Status)
}

func TestExecutionReplayPublishesWithinSessionFence(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "execution.legacy-v1", "UserPromptSubmit", "", 10)
	appendExecutionReplayFact(t, tracker, "execution.legacy-v2", "v2", nil)
	metadata, err := execution.Normalize(execution.Inputs{Kind: execution.Primary, Host: execution.Declaration{Model: execution.Raw{Present: true, Member: execution.HostModel, Value: "first"}}, Environment: execution.Declaration{Model: execution.Raw{Present: true, Member: execution.EnvironmentModel, Value: "second"}}})
	require.NoError(t, err)
	require.Equal(t, execution.Conflict, metadata.Model.Status)
	require.Equal(t, execution.Unavailable, metadata.Provider.Status)
	first := appendExecutionReplayFact(t, tracker, "execution.v3-first", "v3", &metadata)
	var second provenance.JournalID
	scans := 0
	_, _, err = refreshSessionState(t.Context(), tracker, true, func(stage sessionReplayStage) {
		if stage == sessionReplayScanned {
			scans++
			if scans == 1 {
				second = appendExecutionReplayFact(t, tracker, "execution.v3-after-scan", "v3", &metadata)
			}
		}
	})
	require.NoError(t, err)
	require.Equal(t, 2, scans)
	var count int
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_execution`).Scan(&count))
	require.Equal(t, 2, count)
	var raw []byte
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT execution_json FROM pasture_session_execution WHERE journal_id=?`, first).Scan(&raw))
	var got execution.Metadata
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, metadata, got)
	status, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	require.GreaterOrEqual(t, status.CompletedThroughJournalID, int64(second))
	_, err = RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_execution`).Scan(&count))
	require.Equal(t, 2, count)
	third := appendExecutionReplayFact(t, tracker, "execution.v3-suffix", "v3", &metadata)
	_, rebuilt, err := EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt)
	require.NoError(t, store.auditDBHandle().QueryRow(`SELECT count(*) FROM pasture_session_execution WHERE journal_id=?`, third).Scan(&count))
	require.Equal(t, 1, count)
	_, err = store.auditDBHandle().Exec(`UPDATE pasture_session_execution SET execution_json='{}' WHERE journal_id=?`, first)
	require.NoError(t, err)
	before, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	_, err = RebuildSessionState(t.Context(), tracker)
	require.ErrorContains(t, err, "conflicts")
	after, err := ReadSessionStateStatus(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, before, after, "refusal must preserve the certified generation")
}

func TestSessionReplayRefusesUnknownOccurrenceKind(t *testing.T) {
	t.Parallel()
	tracker, store, _ := replayStore(t)
	appendReplayFact(t, tracker, "execution.known-kind", "UserPromptSubmit", "", 10)
	appendExecutionReplayFact(t, tracker, "execution.unrelated-kind", "unrelated", nil)
	readPage := func() error {
		tx, err := store.auditDBHandle().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		defer tx.Rollback()
		reader, err := provenance.NewTransactionEvidenceReader(t.Context(), tx)
		require.NoError(t, err)
		_, err = projection.ReadOccurrenceFactPage(t.Context(), reader, tx, provenance.FactPageRequest{Limit: 1})
		return err
	}
	require.NoError(t, readPage(), "unrelated kinds must not be refused")
	appendExecutionReplayFact(t, tracker, "execution.future-kind", "v4", nil)
	require.ErrorContains(t, readPage(), "unsupported lifecycle occurrence")
	_, err := RebuildSessionState(t.Context(), tracker)
	require.ErrorContains(t, err, "unsupported lifecycle occurrence")
}

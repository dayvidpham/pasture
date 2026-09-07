package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func codexFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "codex", "testdata", "fixtures", name))
	require.NoError(t, err)
	return raw
}

// Unsupported native names still refuse before input or storage, even when
// every registered Codex event is enabled. No valid event is held back here.
func TestHookLifecycleResponseRejectsUnsupportedCodexBeforeInputAndStorage(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"NotRegistered", "SessionStarted"} {
		event := event
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
			input := &readTrackingLifecycleInput{}
			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessCodex, Event: event, HostVersion: registration.Codex0_153_0().Version,
				Input: input, Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.withheld.codex"},
			})
			require.ErrorContains(t, err, `declares no native event named "`+event+`"`)
			require.False(t, response.IsValid(), "withheld Codex event must emit no host response")
			require.Zero(t, input.reads, "withheld Codex event must be rejected before stdin access")
			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "withheld Codex event must be rejected before storage access")
		})
	}
}

// TestHookLifecycleResponseCodexCommitsBeforeReturningAndEncodesNativeBytes
// drives authentic Codex fixtures through the real durable handler with
// the production activation configuration. It proves, on the production
// path, that the durable receipt commits before the response is available for
// native encoding, that the native continuation bytes match the pinned golden
// shapes, and that the persisted evidence is provider-correct on bounded
// public read-back.
func TestHookLifecycleResponseCodexCommitsBeforeReturningAndEncodesNativeBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fixture, event string
		kind                 model.ContractEventKind
		semantic             runtime.EventSemantic
		wantResponse         bool
		wantNative           []byte
		wantEvidence         []provenance.EvidenceKind
		wantIdentities       map[runtime.NativeIdentityKind]string
	}{
		{
			name:         "SessionStart observation",
			fixture:      "session_start_0_153_0.json",
			event:        "SessionStart",
			kind:         registration.EventCodexSessionStart,
			semantic:     runtime.SemanticObservation,
			wantResponse: false,
			wantNative:   []byte(`{}`),
			wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2"},
			wantIdentities: map[runtime.NativeIdentityKind]string{
				runtime.IdentitySession: "session_id",
			},
		},
		{
			name:         "PreToolUse gate",
			fixture:      "pre_tool_use_0_153_0.json",
			event:        "PreToolUse",
			kind:         registration.EventCodexPreToolUse,
			semantic:     runtime.SemanticGateConsultation,
			wantResponse: true,
			wantNative:   []byte(`{"continue":true}`),
			wantEvidence: []provenance.EvidenceKind{
				"pasture.lifecycle.occurrence.v1",
				"pasture.lifecycle.interpreted.v2",
				receipt.CurrentConsultationEvidenceKind(),
			},
			wantIdentities: map[runtime.NativeIdentityKind]string{
				runtime.IdentitySession:  "session_id",
				runtime.IdentityTurn:     "turn_id",
				runtime.IdentityToolCall: "tool_use_id",
			},
		},
		{
			name: "Stop gate", fixture: "stop_0_153_0.json", event: "Stop",
			kind: registration.EventCodexStop, semantic: runtime.SemanticGateConsultation,
			wantResponse: true, wantNative: []byte(`{"continue":true}`),
			wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2", receipt.CurrentConsultationEvidenceKind()},
			wantIdentities: map[runtime.NativeIdentityKind]string{
				runtime.IdentitySession: "session_id", runtime.IdentityTurn: "turn_id",
			},
		},
		{
			name: "PostToolUse gate", fixture: "post_tool_use_0_153_0.json", event: "PostToolUse",
			kind: registration.EventCodexPostToolUse, semantic: runtime.SemanticGateConsultation,
			wantResponse: true, wantNative: []byte(`{"continue":true}`),
			wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2", receipt.CurrentConsultationEvidenceKind()},
			wantIdentities: map[runtime.NativeIdentityKind]string{
				runtime.IdentitySession: "session_id", runtime.IdentityTurn: "turn_id", runtime.IdentityToolCall: "tool_use_id",
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := codexFixture(t, tc.fixture)
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			bootstrap, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			_, err = bootstrap.Create("file://codex-handler-test", "bootstrap", "initialize lifecycle system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			require.NoError(t, bootstrap.Close())

			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessCodex, Event: tc.event, HostVersion: registration.Codex0_153_0().Version,
				Input: bytes.NewReader(raw), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.codex." + tc.name},
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantResponse, response.IsValid())

			// Native continuation bytes are the exact host stdout for this event,
			// through the same per-target encoder the registry row (and therefore
			// the CLI via HookLifecycleNative) invokes for Codex.
			native, err := nativeresponse.CodexContinuation(response)
			require.NoError(t, err)
			require.Equal(t, tc.wantNative, native, "native continuation bytes must equal the pinned golden shape")

			// Returning from the handler must imply every expected effect is
			// already durably readable (commit before response/stdout).
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
				Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
				Kinds:  tc.wantEvidence, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
			})
			require.NoError(t, err)
			require.Len(t, page.Rows, len(tc.wantEvidence), "durable evidence must be committed before the handler returns")
			for _, row := range page.Rows {
				require.Equal(t, provenance.OperationID("test.codex."+tc.name), row.ProducingOperationID)
			}
			if tc.wantResponse {
				interpreted := queryOneEvidence(t, tracker, "pasture.lifecycle.interpreted.v2")
				consultation := queryOneEvidence(t, tracker, receipt.CurrentConsultationEvidenceKind())
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID, "one durable operation must group interpreted and consultation evidence")
				require.Less(t, interpreted.JournalID, consultation.JournalID, "interpreted evidence must precede consultation evidence in the committed operation")
			}

			// Bounded provider-correct public read-back.
			require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
			reader, err := tasks.NewLifecycleReader(tracker)
			require.NoError(t, err)
			pageSize, err := model.NewPageSize(4)
			require.NoError(t, err)
			records, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
			require.NoError(t, err)
			require.Len(t, records.Records(), 1, "the bounded read-back must return exactly the one committed occurrence")
			record := records.Records()[0]
			require.Equal(t, registration.Codex0_153_0().Contract, record.Occurrence.RuntimeContract, "read-back must preserve the Codex provider contract")
			require.Equal(t, registration.Codex0_153_0().Version, record.Occurrence.Envelope.HostVersion)
			require.Equal(t, tc.kind, record.Occurrence.Kind)
			require.Len(t, record.Interpreted(), 1)
			interpreted := record.Interpreted()[0]
			require.Equal(t, runtime.Codex0_153_0().ID(), interpreted.Contract(), "interpreted read-back must carry the Codex runtime contract, never OpenCode or Claude")
			require.Equal(t, tc.semantic, interpreted.Semantic())
			identities := make(map[runtime.NativeIdentityKind]string, len(interpreted.Identities()))
			for _, identity := range interpreted.Identities() {
				identities[identity.Kind] = identity.Value
			}
			// wantIdentities names the payload member each identity kind is bound
			// from; the expected value is read from the committed fixture, so the
			// test follows the capture instead of restating its ids.
			var payload map[string]any
			require.NoError(t, json.Unmarshal(raw, &payload))
			want := make(map[runtime.NativeIdentityKind]string, len(tc.wantIdentities))
			for kind, member := range tc.wantIdentities {
				value, ok := payload[member].(string)
				require.True(t, ok, "fixture %s carries no string member %q for identity %v", tc.fixture, member, kind)
				want[kind] = value
			}
			require.Equal(t, want, identities, "read-back identities must be the provider-correct Codex correlation set")
		})
	}
}

package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/tasks"
)

type fixedLifecycleClock struct{}

func (fixedLifecycleClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

type fixedLifecycleOperations struct{ id string }

func (s fixedLifecycleOperations) NewOperationID() (string, error) { return s.id, nil }

type openCodeRecord struct {
	Value json.RawMessage `json:"value"`
}

type readTrackingLifecycleInput struct{ reads int }

func (r *readTrackingLifecycleInput) Read([]byte) (int, error) {
	r.reads++
	return 0, fmt.Errorf("test input must not be read before activation admission")
}

func TestHookLifecycleResponseRejectsWithheldOpenCodeBeforeInputAndStorage(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
	input := &readTrackingLifecycleInput{}
	response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: dbPath, Harness: ir.HarnessOpenCode, Event: "session.updated", HostVersion: "1.18.10",
		Input: input, Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.withheld"},
	})
	require.ErrorContains(t, err, `OpenCode event "session.updated" is withheld (reason outside-target-set)`)
	require.False(t, response.IsValid())
	require.Zero(t, input.reads, "withheld event must be rejected before stdin access")
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "withheld event must be rejected before storage access")
}

func TestHookLifecycleResponseRejectsUncorrelatedClaudeElicitationBeforeInputAndStorage(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"Elicitation", "ElicitationResult"} {
		event := event
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
			input := &readTrackingLifecycleInput{}
			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessClaudeCode, Event: event, HostVersion: "2.1.222",
				Input: input, Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.withheld.claude"},
			})
			require.ErrorContains(t, err, `Claude event "`+event+`" is withheld (reason missing-request-correlation)`)
			require.False(t, response.IsValid(), "withheld elicitation must emit no host response")
			require.Zero(t, input.reads, "withheld elicitation must be rejected before stdin access")
			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "withheld elicitation must be rejected before storage access")
		})
	}
}

func TestHookLifecycleResponseOpenCodeCommitsBeforeReturning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fixture, event string
		wantResponse         bool
		wantEvidence         []provenance.EvidenceKind
	}{
		{name: "observation", fixture: "session_created_1_18_10.capture.json", event: "session.created", wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2"}},
		{name: "gate", fixture: "tool_execute_before_1_18_10.capture.json", event: "tool.execute.before", wantResponse: true, wantEvidence: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.interpreted.v2", "pasture.lifecycle.consultation.v1"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "opencode", "testdata", "fixtures", test.fixture))
			require.NoError(t, err)
			var record openCodeRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			bootstrap, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			_, err = bootstrap.Create("file://handler-test", "bootstrap", "initialize lifecycle system identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
			require.NoError(t, err)
			require.NoError(t, bootstrap.Close())
			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessOpenCode, Event: test.event, HostVersion: "1.18.10",
				Input: bytes.NewReader(record.Value), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test." + test.name},
			})
			require.NoError(t, err)
			require.Equal(t, test.wantResponse, response.IsValid())
			if test.wantResponse {
				require.Equal(t, backend.DecisionProceed, response.Decision())
				require.JSONEq(t, `{"decision":"proceed"}`, string(requireMarshalResponse(t, response)))
			}

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
				Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
				Kinds:  test.wantEvidence, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
			})
			require.NoError(t, err)
			require.Len(t, page.Rows, len(test.wantEvidence), "returning from the handler must imply every expected effect is already readable")
			for _, row := range page.Rows {
				require.Equal(t, provenance.OperationID("test."+test.name), row.ProducingOperationID)
			}
			if test.wantResponse {
				occurrence := queryOneEvidence(t, tracker, "pasture.lifecycle.occurrence.v1")
				interpreted := queryOneEvidence(t, tracker, "pasture.lifecycle.interpreted.v2")
				consultation := queryOneEvidence(t, tracker, "pasture.lifecycle.consultation.v1")
				require.Equal(t, occurrence.ProducingOperationJournalID, interpreted.ProducingOperationJournalID)
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID)
				require.Less(t, interpreted.JournalID, consultation.JournalID, "interpreted evidence must precede consultation evidence in the committed operation")
			}
		})
	}
}

func queryOneEvidence(t *testing.T, tracker interface{ Journal() provenance.Journal }, kind provenance.EvidenceKind) provenance.EvidenceRow {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{kind}, Page: provenance.FactPageRequest{Limit: 1},
	})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	return page.Rows[0]
}

func TestHookLifecycleResponsePersistenceFailureReturnsNoResponse(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "opencode", "testdata", "fixtures", "tool_execute_before_1_18_10.capture.json"))
	require.NoError(t, err)
	var record openCodeRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	directory := filepath.Join(t.TempDir(), "database-directory")
	require.NoError(t, os.Mkdir(directory, 0o700))
	response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: directory, Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: "1.18.10",
		Input: bytes.NewReader(record.Value), Clock: fixedLifecycleClock{}, Operations: fixedLifecycleOperations{id: "test.failure"},
	})
	require.Error(t, err)
	require.False(t, response.IsValid(), "a response must not escape when persistence fails")
}

func requireMarshalResponse(t *testing.T, response backend.HostResponse) []byte {
	t.Helper()
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	return raw
}

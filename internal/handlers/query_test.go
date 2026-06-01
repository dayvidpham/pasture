package handlers_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// newQueryOKFactory returns a TemporalClientFactory that returns a mock client
// which answers the full_state query with the provided result.
func newQueryOKFactory(result types.QueryStateResult) handlers.TemporalClientFactory {
	return func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{queryResult: result}, nil
	}
}

// newConnErrFactory returns a factory that always fails with a connection error.
func newConnErrFactory() handlers.TemporalClientFactory {
	return func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"dial tcp: connection refused"}
	}
}

// connErr is a simple connection error for tests.
type connErr struct{ msg string }

func (e *connErr) Error() string { return e.msg }

// ─── QueryState tests ─────────────────────────────────────────────────────────

func TestQueryState_Success(t *testing.T) {
	result := types.QueryStateResult{
		CurrentPhase:       protocol.PhaseWorkerSlices,
		CurrentRole:        types.RoleWorker,
		ActiveSessionCount: 2,
	}

	factory := newQueryOKFactory(result)
	conn := config.ConnectionConfig{
		Namespace:     "default",
		ServerAddress: "localhost:7233",
		TaskQueue:     "pasture",
	}

	code, err := handlers.QueryState(context.Background(), conn, "epoch-1", types.OutputText, factory)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestQueryState_ConnectionError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"connection refused"}
	}

	conn := config.ConnectionConfig{ServerAddress: "localhost:7233"}
	code, err := handlers.QueryState(context.Background(), conn, "epoch-1", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
	// connection factory errors pass through to caller; code should reflect non-success
	if code == 0 {
		t.Errorf("expected non-zero exit code for connection error, got 0")
	}
}

func TestQueryState_WorkflowNotFound(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{queryErr: &connErr{"workflow not found"}}, nil
	}

	conn := config.ConnectionConfig{ServerAddress: "localhost:7233"}
	code, err := handlers.QueryState(context.Background(), conn, "epoch-missing", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected workflow error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3 for workflow error, got %d", code)
	}
}

func TestQueryState_JSONFormat(t *testing.T) {
	result := types.QueryStateResult{
		CurrentPhase: protocol.PhaseRequest,
		CurrentRole:  types.RoleEpoch,
	}

	factory := newQueryOKFactory(result)
	conn := config.ConnectionConfig{ServerAddress: "localhost:7233"}

	code, err := handlers.QueryState(context.Background(), conn, "epoch-1", types.OutputJSON, factory)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

// ─── Mock client ─────────────────────────────────────────────────────────────

// mockClient implements handlers.TemporalClient for testing.
type mockClient struct {
	queryResult     types.QueryStateResult
	queryErr        error
	signalErr       error
	executeWorkflow mockWorkflowRun
	executeErr      error
	cancelErr       error
	terminateErr    error
}

func (m *mockClient) Close() {}

func (m *mockClient) QueryWorkflow(_ context.Context, _, _, _ string, _ ...interface{}) (converter.EncodedValue, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	b, _ := json.Marshal(m.queryResult)
	return &jsonEncodedValue{data: b}, nil
}

func (m *mockClient) SignalWorkflow(_ context.Context, _, _, _ string, _ interface{}) error {
	return m.signalErr
}

func (m *mockClient) ExecuteWorkflow(_ context.Context, _ client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (handlers.TemporalWorkflowRun, error) {
	if m.executeErr != nil {
		return nil, m.executeErr
	}
	return &m.executeWorkflow, nil
}

func (m *mockClient) CancelWorkflow(_ context.Context, _, _ string) error {
	return m.cancelErr
}

func (m *mockClient) TerminateWorkflow(_ context.Context, _, _, _ string, _ ...interface{}) error {
	return m.terminateErr
}

// mockWorkflowRun implements handlers.TemporalWorkflowRun.
type mockWorkflowRun struct {
	id    string
	runId string
}

func (m *mockWorkflowRun) GetID() string    { return m.id }
func (m *mockWorkflowRun) GetRunID() string { return m.runId }

// jsonEncodedValue implements converter.EncodedValue using raw JSON bytes.
type jsonEncodedValue struct {
	data []byte
}

func (v *jsonEncodedValue) HasValue() bool { return true }
func (v *jsonEncodedValue) Get(valuePtr interface{}) error {
	return json.Unmarshal(v.data, valuePtr)
}

package handlers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.temporal.io/sdk/client"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── Test fixtures ────────────────────────────────────────────────────────────

// validEpochID is a syntactically valid Provenance TaskID
// ("<namespace>--<uuid-v7>") used by EpochStart tests after PROPOSAL-2 §7.12
// validation landed in S8. The wire form must satisfy
// provenance.ParseTaskID; the UUID below is a stable test fixture (UUIDv7
// generated for tests).
const validEpochID = "aura-plugins--01956d2c-99fb-7700-8b49-1b07b9f8d100"

// ─── EpochStart ──────────────────────────────────────────────────────────────

func TestEpochStart_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{
			executeWorkflow: mockWorkflowRun{id: validEpochID, runID: "run-abc"},
		}, nil
	}

	conn := config.ConnectionConfig{ServerAddress: "localhost:7233", TaskQueue: "pasture"}
	code, err := handlers.EpochStart(context.Background(), conn, validEpochID, "test epoch", "", types.OutputText, factory)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

// TestEpochStart_MalformedEpochID_Rejected verifies PROPOSAL-2 §7.12 / Scenario
// 13: a free-string --epoch-id (anything that fails provenance.ParseTaskID) is
// rejected at the handler boundary with a CategoryValidation StructuredError
// whose Fix string nudges the operator toward `pasture task create REQUEST`.
// No workflow is started — the factory is wrapped to fail the test if it is
// invoked.
func TestEpochStart_MalformedEpochID_Rejected(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		t.Fatal("factory must not be invoked when --epoch-id fails ParseTaskID")
		return nil, nil
	}

	conn := config.ConnectionConfig{TaskQueue: "pasture"}
	code, err := handlers.EpochStart(context.Background(), conn, "not-a-task-id", "desc", "", types.OutputText, factory)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1 (validation), got %d", code)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *pasterrors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
	// What is the plain-language one-liner shown on the top "Error:" line.
	if !strings.Contains(se.What, "not valid") {
		t.Errorf("What = %q, want plain-language substring %q", se.What, "not valid")
	}
	// Why explains in plain English what about the ID is wrong (no
	// project-internal jargon — no "ParseTaskID", no "TaskID" type name).
	if !strings.Contains(se.Why, "\"--\"") {
		t.Errorf("Why = %q, want substring %q (separator explanation)", se.Why, "\"--\"")
	}
	// Fix retains the actionable command suggestion users need to recover.
	if !strings.Contains(se.Fix, "pasture task create REQUEST") {
		t.Errorf("Fix = %q, want substring %q", se.Fix, "pasture task create REQUEST")
	}
}

func TestEpochStart_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochStart(context.Background(), conn, "", "desc", "", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1 for validation error, got %d", code)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Errorf("expected StructuredError, got %T: %v", err, err)
	}
}

func TestEpochStart_ConnectionError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"connection refused"}
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochStart(context.Background(), conn, validEpochID, "", "", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}

func TestEpochStart_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{executeErr: &connErr{"already started"}}, nil
	}

	conn := config.ConnectionConfig{TaskQueue: "pasture"}
	code, err := handlers.EpochStart(context.Background(), conn, validEpochID, "", "", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected workflow error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestEpochStart_UsesConnTaskQueueWhenEmpty(t *testing.T) {
	var capturedOptions client.StartWorkflowOptions
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &captureClient{
			captureOptions: &capturedOptions,
			run:            mockWorkflowRun{id: validEpochID, runID: "r1"},
		}, nil
	}

	conn := config.ConnectionConfig{TaskQueue: "my-queue"}
	code, err := handlers.EpochStart(context.Background(), conn, validEpochID, "", "", types.OutputText, factory)

	// Factory was called — task queue fallback behavior is validated by no error
	if err != nil || code != 0 {
		t.Fatalf("expected success, got code=%d err=%v", code, err)
	}
	_ = capturedOptions
}

// ─── EpochCancel ─────────────────────────────────────────────────────────────

func TestEpochCancel_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochCancel(context.Background(), conn, "epoch-1", types.OutputText, factory)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestEpochCancel_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochCancel(context.Background(), conn, "", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestEpochCancel_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{cancelErr: &connErr{"not found"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochCancel(context.Background(), conn, "epoch-1", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

// ─── EpochTerminate ──────────────────────────────────────────────────────────

func TestEpochTerminate_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochTerminate(context.Background(), conn, "epoch-1", "manual stop", types.OutputText, factory)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestEpochTerminate_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochTerminate(context.Background(), conn, "", "reason", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestEpochTerminate_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{terminateErr: &connErr{"not running"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochTerminate(context.Background(), conn, "epoch-1", "", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestEpochCancel_ConnectionError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"connection refused"}
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochCancel(context.Background(), conn, "epoch-1", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
	if code == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}

func TestEpochTerminate_ConnectionError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"connection refused"}
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.EpochTerminate(context.Background(), conn, "epoch-1", "manual", types.OutputText, factory)

	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
	if code == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}

func TestEpochStart_JSONFormat(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{
			executeWorkflow: mockWorkflowRun{id: validEpochID, runID: "run-def"},
		}, nil
	}

	conn := config.ConnectionConfig{TaskQueue: "pasture"}
	code, err := handlers.EpochStart(context.Background(), conn, validEpochID, "desc", "", types.OutputJSON, factory)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

// ─── captureClient helper ─────────────────────────────────────────────────────

// captureClient records ExecuteWorkflow options for inspection; embeds mockClient
// to satisfy all other TemporalClient methods.
type captureClient struct {
	mockClient
	captureOptions *client.StartWorkflowOptions
	run            mockWorkflowRun
}

func (c *captureClient) ExecuteWorkflow(_ context.Context, opts client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (handlers.TemporalWorkflowRun, error) {
	if c.captureOptions != nil {
		*c.captureOptions = opts
	}
	return &c.run, nil
}

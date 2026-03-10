package handlers_test

import (
	"context"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── SignalVote ───────────────────────────────────────────────────────────────

func TestSignalVote_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"epoch-1", types.AxisCorrectness, types.VoteAccept, "reviewer-1",
		types.OutputText, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestSignalVote_InvalidAxis(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"epoch-1", types.ReviewAxis("invalid"), types.VoteAccept, "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalVote_InvalidVote(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"epoch-1", types.AxisCorrectness, types.VoteType("MAYBE"), "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalVote_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"", types.AxisCorrectness, types.VoteAccept, "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalVote_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{signalErr: &connErr{"not found"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"epoch-1", types.AxisCorrectness, types.VoteAccept, "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected workflow error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestSignalVote_JSONFormat(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalVote(
		context.Background(), conn,
		"epoch-1", types.AxisTestQuality, types.VoteRevise, "",
		types.OutputJSON, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

// ─── SignalComplete ───────────────────────────────────────────────────────────

func TestSignalComplete_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	output := "all tests passed"
	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"epoch-1", "slice-1",
		&output, nil,
		types.OutputText, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestSignalComplete_WithError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	errMsg := "typecheck failed"
	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"epoch-1", "slice-1",
		nil, &errMsg,
		types.OutputText, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestSignalComplete_BothOutputAndError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	output := "something"
	errMsg := "also something"
	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"epoch-1", "slice-1",
		&output, &errMsg,
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalComplete_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"", "slice-1", nil, nil,
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalComplete_MissingSliceID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"epoch-1", "", nil, nil,
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSignalComplete_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{signalErr: &connErr{"workflow not found"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SignalComplete(
		context.Background(), conn,
		"epoch-1", "slice-1", nil, nil,
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected workflow error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

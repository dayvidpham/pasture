package handlers_test

import (
	"context"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── PhaseAdvance ─────────────────────────────────────────────────────────────

func TestPhaseAdvance_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.PhaseAdvance(
		context.Background(), conn,
		"epoch-1", protocol.P10_CodeReview, "supervisor", "all slices complete",
		types.OutputText, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestPhaseAdvance_InvalidPhase(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.PhaseAdvance(
		context.Background(), conn,
		"epoch-1", protocol.PhaseId("p99"), "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestPhaseAdvance_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.PhaseAdvance(
		context.Background(), conn,
		"", protocol.P2_Elicit, "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestPhaseAdvance_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{signalErr: &connErr{"workflow not found"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.PhaseAdvance(
		context.Background(), conn,
		"epoch-1", protocol.P3_Propose, "supervisor", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestPhaseAdvance_JSONFormat(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.PhaseAdvance(
		context.Background(), conn,
		"epoch-1", protocol.Complete, "epoch", "all phases done",
		types.OutputJSON, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

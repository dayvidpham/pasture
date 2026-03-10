package handlers_test

import (
	"context"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── SessionRegister ─────────────────────────────────────────────────────────

func TestSessionRegister_Success(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "session-abc", "worker", "claude-code", "claude-sonnet-4-6",
		types.OutputText, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestSessionRegister_MissingEpochID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"", "session-abc", "worker", "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSessionRegister_MissingSessionID(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "", "worker", "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSessionRegister_MissingRole(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "session-abc", "", "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestSessionRegister_WorkflowError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{signalErr: &connErr{"workflow not found"}}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "session-abc", "supervisor", "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestSessionRegister_JSONFormat(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return &mockClient{}, nil
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "session-xyz", "reviewer", "claude-code", "claude-opus-4",
		types.OutputJSON, factory,
	)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}
}

func TestSessionRegister_ConnectionError(t *testing.T) {
	factory := func(_ context.Context, _ config.ConnectionConfig) (handlers.TemporalClient, error) {
		return nil, &connErr{"connection refused"}
	}

	conn := config.ConnectionConfig{}
	code, err := handlers.SessionRegister(
		context.Background(), conn,
		"epoch-1", "session-1", "worker", "", "",
		types.OutputText, factory,
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code == 0 {
		t.Errorf("expected non-zero exit code, got 0")
	}
}

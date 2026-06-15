package main_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// seedEpochDB launches a real engine on a fresh on-disk pasture.db, drives an
// epoch a couple of phases so a projection row exists, shuts the engine down,
// and returns the db path. The compiled CLI then reads that file — exercising
// the production fold end-to-end (binary → projection reader → SQL read).
func seedEpochDB(t *testing.T, epochId string) string {
	t.Helper()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       "test-v1",
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect", ConditionMet: "elicited"},
	}
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: epochId, Advances: plan},
		dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	if _, err := h.GetResult(dbos.WithHandleTimeout(30 * time.Second)); err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	e.Shutdown(5 * time.Second)
	return dbPath
}

func TestCLI_QueryState_JSON(t *testing.T) {
	t.Parallel()
	const epochId = "demo--query-state"
	db := seedEpochDB(t, epochId)

	out := runCLI(t, "--db", db, "--format", "json", "query", "state", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("query state exit %d; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
	var got struct {
		CurrentPhase         string   `json:"currentPhase"`
		AvailableTransitions []string `json:"availableTransitions"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &got); err != nil {
		t.Fatalf("decode query json: %v\nbody: %s", err, out.stdout)
	}
	if got.CurrentPhase != string(protocol.PhasePropose) {
		t.Errorf("currentPhase = %q, want %q", got.CurrentPhase, protocol.PhasePropose)
	}
	if len(got.AvailableTransitions) == 0 {
		t.Error("expected recomputed available transitions, got none")
	}
}

func TestCLI_QueryCurrentAndTransitions(t *testing.T) {
	t.Parallel()
	const epochId = "demo--query-current"
	db := seedEpochDB(t, epochId)

	current := runCLI(t, "--db", db, "--format", "json", "query", "current", "--epoch-id", epochId)
	if current.exitCode != 0 {
		t.Fatalf("query current exit %d; stderr=%s", current.exitCode, current.stderr)
	}
	var cur struct {
		CurrentPhase string `json:"currentPhase"`
		CurrentRole  string `json:"currentRole"`
	}
	if err := json.Unmarshal([]byte(current.stdout), &cur); err != nil {
		t.Fatalf("decode current json: %v\nbody: %s", err, current.stdout)
	}
	if cur.CurrentPhase != string(protocol.PhasePropose) {
		t.Errorf("currentPhase = %q, want %q", cur.CurrentPhase, protocol.PhasePropose)
	}

	trans := runCLI(t, "--db", db, "--format", "json", "query", "transitions", "--epoch-id", epochId)
	if trans.exitCode != 0 {
		t.Fatalf("query transitions exit %d; stderr=%s", trans.exitCode, trans.stderr)
	}
	var tr struct {
		AvailableTransitions []string `json:"availableTransitions"`
	}
	if err := json.Unmarshal([]byte(trans.stdout), &tr); err != nil {
		t.Fatalf("decode transitions json: %v\nbody: %s", err, trans.stdout)
	}
	if len(tr.AvailableTransitions) == 0 {
		t.Error("expected at least one available transition")
	}
}

// TestCLI_QuerySessionsAndSliceProgress checks the two detail verbs are wired
// and read the projection (empty lists render cleanly with a clean exit).
func TestCLI_QuerySessionsAndSliceProgress(t *testing.T) {
	t.Parallel()
	const epochId = "demo--query-detail"
	db := seedEpochDB(t, epochId)

	for _, verb := range []string{"sessions", "slice-progress"} {
		out := runCLI(t, "--db", db, "--format", "json", "query", verb, "--epoch-id", epochId)
		if out.exitCode != 0 {
			t.Fatalf("query %s exit %d; stdout=%s stderr=%s", verb, out.exitCode, out.stdout, out.stderr)
		}
	}
}

// TestCLI_QueryUnknownEpoch covers the fresh-database path: opening a db on
// which no epoch ran must report a not-found (exit 3), not a raw SQL error.
func TestCLI_QueryUnknownEpoch(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	out := runCLI(t, "--db", db, "query", "state", "--epoch-id", "demo--never-ran")
	if out.exitCode != 3 {
		t.Fatalf("unknown-epoch query exit %d (want 3); stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
}

// TestCLI_QueryMissingEpochId asserts the required-flag guard (exit 1).
func TestCLI_QueryMissingEpochId(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	out := runCLI(t, "--db", db, "query", "state")
	if out.exitCode != 1 {
		t.Fatalf("missing --epoch-id exit %d (want 1); stderr=%s", out.exitCode, out.stderr)
	}
}

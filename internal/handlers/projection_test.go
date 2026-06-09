package handlers_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// newQueryEngine launches a real engine against an on-disk pasture.db and drives
// it a couple of phases so a projection row exists to read. It returns the
// engine (a ProjectionReader) and the epoch id.
func newQueryEngine(t *testing.T, epochId string) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })

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
	return e
}

// TestQueryStateFromProjection_RecomputesTransitions proves the query core reads
// the projected state and recomputes the available transitions via the FSM
// (rather than returning a value frozen at write time).
func TestQueryStateFromProjection_RecomputesTransitions(t *testing.T) {
	const epochId = "proj--query-1"
	e := newQueryEngine(t, epochId)

	state, err := e.ReadProjection(epochId)
	if err != nil {
		t.Fatalf("ReadProjection: %v", err)
	}
	if state == nil {
		t.Fatal("ReadProjection returned nil for an epoch that advanced")
	}

	result := handlers.QueryStateFromProjection(state)
	if result.CurrentPhase != protocol.PhasePropose {
		t.Errorf("CurrentPhase = %q, want %q", result.CurrentPhase, protocol.PhasePropose)
	}

	// From propose, the FSM offers review (forward) — recomputed, not stored.
	want := protocol.NewEpochStateMachineFromState(state, nil).AvailableTransitions()
	if len(result.AvailableTransitions) != len(want) {
		t.Fatalf("AvailableTransitions = %v, want %v", result.AvailableTransitions, want)
	}
	if len(result.AvailableTransitions) == 0 {
		t.Error("expected at least one available transition from propose")
	}
	if got := len(result.TransitionHistory); got != 2 {
		t.Errorf("TransitionHistory = %d, want 2", got)
	}
}

// TestQueryEpochState_SupportedQueries exercises the production handler for each
// projection-serviceable query against a real engine, asserting a clean exit.
func TestQueryEpochState_SupportedQueries(t *testing.T) {
	const epochId = "proj--query-2"
	e := newQueryEngine(t, epochId)

	for _, q := range []protocol.QueryName{
		protocol.QueryCurrentState,
		protocol.QueryAvailableTransitions,
		protocol.QueryFullState,
	} {
		code, err := handlers.QueryEpochState(e, epochId, q, types.OutputText)
		if err != nil {
			t.Errorf("QueryEpochState(%q) err = %v", q, err)
		}
		if code != 0 {
			t.Errorf("QueryEpochState(%q) exit = %d, want 0", q, code)
		}
	}
}

// seedProjection launches an engine and writes a projection row carrying the
// given sessions + slice-progress, so the session/progress query paths can be
// exercised without driving the signal loop. Returns the engine (a reader).
func seedProjection(t *testing.T, epochId string, sessions []protocol.RegisterSessionSignal, progress []protocol.SliceProgressSignal) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{DBPath: dbPath, ApplicationVersion: "test-v1"})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })

	state := &protocol.EpochState{
		EpochId:            epochId,
		CurrentPhase:       protocol.PhaseWorkerSlices,
		CurrentRole:        protocol.RoleSupervisor,
		ActiveSessions:     sessions,
		ActiveSessionCount: len(sessions),
		SliceProgress:      progress,
	}
	if err := engine.WriteProjection(context.Background(), e.DB(), state, time.Now().UnixNano()); err != nil {
		t.Fatalf("WriteProjection: %v", err)
	}
	return e
}

func TestQueryEpochState_ActiveSessions(t *testing.T) {
	const epochId = "proj--sessions"
	e := seedProjection(t, epochId, []protocol.RegisterSessionSignal{
		{EpochId: epochId, SessionId: "s1", Role: "worker"},
		{EpochId: epochId, SessionId: "s2", Role: "reviewer"},
	}, nil)

	code, err := handlers.QueryEpochState(e, epochId, protocol.QueryActiveSessions, types.OutputText)
	if err != nil {
		t.Fatalf("active_sessions query err = %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

func TestQueryEpochState_SliceProgress(t *testing.T) {
	const epochId = "proj--progress"
	e := seedProjection(t, epochId, nil, []protocol.SliceProgressSignal{
		{SliceId: "slice-1", LeafTaskId: "leaf-1", StageName: "impl", Completed: true},
	})

	code, err := handlers.QueryEpochState(e, epochId, protocol.QuerySliceProgressState, types.OutputText)
	if err != nil {
		t.Fatalf("slice_progress_state query err = %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

func TestQueryEpochState_UnknownEpoch(t *testing.T) {
	e := newQueryEngine(t, "proj--query-3")
	code, err := handlers.QueryEpochState(e, "proj--does-not-exist", protocol.QueryFullState, types.OutputText)
	if err == nil {
		t.Fatal("expected an error for an unknown epoch")
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3 (workflow/not-found)", code)
	}
}

func TestQueryEpochState_MissingEpochId(t *testing.T) {
	e := newQueryEngine(t, "proj--query-4")
	code, err := handlers.QueryEpochState(e, "", protocol.QueryFullState, types.OutputText)
	if err == nil {
		t.Fatal("expected a validation error for an empty epoch id")
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (validation)", code)
	}
}

func TestQueryEpochState_RejectsUnknownQuery(t *testing.T) {
	e := newQueryEngine(t, "proj--query-5")
	code, err := handlers.QueryEpochState(e, "proj--query-5", protocol.QueryName("bogus_query"), types.OutputText)
	if err == nil {
		t.Fatal("expected a validation error for an unrecognized query name")
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (validation)", code)
	}
}

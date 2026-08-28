package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// recvProbeTimeout is the receive deadline the probe workflow waits out. Nothing
// ever sends on the probe topic, so the deadline always expires; it is short
// because the wait is the whole point and no other test work depends on it.
const recvProbeTimeout = 50 * time.Millisecond

// recvProbeResult reports how the workflow itself classified the receive error,
// so the assertion covers the production predicate running inside a real
// workflow rather than a value built by the test.
type recvProbeResult struct {
	ClassifiedAsTimeout bool
	ErrText             string
}

// recvTimeoutProbeWorkflow waits out a receive deadline on a topic no one sends
// to, then classifies the runtime's own error with isRecvTimeout.
func recvTimeoutProbeWorkflow(ctx dbos.Context, topic string) (recvProbeResult, error) {
	_, err := dbos.Recv[string](ctx, topic, recvProbeTimeout)
	if err == nil {
		return recvProbeResult{}, fmt.Errorf("receive on topic %q returned no error; the probe needs the deadline to expire", topic)
	}
	return recvProbeResult{ClassifiedAsTimeout: isRecvTimeout(err), ErrText: err.Error()}, nil
}

// newRecvProbeEngine builds and launches an engine with the probe workflow
// registered. Registration must happen between New and Launch, which is why the
// engine is built here instead of through an already-launched helper.
func newRecvProbeEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(context.Background(), Config{
		DBPath:                   testutil.GoldenUnifiedDBPath(t),
		ApplicationVersion:       "test-app-recv-timeout",
		ExecutorID:               "test-executor-recv-timeout",
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })
	dbos.RegisterWorkflow(e.dbosCtx, recvTimeoutProbeWorkflow)
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	return e
}

// TestIsRecvTimeout_MatchesRuntimeRecvTimeout pins the receive-timeout
// classification to the value the durable runtime actually produces.
//
// isRecvTimeout decides whether the control workflow parks (no message right
// now) or ends with an error (a real delivery failure), so a classification that
// silently stops matching turns every idle poll into a workflow failure. No unit
// test over a hand-built error can catch that: the runtime is the only source of
// the value under test. This test therefore makes the runtime raise the timeout,
// and asserts on the error the runtime returns, on the error the runtime
// persisted, and on the code the runtime stamped.
func TestIsRecvTimeout_MatchesRuntimeRecvTimeout(t *testing.T) {
	e := newRecvProbeEngine(t)

	const workflowID = "recv-timeout-probe"
	handle, err := dbos.RunWorkflow(e.dbosCtx, recvTimeoutProbeWorkflow, "probe-topic",
		dbos.WithWorkflowID(workflowID))
	if err != nil {
		t.Fatalf("RunWorkflow(recv probe): %v", err)
	}
	result, err := handle.GetResult(dbos.WithHandleTimeout(30 * time.Second))
	if err != nil {
		t.Fatalf("recv probe workflow: %v", err)
	}

	// 1. The error the runtime returns on a fresh execution.
	if !result.ClassifiedAsTimeout {
		t.Errorf("isRecvTimeout on the live receive error = false, want true; the runtime returned: %s",
			result.ErrText)
	}

	// 2. The same error after a database round trip. A replayed receive returns
	//    the persisted error decoded by the runtime, which is the value read
	//    back here, so this covers the replay classification too.
	steps, err := dbos.GetWorkflowSteps(e.dbosCtx, workflowID)
	if err != nil {
		t.Fatalf("GetWorkflowSteps(%q): %v", workflowID, err)
	}
	var stored error
	for _, step := range steps {
		if step.Error != nil {
			stored = step.Error
			break
		}
	}
	if stored == nil {
		t.Fatalf("no step of workflow %q recorded an error; the receive timeout was not persisted (steps: %d)",
			workflowID, len(steps))
	}
	if !isRecvTimeout(stored) {
		t.Errorf("isRecvTimeout on the persisted receive error = false, want true; the runtime stored: %v", stored)
	}

	// 3. The code the runtime stamped. isRecvTimeout matches the dbos.ErrTimeout
	//    sentinel, and the sentinel matches by code; this asserts that the code
	//    on the runtime's own error is still the timeout code, so a runtime that
	//    re-codes receive timeouts fails here with the code it moved to.
	var typed *dbos.Error
	if !errors.As(stored, &typed) {
		t.Fatalf("persisted receive error type = %T, want *dbos.Error carrying an error code", stored)
	}
	if typed.Code != dbos.ErrorCodeTimeout {
		t.Errorf("persisted receive error code = %v, want %v", typed.Code, dbos.ErrorCodeTimeout)
	}
}

// TestIsRecvTimeout_RejectsOtherRuntimeErrors pins the other direction: the
// predicate must not classify a real failure as an idle poll, or the control
// workflow would loop over a delivery failure instead of reporting it. The
// non-timeout error is again taken from the runtime, not hand-built.
func TestIsRecvTimeout_RejectsOtherRuntimeErrors(t *testing.T) {
	e := newRecvProbeEngine(t)

	_, err := dbos.RetrieveWorkflow[string](e.dbosCtx, "no-such-workflow-id")
	if err == nil {
		t.Fatal("RetrieveWorkflow on an unknown id returned no error; the probe needs the runtime's failure")
	}
	if !errors.Is(err, dbos.ErrNonExistentWorkflow) {
		t.Fatalf("runtime error for an unknown workflow id = %v, want a non-existent-workflow error; "+
			"this test needs a real non-timeout runtime error", err)
	}
	if isRecvTimeout(err) {
		t.Errorf("isRecvTimeout on a non-existent-workflow error = true, want false; "+
			"the predicate would swallow a real failure: %v", err)
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"unrelated error", errors.New("no message received within 50ms")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if isRecvTimeout(tc.err) {
				t.Errorf("isRecvTimeout(%v) = true, want false", tc.err)
			}
		})
	}
}

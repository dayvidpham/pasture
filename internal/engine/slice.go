package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SliceInput is the input to a slice sub-workflow.
type SliceInput struct {
	// EpochId is the parent epoch this slice belongs to. Used for hook dispatch
	// and the parent progress signal.
	EpochId string `json:"epochId"`
	// SliceId is the unique identifier for this slice. It doubles as the
	// sub-workflow's id so start_slice / complete_slice signals address it.
	SliceId string `json:"sliceId"`
	// ParentWorkflowId is the id of the epoch control workflow that dispatched
	// this slice. When non-empty, the sub-workflow delivers a slice_progress
	// signal to it on completion.
	ParentWorkflowId string `json:"parentWorkflowId"`
}

// SliceResult is the output of a slice sub-workflow.
type SliceResult struct {
	// SliceId echoes the input for correlation on the parent side.
	SliceId string `json:"sliceId"`
	// Success is true when the slice completed without error.
	Success bool `json:"success"`
	// Output holds a human-readable success message (non-empty when Success is true).
	Output string `json:"output,omitempty"`
	// Error holds the failure reason (non-empty when Success is false).
	Error *string `json:"error,omitempty"`
}

// SliceSubWorkflow is the DBOS sub-workflow for a single implementation slice.
//
// Lifecycle:
//  1. Dispatched via Engine.EnqueueSlice to the slice queue; starts when a queue
//     slot is free (bounded by the configured concurrency limit K).
//  2. Receives a start_slice signal (SliceStartSignal) via dbos.Recv before
//     deciding the execution mode. Defaults to mock if no signal arrives within
//     a brief fixed deadline (2s).
//  3. Executes the slice in the chosen mode (mock / tmux / subprocess) inside a
//     durable step.
//  4. Receives an optional complete_slice signal (SliceCompleteSignal) that
//     overrides the computed outcome.
//  5. Dispatches hook events (SliceStarted / SliceCompleted / SliceFailed)
//     through the engine's hook manager inside durable steps (memoized; not
//     re-fired on crash recovery).
//  6. Sends a slice_progress signal to the parent epoch workflow.
//
// The start_slice and complete_slice signals are addressed to the sub-workflow
// by its sliceId (which is its DBOS workflow id).
func (e *Engine) SliceSubWorkflow(ctx dbos.DBOSContext, in SliceInput) (SliceResult, error) {
	// ── 1. Receive the start_slice configuration signal (non-blocking with
	//       a brief deadline so the sub-workflow doesn't park forever waiting
	//       for a signal that may never arrive in mock/CI mode).
	const startSignalTimeout = 2 * time.Second
	startSig, err := dbos.Recv[protocol.SliceStartSignal](ctx,
		protocol.SignalStartSlice.String(), startSignalTimeout)
	if err != nil && !isRecvTimeout(err) {
		return SliceResult{}, fmt.Errorf("slice %q: unexpected error receiving start_slice signal: %w", in.SliceId, err)
	}

	// Resolve the execution mode: signal wins (including unrecognised modes,
	// so runSlice can return an actionable error), then default to mock.
	mode := protocol.SliceMock
	var command string
	timeoutSecs := 300
	if err == nil {
		// Signal was received successfully. Pass the mode as-is — runSlice
		// handles unrecognised modes with an actionable validation error.
		mode = startSig.Mode
		command = startSig.Command
		if startSig.TimeoutSeconds > 0 {
			timeoutSecs = startSig.TimeoutSeconds
		}
	}

	// ── 2. Dispatch HookSliceStarted (best-effort; hooks are non-fatal).
	e.dispatchHook(ctx, hooks.HookSliceStarted, in.EpochId, map[string]any{
		"sliceId": in.SliceId,
		"mode":    string(mode),
	})

	// ── 3. Execute the slice inside a durable step.
	result, err := dbos.RunAsStep(ctx, func(c context.Context) (SliceResult, error) {
		return runSlice(c, in.SliceId, in.EpochId, mode, command, timeoutSecs)
	})
	if err != nil {
		errMsg := err.Error()
		result = SliceResult{SliceId: in.SliceId, Success: false, Error: &errMsg}
	}

	// ── 4. Receive an optional complete_slice override (non-blocking).
	const completeSignalTimeout = 1 * time.Second
	completeSig, cerr := dbos.Recv[protocol.SliceCompleteSignal](ctx,
		protocol.SignalCompleteSlice.String(), completeSignalTimeout)
	if cerr == nil {
		// Override applied.
		result = SliceResult{
			SliceId: in.SliceId,
			Success: completeSig.Success,
			Output:  completeSig.Output,
			Error:   completeSig.Error,
		}
	}
	// A timeout on complete_slice is normal (no override) — ignore it.

	// ── 5. Dispatch completion hook (best-effort).
	if result.Success {
		e.dispatchHook(ctx, hooks.HookSliceCompleted, in.EpochId, map[string]any{
			"sliceId": in.SliceId,
			"output":  result.Output,
		})
	} else {
		errVal := ""
		if result.Error != nil {
			errVal = *result.Error
		}
		e.dispatchHook(ctx, hooks.HookSliceFailed, in.EpochId, map[string]any{
			"sliceId": in.SliceId,
			"error":   errVal,
		})
	}

	// ── 6. Report progress to the parent epoch workflow (non-fatal if it has
	//       already completed; the parent keeps the workflow_status row so
	//       dbos.Send to a completed workflow returns nil — see note in
	//       internal/handlers/controller_test.go on dbos.Send semantics).
	if in.ParentWorkflowId != "" {
		progressSig := protocol.SliceProgressSignal{
			SliceId:    in.SliceId,
			LeafTaskId: in.SliceId,
			StageName:  "execute",
			Completed:  result.Success,
		}
		if serr := dbos.Send(ctx, in.ParentWorkflowId, progressSig,
			protocol.SignalSliceProgress.String()); serr != nil {
			// Log but do not fail the sub-workflow: the parent may have finished
			// or been cancelled. The slice's own result is still durable.
			slog.Default().Warn("slice progress signal not delivered to parent",
				"sliceId", in.SliceId,
				"parentWorkflowId", in.ParentWorkflowId,
				"err", serr,
			)
		}
	}

	return result, nil
}

// runSlice executes the slice in the given mode and returns the result.
// Called inside a durable step so its effect is checkpointed by DBOS.
//
// Modes:
//   - mock: no-op stub; always returns Success=true. Used for testing and dry-runs.
//   - tmux / subprocess: real agent execution is not yet implemented in the DBOS
//     adapter; returns an actionable not-implemented error with Success=false. The
//     operator can still report an outcome via the complete_slice signal override.
//     Full agent execution is tracked as a separate follow-up epic.
//   - any other value: validation error with Success=false.
func runSlice(_ context.Context, sliceId, _ string, mode protocol.SliceExecutionMode, _ string, _ int) (SliceResult, error) {
	switch mode {
	case protocol.SliceMock:
		return SliceResult{SliceId: sliceId, Success: true, Output: "mock: completed"}, nil

	case protocol.SliceTmux, protocol.SliceSubprocess:
		// Real agent execution is not yet implemented in the DBOS adapter.
		// Returning Success=false (not Success=true) preserves forensic integrity:
		// a fabricated success would write a false completed-projection record and
		// fire HookSliceCompleted without any execution having occurred.
		// Use the complete_slice signal override to report the real outcome once
		// the agent finishes externally. Wired by the daemon at startup.
		errMsg := fmt.Sprintf(
			"slice %q: execution mode %q is not yet implemented.\n"+
				"Problem: the DBOS adapter cannot launch tmux sessions or subprocesses directly.\n"+
				"Why: real agent execution is a tracked follow-up; the current adapter supports mock mode only.\n"+
				"How to fix:\n"+
				"  1. Re-enqueue with mode=mock if you only need to test the signal path.\n"+
				"  2. Run the slice externally and report the outcome with:\n"+
				"       pasture slice complete --slice-id %s --output \"<result>\"\n"+
				"     or, on failure:\n"+
				"       pasture slice complete --slice-id %s --error \"<reason>\"",
			sliceId, mode, sliceId, sliceId,
		)
		return SliceResult{SliceId: sliceId, Success: false, Error: &errMsg}, nil

	default:
		errMsg := fmt.Sprintf(
			"slice %q: unrecognised execution mode %q.\n"+
				"How to fix: pass one of the supported modes: mock, tmux, subprocess.",
			sliceId, mode,
		)
		return SliceResult{SliceId: sliceId, Success: false, Error: &errMsg}, nil
	}
}

// dispatchHook fires a hook event through the engine's hook manager inside a
// durable DBOS step (memoized — not re-executed on crash recovery). If the
// manager is nil (Config.HooksMgr was not set), the call is a no-op.
//
// The step is identified by the event name + epoch id, making each dispatch
// deterministic and replay-stable. Hook failures are logged but never
// propagated: hooks are optional observability, not control flow.
//
// Config.HooksMgr is wired by the daemon at startup; callers that omit it
// (e.g. tests that don't need observability) see no dispatches.
func (e *Engine) dispatchHook(ctx dbos.DBOSContext, event hooks.HookEvent, epochId string, data map[string]any) {
	if e.cfg.HooksMgr == nil {
		return
	}
	mgr := e.cfg.HooksMgr
	payload := hooks.HookPayload{
		Event:   event,
		EpochId: epochId,
		Data:    data,
	}
	_, _ = dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		if _, err := mgr.Dispatch(stepCtx, payload); err != nil {
			// Log but do not fail the step: hook dispatch is best-effort.
			slog.Default().Warn("hook dispatch error",
				"event", string(event),
				"epochId", epochId,
				"err", err,
			)
		}
		return struct{}{}, nil
	})
}

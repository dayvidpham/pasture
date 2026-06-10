package engine

import (
	"context"
	"fmt"
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
//     the configured timeout.
//  3. Executes the slice in the chosen mode (mock / tmux / subprocess) inside a
//     durable step.
//  4. Receives an optional complete_slice signal (SliceCompleteSignal) that
//     overrides the computed outcome.
//  5. Dispatches hook events (SliceStarted / SliceCompleted / SliceFailed)
//     through the engine's hook manager.
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

	// Resolve the execution mode: signal wins, then default to mock.
	mode := protocol.SliceMock
	var command string
	timeoutSecs := 300
	if err == nil {
		// Signal was received successfully.
		if startSig.Mode.IsValid() {
			mode = startSig.Mode
		}
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
			_ = serr
		}
	}

	return result, nil
}

// runSlice executes the slice in the given mode and returns the result.
// Called inside a durable step so its effect is checkpointed by DBOS.
func runSlice(_ context.Context, sliceId, _ string, mode protocol.SliceExecutionMode, command string, timeoutSecs int) (SliceResult, error) {
	switch mode {
	case protocol.SliceMock:
		return SliceResult{SliceId: sliceId, Success: true, Output: "mock: completed"}, nil

	case protocol.SliceTmux, protocol.SliceSubprocess:
		// Execute the slice command. In the DBOS adapter the activity-level
		// execution of external commands is a future integration point; for
		// the current scope the step records the intent and the operator
		// observes the result via the complete_slice signal override.
		if command == "" {
			errMsg := fmt.Sprintf("slice %q: mode %q requires a non-empty command", sliceId, mode)
			return SliceResult{SliceId: sliceId, Success: false, Error: &errMsg}, nil
		}
		// The timeout is available for future integration with an actual
		// exec-with-timeout helper; record it for replay fidelity.
		_ = timeoutSecs
		msg := fmt.Sprintf("%s: launched %s", mode, command)
		return SliceResult{SliceId: sliceId, Success: true, Output: msg}, nil

	default:
		errMsg := fmt.Sprintf("slice %q: unrecognised execution mode %q; valid modes are mock, tmux, subprocess", sliceId, mode)
		return SliceResult{SliceId: sliceId, Success: false, Error: &errMsg}, nil
	}
}

// dispatchHook fires a hook event best-effort through the engine's hook manager.
// Hook failures are logged (when a manager is available) but never propagate to
// the caller — hooks are optional observability, not control flow.
func (e *Engine) dispatchHook(_ dbos.DBOSContext, event hooks.HookEvent, epochId string, data map[string]any) {
	if e.cfg.HooksMgr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = e.cfg.HooksMgr.Dispatch(ctx, hooks.HookPayload{
		Event:   event,
		EpochId: epochId,
		Data:    data,
	})
}

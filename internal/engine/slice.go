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
//     deciding the execution mode. If no signal arrives within the deadline the
//     sub-workflow records an honest failure (Success=false) and returns; no
//     completion hook fires and the parent projection receives Completed=false.
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
	//       a brief deadline). If no signal arrives, record an honest failure:
	//       the slice was enqueued but never started.
	const startSignalTimeout = 2 * time.Second
	startSig, err := dbos.Recv[protocol.SliceStartSignal](ctx,
		protocol.SignalStartSlice.String(), startSignalTimeout)
	if err != nil && !isRecvTimeout(err) {
		return SliceResult{}, fmt.Errorf("slice %q: unexpected error receiving start_slice signal: %w", in.SliceId, err)
	}

	// If no signal arrived within the window, return an honest failure.
	// Recording a fabricated success for a slice that was never started
	// would corrupt the completion projection and the audit record.
	// The caller must send a start_slice signal at or before enqueue.
	if isRecvTimeout(err) {
		errMsg := fmt.Sprintf(
			"no start_slice signal received for slice %q within %s — "+
				"the slice was enqueued but never started.\n"+
				"Problem: the start_slice signal did not arrive before the %s deadline.\n"+
				"Why: the signal must be delivered at or shortly after EnqueueSlice returns "+
				"(before the %s window closes).\n"+
				"How to fix:\n"+
				"  1. Send the signal immediately after enqueueing:\n"+
				"       pasture slice start --slice-id %s --mode mock\n"+
				"  2. Or re-enqueue and send the signal within %s.",
			in.SliceId, startSignalTimeout,
			startSignalTimeout, startSignalTimeout,
			in.SliceId, startSignalTimeout,
		)
		noStartResult := SliceResult{SliceId: in.SliceId, Success: false, Error: &errMsg}

		// Fire the SliceFailed hook (not SliceCompleted — the slice never ran).
		e.dispatchHook(ctx, hooks.HookSliceFailed, in.EpochId, map[string]any{
			"sliceId": in.SliceId,
			"error":   errMsg,
		})

		// Report Completed=false to the parent.
		if in.ParentWorkflowId != "" {
			progressSig := protocol.SliceProgressSignal{
				SliceId:    in.SliceId,
				LeafTaskId: in.SliceId,
				StageName:  "execute",
				Completed:  false,
			}
			if serr := dbos.Send(ctx, in.ParentWorkflowId, progressSig,
				protocol.SignalSliceProgress.String()); serr != nil {
				slog.Default().Warn("slice progress signal not delivered to parent",
					"sliceId", in.SliceId,
					"parentWorkflowId", in.ParentWorkflowId,
					"err", serr,
				)
			}
		}
		return noStartResult, nil
	}

	// Signal was received. Pass the mode as-is — runSlice handles unrecognised
	// modes with an actionable validation error.
	mode := startSig.Mode
	command := startSig.Command
	timeoutSecs := 300
	if startSig.TimeoutSeconds > 0 {
		timeoutSecs = startSig.TimeoutSeconds
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
//   - tmux / subprocess: deliberate stub. The command and timeoutSecs parameters
//     are the seams for real agent execution, but the DBOS adapter does not
//     launch them yet. Returns an actionable not-implemented error with
//     Success=false so no reader or audit record mistakes it for finished work.
//   - any other value: validation error with Success=false.
func runSlice(_ context.Context, sliceId, _ string, mode protocol.SliceExecutionMode, _ string, _ int) (SliceResult, error) {
	switch mode {
	case protocol.SliceMock:
		return SliceResult{SliceId: sliceId, Success: true, Output: "mock: completed"}, nil

	case protocol.SliceTmux, protocol.SliceSubprocess:
		// DELIBERATE STUB (pending real-exec integration).
		// This branch is a placeholder to be filled in at a later date; it does
		// not yet run the slice command.
		//
		// WHAT TO IMPLEMENT: the real tmux/subprocess agent-exec path: launch
		// the slice command in the chosen execution mode, stream/capture its
		// output, honor timeoutSecs by killing and failing on overrun, and
		// surface the true process outcome.
		//
		// EXPECTED BEHAVIOUR ONCE IMPLEMENTED: return an honest SliceResult that
		// reflects the actual process result. Success=true only if the command
		// genuinely ran and exited 0; fire HookSliceCompleted and write the
		// completed projection only on real completion. Never fabricate success.
		//
		// FEATURE ENABLED: real multi-agent slice execution under the DBOS
		// substrate, driving actual agent/subprocess work per slice instead of
		// mock-only mode.
		//
		// Until then we return Success=false with an actionable not-implemented
		// error so no reader or audit record mistakes this for finished work.
		errMsg := fmt.Sprintf(
			"slice %q: execution mode %q is not yet implemented.\n"+
				"Problem: the DBOS adapter cannot launch tmux sessions or subprocesses directly.\n"+
				"Why: this execution mode is a deliberate stub pending real tmux/subprocess agent-exec; the current adapter supports mock mode only.\n"+
				"Where: running a slice workflow in internal/engine/slice.go.\n"+
				"Impact: no slice command was run, no completion hook was fired, and no completed projection was written.\n"+
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
// durable DBOS step (memoized by positional replay order — not re-executed on
// crash recovery). If the manager is nil (Config.HooksMgr was not set), the
// call is a no-op.
//
// Hook failures are logged but never propagated: hooks are optional
// observability, not control flow.
//
// pastured wires Config.HooksMgr when it hosts the engine. Callers that omit it
// (e.g. CLI paths and tests that don't need observability) see no dispatches.
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

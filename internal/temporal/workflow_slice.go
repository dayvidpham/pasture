package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SliceWorkflow is the child workflow for a single P9_Slice implementation slice.
//
// Lifecycle:
//  1. Parent (EpochWorkflow) starts SliceWorkflow with SliceInput.
//  2. Workflow defaults to mock mode (immediate success) unless a SliceStartSignal
//     is received before run begins.
//  3. On completion, signals the parent EpochWorkflow via slice_progress using
//     input.ParentWorkflowId. Signal delivery is best-effort; if the parent has
//     already completed, the exception is caught and ignored (non-fatal).
//
// Port of Python SliceWorkflow in scripts/aura_protocol/workflow.py.
type SliceWorkflow struct {
	startSignal    *SliceStartSignal
	completeSignal *SliceCompleteSignal
}

// SliceStartSignal is the signal payload for configuring SliceWorkflow execution mode.
type SliceStartSignal struct {
	// Mode controls how the slice is executed: "mock", "tmux", or "subprocess".
	Mode string `json:"mode"`
	// Command is the shell command to execute (tmux/subprocess mode only).
	Command string `json:"command"`
	// TimeoutSeconds overrides the default activity start-to-close timeout.
	TimeoutSeconds int `json:"timeoutSeconds"`
}

// SliceCompleteSignal is the signal payload for external completion override.
// When received, the slice adopts the reported outcome instead of the computed one.
type SliceCompleteSignal struct {
	Success bool    `json:"success"`
	Output  string  `json:"output,omitempty"`
	Error   *string `json:"error,omitempty"`
}

// StartSlice is the signal handler that configures slice execution before run.
func (sw *SliceWorkflow) StartSlice(_ workflow.Context, sig SliceStartSignal) {
	sw.startSignal = &sig
}

// CompleteSlice is the signal handler for external completion override.
func (sw *SliceWorkflow) CompleteSlice(_ workflow.Context, sig SliceCompleteSignal) {
	sw.completeSignal = &sig
}

// hookDispatchOptions returns short, non-retryable activity options for hook
// dispatch activities. Hooks are best-effort — a single attempt with a 5 s
// deadline; failure is logged and the workflow continues.
var hookDispatchOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 5 * time.Second,
}

// dispatchHookActivity fires a DispatchHook activity in the workflow context.
// Hook dispatch is best-effort: errors are logged but never propagated to the
// caller. Workflows must not fail due to hook errors.
//
// ActivityDispatchHook is referenced by string name since the activity is
// registered as a method on *Activities via RegisterWorkflows.
func dispatchHookActivity(ctx workflow.Context, payload hooks.HookPayload) {
	hookCtx := workflow.WithActivityOptions(ctx, hookDispatchOptions)
	if err := workflow.ExecuteActivity(hookCtx, ActivityDispatchHook, payload).Get(hookCtx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("SliceWorkflow: DispatchHook activity failed (best-effort, non-fatal)",
			"event", string(payload.Event),
			"epochId", payload.EpochId,
			"error", err,
		)
	}
}

// Run executes a single implementation slice.
//
// Execution modes (from SliceStartSignal, defaults to "mock"):
//   - "mock": Returns success immediately. Used in tests and CI mode.
//   - "tmux" / "subprocess": Executes command via execute_slice_command activity.
//
// After computation, waits briefly for a SliceCompleteSignal override (1 s timeout).
// If an override arrives, it replaces the computed result.
//
// On completion, signals the parent EpochWorkflow via slice_progress.
// Signal delivery failure is non-fatal (parent may have already completed).
//
// Hook dispatch (HookSliceStarted, HookSliceCompleted, HookSliceFailed) runs
// via DispatchHook activity calls. Hook failures are logged but never fail the
// workflow — hooks are optional, best-effort observability.
func (sw *SliceWorkflow) Run(ctx workflow.Context, input SliceInput) (*SliceResult, error) {
	// Register signal handlers via goroutine-per-channel pattern.
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, protocol.SignalStartSlice)
		for {
			var sig SliceStartSignal
			ch.Receive(ctx, &sig)
			sw.StartSlice(ctx, sig)
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, protocol.SignalCompleteSlice)
		for {
			var sig SliceCompleteSignal
			ch.Receive(ctx, &sig)
			sw.CompleteSlice(ctx, sig)
		}
	})

	mode := "mock"
	var command string
	timeoutSecs := 300
	if sw.startSignal != nil {
		if sw.startSignal.Mode != "" {
			mode = sw.startSignal.Mode
		}
		command = sw.startSignal.Command
		if sw.startSignal.TimeoutSeconds > 0 {
			timeoutSecs = sw.startSignal.TimeoutSeconds
		}
	}

	// Best-effort: fire HookSliceStarted before slice execution begins.
	dispatchHookActivity(ctx, hooks.HookPayload{
		Event:   hooks.HookSliceStarted,
		EpochId: input.EpochId,
		Data: map[string]any{
			"sliceId": input.SliceId,
			"mode":    mode,
		},
	})

	var result *SliceResult

	switch mode {
	case "mock":
		result = &SliceResult{SliceId: input.SliceId, Success: true}

	case "tmux", "subprocess":
		// Delegate to execute_slice_command activity.
		actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Duration(timeoutSecs) * time.Second,
		})
		var actResult SliceResult
		if err := workflow.ExecuteActivity(actCtx, "execute_slice_command",
			command, input.SliceId, input.EpochId,
		).Get(actCtx, &actResult); err != nil {
			errMsg := err.Error()
			result = &SliceResult{
				SliceId: input.SliceId,
				Success: false,
				Error:   &errMsg,
			}
		} else {
			result = &actResult
		}

	default:
		errMsg := fmt.Sprintf("unknown execution mode %q; must be mock, tmux, or subprocess", mode)
		result = &SliceResult{
			SliceId: input.SliceId,
			Success: false,
			Error:   &errMsg,
		}
	}

	// Brief wait for a CompleteSlice override (1 s timeout in production;
	// resolves instantly in test time-skipping environments).
	// AwaitWithTimeout returns (ok bool, err error); we discard both.
	if sw.completeSignal == nil {
		_, _ = workflow.AwaitWithTimeout(ctx, time.Second,
			func() bool { return sw.completeSignal != nil },
		)
	}

	// Apply external override if received.
	if sw.completeSignal != nil {
		cs := sw.completeSignal
		result = &SliceResult{
			SliceId: input.SliceId,
			Success: cs.Success,
			Output:  cs.Output,
			Error:   cs.Error,
		}
	}

	// Best-effort: fire HookSliceCompleted or HookSliceFailed based on result.
	if result.Success {
		dispatchHookActivity(ctx, hooks.HookPayload{
			Event:   hooks.HookSliceCompleted,
			EpochId: input.EpochId,
			Data: map[string]any{
				"sliceId": input.SliceId,
				"output":  result.Output,
			},
		})
	} else {
		errVal := ""
		if result.Error != nil {
			errVal = *result.Error
		}
		dispatchHookActivity(ctx, hooks.HookPayload{
			Event:   hooks.HookSliceFailed,
			EpochId: input.EpochId,
			Data: map[string]any{
				"sliceId": input.SliceId,
				"error":   errVal,
			},
		})
	}

	// Signal parent EpochWorkflow with slice completion progress.
	// Use input.ParentWorkflowId (explicit) for testability.
	// Signal delivery failure is non-fatal: parent may have already completed.
	if input.ParentWorkflowId != "" {
		progressSig := protocol.SliceProgressSignal{
			SliceId:    input.SliceId,
			LeafTaskId: input.SliceId,
			StageName:  "execute",
			Completed:  result.Success,
		}
		if sigErr := workflow.SignalExternalWorkflow(ctx, input.ParentWorkflowId, "", protocol.SignalSliceProgress, progressSig).Get(ctx, nil); sigErr != nil {
			workflow.GetLogger(ctx).Warn("SliceWorkflow: parent signal delivery failed",
				"sliceId", input.SliceId,
				"parentWorkflowId", input.ParentWorkflowId,
				"error", sigErr)
		}
	}

	return result, nil
}

// SliceWorkflowFn is the top-level function registered with the Temporal worker.
// Exported for test registration via TestWorkflowEnvironment.RegisterWorkflow.
func SliceWorkflowFn(ctx workflow.Context, input SliceInput) (*SliceResult, error) {
	sw := &SliceWorkflow{}
	return sw.Run(ctx, input)
}

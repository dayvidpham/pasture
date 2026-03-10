package handlers

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
)

// EpochStart starts a new EpochWorkflow with the given epochID and description.
//
// The workflowID is set to epochID so callers can reference the workflow by a
// human-readable name. taskQueue defaults to conn.TaskQueue when empty.
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func EpochStart(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID, description, taskQueue string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "epoch-id is required",
			Why:      "--epoch-id flag was not provided",
			Impact:   "epoch cannot be started without an ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	if taskQueue == "" {
		taskQueue = conn.TaskQueue
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	input := temporal.EpochInput{
		EpochID:            epochID,
		RequestDescription: description,
	}

	opts := client.StartWorkflowOptions{
		ID:        epochID,
		TaskQueue: taskQueue,
	}
	run, err := c.ExecuteWorkflow(ctx, opts, temporal.EpochWorkflowType, input)
	if err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("failed to start epoch workflow %q", epochID),
			Why:      err.Error(),
			Impact:   "epoch was not started",
			Fix:      fmt.Sprintf("verify that epoch %q does not already exist and that pastured is running on task queue %q", epochID, taskQueue),
		}
	}

	out, fmtErr := formatters.FormatStartResult(run.GetID(), run.GetRunID(), format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// EpochCancel requests graceful cancellation of a running EpochWorkflow.
//
// The workflow receives a cancellation request and can perform cleanup before
// stopping. Use EpochTerminate for immediate (non-graceful) termination.
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func EpochCancel(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "epoch-id is required",
			Why:      "--epoch-id flag was not provided",
			Impact:   "epoch cannot be cancelled without an ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	if err := c.CancelWorkflow(ctx, epochID, ""); err != nil {
		return 3, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("cancel failed for epoch %q", epochID),
			Why:      err.Error(),
			Impact:   "cancellation was not issued",
			Fix:      fmt.Sprintf("verify that epoch %q exists and is running", epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// EpochTerminate immediately terminates a running EpochWorkflow (non-graceful).
//
// Unlike EpochCancel, terminate stops the workflow immediately without giving
// it a chance to run cleanup handlers. Use a descriptive reason so the audit
// trail is informative.
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func EpochTerminate(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID, reason string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "epoch-id is required",
			Why:      "--epoch-id flag was not provided",
			Impact:   "epoch cannot be terminated without an ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	if err := c.TerminateWorkflow(ctx, epochID, "", reason); err != nil {
		return 3, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("terminate failed for epoch %q", epochID),
			Why:      err.Error(),
			Impact:   "termination was not issued",
			Fix:      fmt.Sprintf("verify that epoch %q exists and is running", epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

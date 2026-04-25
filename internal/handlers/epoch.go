package handlers

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
)

// validateEpochIDForHandler enforces PROPOSAL-2 §7.12 at the handler boundary:
// the supplied --epoch-id MUST parse as a Provenance TaskID
// ("<namespace>--<uuid>"). Free-string epoch IDs are rejected with a
// CategoryValidation StructuredError per the §7.12 example, so no signal /
// workflow start ever runs against an ID that cannot align with the audit /
// Provenance / Temporal subsystems.
//
// The caller token (e.g. "EpochStart") appears in the error's What field so
// users can attribute the failure to the right entry point. The Fix string
// matches the §7.12 example verbatim ("pasture task create REQUEST ...") for
// Scenario 13's substring assertion.
func validateEpochIDForHandler(epochID, caller string) error {
	if _, err := provenance.ParseTaskID(epochID); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What: fmt.Sprintf(
				"%s: epoch-id %q is not a valid Provenance TaskID",
				caller, epochID,
			),
			Why: err.Error(),
			Impact: "the workflow cannot be started without an epoch ID that aligns " +
				"across the audit, Provenance, and Temporal subsystems (URD R5); " +
				"a malformed epoch_id would produce dangling correlations in context_edges " +
				"because no row in tasks.id matches the free string",
			Fix: "create the REQUEST task first with " +
				"`pasture task create REQUEST --type=feature \"<title>\"` and pass the " +
				"returned ID as --epoch-id; or use " +
				"`pasture task list --status=open --type=feature` to find an existing one",
		}
	}
	return nil
}

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

	// PROPOSAL-2 §7.12 / Scenario 13: reject malformed epoch IDs before any
	// signal/workflow start so no row leaks to audit_events, context_edges,
	// or tasks for a malformed epoch_id. The activity entry in
	// internal/temporal/activities.go enforces the same check as defence in
	// depth against direct Temporal client calls that bypass this handler.
	if vErr := validateEpochIDForHandler(epochID, "handlers.EpochStart"); vErr != nil {
		return pasterrors.ExitCode(vErr), vErr
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

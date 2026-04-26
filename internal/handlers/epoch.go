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
			What:     "The epoch ID you provided is not valid.",
			Why: fmt.Sprintf(
				"%q doesn't match the required ID format. We expect IDs that look like\n"+
					"\"yourproject--01968a3c-1234-...\" — a project name followed by \"--\"\n"+
					"and a UUID. The separator \"--\" was not found in what you provided,\n"+
					"so we couldn't split it into the two required parts.",
				epochID,
			),
			Impact: "The epoch can't be started. Without a properly-formatted ID, the audit\n" +
				"log can't link events back to any task, which would leave a broken trail.",
			Fix: "1. Create a task first to get a valid ID:\n" +
				"     pasture task create REQUEST --type=feature \"<title>\"\n" +
				"2. Or find one that already exists:\n" +
				"     pasture task list --status=open --type=feature\n" +
				"3. Pass the returned ID as --epoch-id when starting the epoch.",
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
			What:     "An epoch ID is required to start an epoch.",
			Why:      "The --epoch-id flag was not provided.",
			Impact:   "The epoch can't be started without an ID to identify it.",
			Fix: "1. Pass an epoch ID when starting the epoch:\n" +
				"     pasture-msg epoch start --epoch-id <id> ...\n" +
				"2. If you don't have an ID yet, create a task first:\n" +
				"     pasture task create REQUEST --type=feature \"<title>\"",
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
			What:     fmt.Sprintf("The epoch workflow %q couldn't be started.", epochID),
			Why:      fmt.Sprintf("The Temporal server rejected the start request: %s", err),
			Impact:   "The epoch did not start, so no workflow steps will run for it.",
			Fix: fmt.Sprintf("1. Check whether an epoch with this ID is already running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. Confirm pastured is running and listening on the right task queue (%q):\n"+
				"     pastured --task-queue %s\n"+
				"3. Retry the start once the queue is healthy.",
				epochID, taskQueue, taskQueue),
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
			What:     "An epoch ID is required to cancel an epoch.",
			Why:      "The --epoch-id flag was not provided.",
			Impact:   "Without an ID, there's no way to know which epoch to cancel.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture-msg epoch cancel --epoch-id <id>\n" +
				"2. If you don't know which epochs are running, list them:\n" +
				"     pasture-msg epoch list",
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
			What:     fmt.Sprintf("Couldn't cancel the epoch %q.", epochID),
			Why:      fmt.Sprintf("The Temporal server rejected the cancel request: %s", err),
			Impact:   "The epoch is still running. The cancellation request never reached it.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, the ID may be wrong — list active epochs:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry once you've confirmed the epoch exists.",
				epochID),
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
			What:     "An epoch ID is required to terminate an epoch.",
			Why:      "The --epoch-id flag was not provided.",
			Impact:   "Without an ID, there's no way to know which epoch to terminate.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture-msg epoch terminate --epoch-id <id> --reason \"<why>\"\n" +
				"2. If you don't know which epochs are running, list them:\n" +
				"     pasture-msg epoch list",
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
			What:     fmt.Sprintf("Couldn't terminate the epoch %q.", epochID),
			Why:      fmt.Sprintf("The Temporal server rejected the terminate request: %s", err),
			Impact:   "The epoch is still running. The terminate request never reached it.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, the ID may be wrong — list active epochs:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry once you've confirmed the epoch exists.",
				epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

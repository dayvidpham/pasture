package handlers

import (
	"context"
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
)

// validateEpochID enforces that an --epoch-id parses as a Provenance TaskId
// ("<namespace>--<uuid>"). A free-string epoch id is rejected so an epoch never
// runs against an id that cannot align the task, audit, and durable subsystems
// (they all key on the same id).
//
// caller appears in the error's Where field so users can attribute the failure
// to the entry point.
func validateEpochID(epochId, caller string) error {
	if _, err := provenance.ParseTaskID(epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The epoch ID %q is not valid.", epochId),
			Why: "Epoch IDs need the shape \"yourproject--01968a3c-...\" — a project name\n" +
				"followed by \"--\" and a UUID. The value you passed couldn't be split\n" +
				"into those two parts because the \"--\" separator was missing.",
			Where: fmt.Sprintf("Validating the epoch id (internal/handlers/epoch.go in %s).", caller),
			Impact: "The epoch can't run. Without a properly-formatted ID, the audit\n" +
				"log can't link events back to any task, which would leave a broken trail.",
			Fix: "1. Create a task first to get a valid ID:\n" +
				"     pasture task create REQUEST --type=feature \"<title>\"\n" +
				"2. Or find one that already exists:\n" +
				"     pasture task list --status=open --type=feature\n" +
				"3. Pass the returned ID as --epoch-id when starting the epoch.",
			Cause: err,
		}
	}
	return nil
}

// requireEpochID returns a validation error when epochId is empty. action names
// the verb (e.g. "cancel the epoch") for an actionable message.
func requireEpochID(epochId, action, where, example string) error {
	if epochId != "" {
		return nil
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("An epoch ID is required to %s.", action),
		Why:      "The --epoch-id flag was not provided.",
		Where:    where,
		Impact:   "Without an epoch ID, there's no way to know which epoch to act on.",
		Fix:      "1. Pass the epoch's ID:\n     " + example,
	}
}

// EpochStart starts the durable control workflow for epochId. The epoch is
// identified by its id (the task already carries its description), and signals
// are addressed to it by that id.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func EpochStart(ctrl EpochController, epochId string, format types.OutputFormat) (int, error) {
	if err := requireEpochID(epochId, "start an epoch",
		"Starting the epoch (internal/handlers/epoch.go in handlers.EpochStart).",
		"pasture epoch start --epoch-id <id>"); err != nil {
		return pasterrors.ExitCode(err), err
	}
	if vErr := validateEpochID(epochId, "handlers.EpochStart"); vErr != nil {
		return pasterrors.ExitCode(vErr), vErr
	}

	if err := ctrl.StartEpoch(context.Background(), epochId); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatStartResult(epochId, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// EpochCancel requests cancellation of a running epoch. Both the cancel and
// terminate verbs route here: the durable substrate has a single stop path.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func EpochCancel(ctrl EpochController, epochId string, format types.OutputFormat) (int, error) {
	if err := requireEpochID(epochId, "cancel an epoch",
		"Cancelling the epoch (internal/handlers/epoch.go in handlers.EpochCancel).",
		"pasture epoch cancel --epoch-id <id>"); err != nil {
		return pasterrors.ExitCode(err), err
	}

	if err := ctrl.CancelEpoch(context.Background(), epochId); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

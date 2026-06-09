package handlers

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// PhaseAdvance sends a PhaseAdvanceSignal to the EpochWorkflow.
//
// toPhase must be a valid PhaseId. triggeredBy identifies the sender (e.g. a
// role name). condition describes the protocol condition that was satisfied.
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func PhaseAdvance(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochId string,
	toPhase protocol.PhaseId,
	triggeredBy, condition string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An epoch ID is required to advance the phase.",
			Why:      "The --epoch-id flag was not provided.",
			Where:    "Advancing the workflow phase (internal/handlers/phase.go in handlers.PhaseAdvance).",
			Impact:   "Without an epoch ID, there's no way to know which workflow to advance.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture-msg phase advance --epoch-id <id> --to <phase>\n" +
				"2. If you don't know the epoch ID, list active epochs:\n" +
				"     pasture-msg epoch list",
		}
		return pasterrors.ExitCode(err), err
	}
	if !toPhase.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised phase name.", toPhase),
			Why: "The target phase must be one of the 12 protocol phase names\n" +
				"(request, elicit, propose, ..., landing, complete) or the short\n" +
				"form p1..p12.",
			Where:  "Advancing the workflow phase (internal/handlers/phase.go in handlers.PhaseAdvance).",
			Impact: "The phase advance can't be sent because the target phase isn't recognised.",
			Fix: "1. Use a recognised phase name, for example:\n" +
				"     pasture-msg phase advance --to code-review --epoch-id <id>\n" +
				"2. Or use the short form:\n" +
				"     pasture-msg phase advance --to p10 --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	payload := protocol.PhaseAdvanceSignal{
		ToPhase:      toPhase,
		TriggeredBy:  triggeredBy,
		ConditionMet: condition,
	}

	if err := c.SignalWorkflow(ctx, epochId, "", string(protocol.SignalAdvancePhase), payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't send the phase-advance request to epoch %q.", epochId),
			Why:      "The workflow server rejected the advance signal.",
			Where:    "Advancing the workflow phase (internal/handlers/phase.go in handlers.PhaseAdvance).",
			Impact:   "The phase transition didn't start, so the workflow remains in its current phase.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, list active epochs to find the right ID:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry the phase advance once the epoch is healthy.",
				epochId),
			Cause: err,
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

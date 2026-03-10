package handlers

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/temporal"
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
	epochID string,
	toPhase protocol.PhaseId,
	triggeredBy, condition string,
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
			Impact:   "phase advance cannot be sent without an epoch ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if !toPhase.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a valid phase", toPhase),
			Why:      "PhaseId must be one of: request, elicit, propose, ..., landing, complete (or pX shorthand)",
			Impact:   "phase advance cannot be sent with an unknown target phase",
			Fix:      "use a valid phase name (e.g., request, elicit, code-review) or pX shorthand (p1..p12)",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	payload := types.PhaseAdvanceSignal{
		ToPhase:      toPhase,
		TriggeredBy:  triggeredBy,
		ConditionMet: condition,
	}

	if err := c.SignalWorkflow(ctx, epochID, "", temporal.SignalAdvancePhase, payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("phase advance signal failed for epoch %q", epochID),
			Why:      err.Error(),
			Impact:   "phase transition was not initiated",
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

package handlers

import (
	"context"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// PhaseAdvance delivers an advance-phase signal to the epoch's control workflow.
//
// toPhaseStr is the raw phase name or pX shorthand from the CLI flag; this
// handler parses and validates it so RunE can forward the string directly.
// triggeredBy identifies the sender (e.g. a role name); condition describes
// the protocol condition that was satisfied.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func PhaseAdvance(
	ctrl EpochController,
	epochId string,
	toPhaseStr protocol.PhaseId,
	triggeredBy, condition string,
	format types.OutputFormat,
) (int, error) {
	if err := requireEpochID(epochId, "advance the phase",
		"Advancing the phase (internal/handlers/phase.go in handlers.PhaseAdvance).",
		"pasture phase advance --epoch-id <id> --to <phase>"); err != nil {
		return pasterrors.ExitCode(err), err
	}

	toPhase, parseErr := protocol.ParsePhaseId(string(toPhaseStr))
	if parseErr != nil {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised phase name.", toPhaseStr),
			Why: "The target phase must be one of the 12 protocol phase names\n" +
				"(request, elicit, propose, ..., landing, complete) or the short\n" +
				"form p1..p12.",
			Where:  "Advancing the phase (internal/handlers/phase.go in handlers.PhaseAdvance).",
			Impact: "The phase advance can't be sent because the target phase isn't recognised.",
			Fix: "1. Use a recognised phase name, for example:\n" +
				"     pasture phase advance --to code-review --epoch-id <id>\n" +
				"2. Or use the short form:\n" +
				"     pasture phase advance --to p10 --epoch-id <id>",
			Cause: parseErr,
		}
		return pasterrors.ExitCode(err), err
	}

	sig := protocol.PhaseAdvanceSignal{
		ToPhase:      toPhase,
		TriggeredBy:  triggeredBy,
		ConditionMet: condition,
	}
	if err := ctrl.AdvancePhase(context.Background(), epochId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

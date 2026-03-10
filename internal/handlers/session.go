package handlers

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
)

// SessionRegister sends a RegisterSessionSignal to the EpochWorkflow.
//
// Registers a Claude Code session with the running epoch for observability
// and permission tracking. Duplicate session_id registrations are silently
// ignored by the workflow (idempotent).
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func SessionRegister(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID, sessionID, role, modelHarness, model string,
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
			Impact:   "session cannot be registered without an epoch ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if sessionID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "session-id is required",
			Why:      "--session-id flag was not provided",
			Impact:   "session cannot be registered without a session ID",
			Fix:      "provide --session-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if role == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "role is required",
			Why:      "--role flag was not provided",
			Impact:   "session cannot be registered without a role",
			Fix:      "provide --role <role> (e.g., worker, supervisor, reviewer)",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	payload := types.RegisterSessionSignal{
		EpochID:      epochID,
		SessionID:    sessionID,
		Role:         role,
		ModelHarness: modelHarness,
		Model:        model,
	}

	if err := c.SignalWorkflow(ctx, epochID, "", temporal.SignalRegisterSession, payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("session register signal failed for epoch %q", epochID),
			Why:      err.Error(),
			Impact:   "session was not registered with the epoch",
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

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
	epochId, sessionId, role, modelHarness, model string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An epoch ID is required to register a session.",
			Why:      "The --epoch-id flag was not provided.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.SessionRegister).",
			Impact:   "Without an epoch ID, the session can't be linked to a running epoch.",
			Fix: "1. Pass the epoch this session belongs to:\n" +
				"     pasture-msg session register --epoch-id <id> --session-id <id> --role <role>\n" +
				"2. If you don't know the epoch ID, list active epochs:\n" +
				"     pasture-msg epoch list",
		}
		return pasterrors.ExitCode(err), err
	}
	if sessionId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A session ID is required to register a session.",
			Why:      "The --session-id flag was not provided.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.SessionRegister).",
			Impact:   "Without a session ID, there's nothing to register.",
			Fix: "1. Pass the session ID supplied by Claude Code:\n" +
				"     pasture-msg session register --session-id <id> --epoch-id <id> --role <role>",
		}
		return pasterrors.ExitCode(err), err
	}
	if role == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A role is required to register a session.",
			Why:      "The --role flag was not provided.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.SessionRegister).",
			Impact:   "The session can't be tracked without knowing what role it's playing.",
			Fix: "1. Pass the role this session is playing (worker, supervisor, reviewer, ...):\n" +
				"     pasture-msg session register --role <role> --epoch-id <id> --session-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	payload := types.RegisterSessionSignal{
		EpochId:      epochId,
		SessionId:    sessionId,
		Role:         role,
		ModelHarness: modelHarness,
		Model:        model,
	}

	if err := c.SignalWorkflow(ctx, epochId, "", temporal.SignalRegisterSession, payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't register the session with epoch %q.", epochId),
			Why:      "The workflow server rejected the register-session signal.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.SessionRegister).",
			Impact:   "The session is not tracked by the epoch, so its events won't appear in the epoch's history.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, list active epochs to find the right ID:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry the register once the epoch is healthy.",
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

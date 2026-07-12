package handlers

import (
	"context"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ValidateSessionRegister checks the session register verb's arguments (epoch id,
// session id, role) without opening a controller or touching the database, so the
// CLI can reject a bad invocation before OpenEpochController runs.
func ValidateSessionRegister(epochId, sessionId, role string) error {
	if err := requireEpochID(epochId, "register a session",
		"Registering a session (internal/handlers/session.go in handlers.ValidateSessionRegister).",
		"pasture session register --epoch-id <id> --session-id <id> --role <role>"); err != nil {
		return err
	}
	if err := validateEpochID(epochId, "handlers.ValidateSessionRegister"); err != nil {
		return err
	}
	if sessionId == "" {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A session ID is required to register a session.",
			Why:      "The --session-id flag was not provided.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.ValidateSessionRegister).",
			Impact:   "Without a session ID, there's nothing to register.",
			Fix: "1. Pass the session ID supplied by Claude Code:\n" +
				"     pasture session register --session-id <id> --epoch-id <id> --role <role>",
		}
	}
	if role == "" {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A role is required to register a session.",
			Why:      "The --role flag was not provided.",
			Where:    "Registering a session (internal/handlers/session.go in handlers.ValidateSessionRegister).",
			Impact:   "The session can't be tracked without knowing what role it's playing.",
			Fix: "1. Pass the role this session is playing (worker, supervisor, reviewer, ...):\n" +
				"     pasture session register --role <role> --epoch-id <id> --session-id <id>",
		}
	}
	return nil
}

// SessionRegister delivers a register-session signal to the epoch's control
// workflow. Duplicate session_id registrations are ignored by the workflow
// (idempotent).
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func SessionRegister(
	ctrl EpochController,
	epochId, sessionId, role, modelHarness, model string,
	format types.OutputFormat,
) (int, error) {
	if err := ValidateSessionRegister(epochId, sessionId, role); err != nil {
		return pasterrors.ExitCode(err), err
	}

	sig := protocol.RegisterSessionSignal{
		EpochId:      epochId,
		SessionId:    sessionId,
		Role:         role,
		ModelHarness: modelHarness,
		Model:        model,
	}
	if err := ctrl.RegisterSession(context.Background(), epochId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

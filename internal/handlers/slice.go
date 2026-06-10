package handlers

import (
	"context"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SliceStart delivers a start-slice configuration signal to a slice sub-workflow
// (addressed by its slice workflow id).
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func SliceStart(
	ctrl EpochController,
	sliceId string,
	mode protocol.SliceExecutionMode,
	command string,
	timeoutSeconds int,
	format types.OutputFormat,
) (int, error) {
	if sliceId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A slice ID is required to configure a slice.",
			Why:      "The --slice-id flag was not provided.",
			Where:    "Configuring a slice (internal/handlers/slice.go in handlers.SliceStart).",
			Impact:   "Without a slice ID, there's no slice to configure.",
			Fix:      "1. Pass the slice's ID:\n     pasture slice start --slice-id <id> --mode mock",
		}
		return pasterrors.ExitCode(err), err
	}
	if !mode.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised slice execution mode.", mode),
			Why:      "A slice runs in one of three modes: mock, tmux, or subprocess.",
			Where:    "Configuring a slice (internal/handlers/slice.go in handlers.SliceStart).",
			Impact:   "The slice can't be configured with an unknown mode.",
			Fix: "1. Pass a recognised mode:\n" +
				"     pasture slice start --mode mock --slice-id <id>\n" +
				"     pasture slice start --mode subprocess --command \"<cmd>\" --slice-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	sig := protocol.SliceStartSignal{Mode: mode, Command: command, TimeoutSeconds: timeoutSeconds}
	if err := ctrl.StartSlice(context.Background(), sliceId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// SliceComplete delivers a complete-slice override signal to a slice sub-workflow.
//
// output and errMsg are mutually exclusive: set output for a successful override,
// errMsg for a failure override.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func SliceComplete(
	ctrl EpochController,
	sliceId string,
	output, errMsg *string,
	format types.OutputFormat,
) (int, error) {
	if sliceId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A slice ID is required to complete a slice.",
			Why:      "The --slice-id flag was not provided.",
			Where:    "Completing a slice (internal/handlers/slice.go in handlers.SliceComplete).",
			Impact:   "Without a slice ID, there's no slice to complete.",
			Fix:      "1. Pass the slice's ID:\n     pasture slice complete --slice-id <id> --output \"<result>\"",
		}
		return pasterrors.ExitCode(err), err
	}
	if output != nil && errMsg != nil {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pass either --output or --error, not both.",
			Why:      "A slice completion override is either a success (--output) or a failure (--error), not both.",
			Where:    "Completing a slice (internal/handlers/slice.go in handlers.SliceComplete).",
			Impact:   "The override can't be recorded because the result is ambiguous.",
			Fix: "1. Pick one and retry. For success:\n" +
				"     pasture slice complete --output \"<result>\" --slice-id <id>\n" +
				"2. For failure:\n" +
				"     pasture slice complete --error \"<reason>\" --slice-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	sig := protocol.SliceCompleteSignal{Success: errMsg == nil}
	if output != nil {
		sig.Output = *output
	}
	if errMsg != nil {
		sig.Error = errMsg
	}
	if err := ctrl.CompleteSlice(context.Background(), sliceId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

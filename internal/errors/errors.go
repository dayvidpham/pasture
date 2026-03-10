// Package errors provides structured, actionable error reporting for the pasture system.
//
// Every error carries a Category (connection, workflow, validation, config),
// a machine-readable What field, a human-readable Why/Impact/Fix triple, and
// implements the standard error interface so it can be used anywhere Go errors
// are expected. Use errors.As() to extract the full StructuredError from any
// wrapped error chain.
package errors

import (
	stderrors "errors"
	"fmt"
	"io"
)

// Category classifies the error domain and drives exit-code selection.
type Category string

const (
	// CategoryConnection indicates the daemon could not reach Temporal.
	CategoryConnection Category = "connection error"
	// CategoryWorkflow indicates a Temporal workflow or activity failure.
	CategoryWorkflow Category = "workflow error"
	// CategoryValidation indicates bad user input or missing required fields.
	CategoryValidation Category = "validation error"
	// CategoryConfig indicates a configuration file or environment variable problem.
	CategoryConfig Category = "config error"
)

// StructuredError implements the error interface with actionable diagnostic fields.
//
// Every field should be filled in so that the reader knows not just what broke,
// but why it broke and exactly how to fix it.
type StructuredError struct {
	// Category classifies the error domain (connection, workflow, validation, config).
	Category Category
	// What is a short, one-line description of what went wrong.
	What string
	// Why explains the underlying cause of the failure.
	Why string
	// Impact describes what the caller cannot do as a result of this error.
	Impact string
	// Fix provides concrete, actionable steps to resolve the error.
	Fix string
}

// Error implements the error interface.
// Returns "<category>: <what>" — suitable for log lines or wrapping with fmt.Errorf("%w").
func (e *StructuredError) Error() string {
	return fmt.Sprintf("%s: %s", e.Category, e.What)
}

// Report writes the full structured error to w, including all diagnostic fields.
//
// Output format:
//
//	<category>: <what>
//	  why: <why>
//	  impact: <impact>
//	  fix: <fix>
func (e *StructuredError) Report(w io.Writer) {
	fmt.Fprintf(w, "%s: %s\n", e.Category, e.What)
	fmt.Fprintf(w, "  why: %s\n", e.Why)
	fmt.Fprintf(w, "  impact: %s\n", e.Impact)
	fmt.Fprintf(w, "  fix: %s\n", e.Fix)
}

// ExitCode maps an error to a process exit code.
//
// Exit code mapping:
//   - CategoryValidation, CategoryConfig → 1
//   - CategoryConnection → 2
//   - CategoryWorkflow → 3
//   - any other error (or nil) → 1
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var se *StructuredError
	if stderrors.As(err, &se) {
		switch se.Category {
		case CategoryValidation, CategoryConfig:
			return 1
		case CategoryConnection:
			return 2
		case CategoryWorkflow:
			return 3
		}
	}
	return 1 // default for unknown error types
}

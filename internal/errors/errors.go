// Package errors provides structured, actionable error reporting for the pasture system.
//
// Every error carries a Category (connection, workflow, validation, config,
// storage), a short plain-language What field, a Why/Where/Impact/Fix tuple
// describing the cause/location/consequence/recovery, and implements the
// standard error interface so it can be used anywhere Go errors are
// expected. Use errors.As() to extract the full StructuredError from any
// wrapped error chain.
//
// User-facing output (Report and the Stringer) follows a plain-language
// convention: a top "Error:" line summarising the category in one short
// sentence (Category-derived header), then a vertically aligned block with
// full English labels (Problem / Reason / Where / Impact / How to fix). Body
// text must avoid project-internal jargon — translate code-level terms into
// ordinary English. The "How to fix" section is numbered when there are
// multiple alternatives, with concrete shell commands on indented lines.
//
// Top-line headers per Category (see categoryHeader):
//
//	CategoryConnection  → "Error: Couldn't connect."
//	CategoryWorkflow    → "Error: A workflow step failed."
//	CategoryValidation  → "Error: The input wasn't valid."
//	CategoryConfig      → "Error: There's a problem with the configuration."
//	CategoryStorage     → "Error: A storage operation failed."
//	(unrecognised)      → "Error: Something went wrong."
//
// The Problem: line then carries the SPECIFIC What value, so the headline
// and Problem elaborate rather than duplicate.
package errors

import (
	stderrors "errors"
	"fmt"
	"io"
	"strings"
)

// Category classifies the error domain and drives exit-code selection.
//
// The category is intentionally NOT shown verbatim in the user-visible
// "Error:" line — it remains available programmatically (via the Category
// field and via ExitCode) for log lines, exit-code mapping, and forensic
// inspection. The user-visible header is derived from the category via
// categoryHeader so the top line is plain English, not the raw enum string.
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
	// CategoryStorage indicates a persistence-layer failure: SQLite open
	// errors, schema migration failures, or schema-version mismatches between
	// the binary and the on-disk database. See PROPOSAL-2 §7.10.5 for rationale.
	CategoryStorage Category = "storage error"
)

// categoryHeader maps a Category to the plain-language top "Error:" line.
//
// The header is intentionally generic so the specific What can elaborate
// underneath in the Problem: line without duplicating the headline. If the
// category is not recognised, the caller falls back to a generic
// "Error: Something went wrong." line — never the raw enum string, which
// would leak internal jargon into user-visible output.
var categoryHeader = map[Category]string{
	CategoryConnection: "Couldn't connect.",
	CategoryWorkflow:   "A workflow step failed.",
	CategoryValidation: "The input wasn't valid.",
	CategoryConfig:     "There's a problem with the configuration.",
	CategoryStorage:    "A storage operation failed.",
}

// headerFor returns the plain-language top-line text for a category. Falls
// back to "Something went wrong." for unrecognised categories rather than
// surfacing the raw Category enum string (which is project-internal jargon).
func headerFor(cat Category) string {
	if h, ok := categoryHeader[cat]; ok {
		return h
	}
	return "Something went wrong."
}

// StructuredError implements the error interface with actionable diagnostic fields.
//
// All narrative fields (What, Why, Where, Impact, Fix) should be filled in
// so the reader can understand both the cause and the recovery without
// reading source code. Where is optional — when empty the line is suppressed
// in user-visible output. See package docs for the plain-language
// conventions all callers must follow.
type StructuredError struct {
	// Category classifies the error domain (connection, workflow,
	// validation, config, storage). Drives exit-code selection and the
	// top-line header (via categoryHeader). The raw enum string is NOT
	// surfaced in user-visible output.
	Category Category
	// What is one short plain-English sentence summarising what went wrong
	// SPECIFICALLY (the value the user passed, the file that failed, etc.).
	// Surfaced as the Problem: line. Avoid type names, SQL columns,
	// and protocol references.
	What string
	// Why explains the underlying cause in plain English. Translate
	// technical roots ("ParseTaskId returned ErrInvalidFormat" → "the ID
	// didn't have the required separator") so a non-specialist can act on
	// it. Surfaced as the Reason: line.
	Why string
	// Where describes what the user (or system) was doing when the error
	// occurred, with the code location parenthetically. Example:
	//   "Starting the epoch (handlers/epoch.go in handlers.EpochStart)"
	// Optional — when empty the Where: line is suppressed in user-visible
	// output. See package docs.
	Where string
	// Impact describes the consequence to the caller in plain English.
	// "The workflow can't start," not "the workflow boundary cannot
	// satisfy R5/D5 alignment."
	Impact string
	// Fix provides concrete recovery steps. When multiple alternatives
	// exist, format as numbered items joined with "\n" — see FixStep
	// helpers below for the canonical shape. Each step starts with a
	// plain-English sentence followed by an indented shell command.
	Fix string
	// Cause optionally wraps the underlying error for log-debugging and for
	// errors.Is / errors.As traversal. It is NOT surfaced in user-visible
	// output (Report) — that prose must be plain English with no Go symbol
	// names, package qualifiers, or SQL column references. Set Cause when a
	// translated Why field would otherwise lose the underlying error
	// information that operators need in logs.
	Cause error
}

// Unwrap returns the optional underlying cause so errors.Is / errors.As can
// traverse the chain without exposing the raw cause to user-visible output.
func (e *StructuredError) Unwrap() error {
	return e.Cause
}

// Error implements the error interface.
//
// Returns "<category>: <what>" — suitable for log lines or wrapping with
// fmt.Errorf("%w"). User-facing output should use Report or the package's
// Print helpers (which emit the full plain-language block).
func (e *StructuredError) Error() string {
	return fmt.Sprintf("%s: %s", e.Category, e.What)
}

// Report writes the full plain-language error block to w.
//
// Output format (vertically aligned for visual parseability):
//
//	Error: <category-derived plain-language header>
//
//	  Problem:    <what — specific to this occurrence>
//	  Reason:     <why>
//	  Where:      <what was happening (file:line in code parenthetically)>
//	  Impact:     <impact>
//	  How to fix:
//	    <fix body — already includes numbered steps and indented commands>
//
// The top "Error:" line is derived from Category via categoryHeader so it
// is a generic plain-English summary; the specific situation appears in the
// Problem: line. This avoids the headline-duplicates-Problem anti-pattern.
//
// The Where: line is suppressed entirely when StructuredError.Where is empty
// — better than printing an empty label.
//
// Multi-line What/Why/Where/Impact/Fix values are wrapped to align under
// the label column so the whole block stays scannable.
func (e *StructuredError) Report(w io.Writer) {
	const labelWidth = 12 // "How to fix:" + space, padded for alignment

	fmt.Fprintf(w, "Error: %s\n\n", headerFor(e.Category))

	writeAligned(w, "Problem:", labelWidth, e.What)
	writeAligned(w, "Reason:", labelWidth, e.Why)
	writeAligned(w, "Where:", labelWidth, e.Where)
	writeAligned(w, "Impact:", labelWidth, e.Impact)

	// "How to fix" is a label on its own line; the Fix body follows
	// indented underneath so multi-step instructions remain readable.
	if e.Fix != "" {
		fmt.Fprintf(w, "  %s\n", "How to fix:")
		writeFixBody(w, e.Fix)
	}
}

// writeAligned emits "  <label><padding><value>" with continuation lines
// indented under the value column so multi-line values stay readable.
//
// When value is the empty string the entire line (label and all) is
// suppressed — callers rely on this for the optional Where: field.
func writeAligned(w io.Writer, label string, labelWidth int, value string) {
	if value == "" {
		return
	}
	pad := labelWidth - len(label)
	if pad < 1 {
		pad = 1
	}
	indent := strings.Repeat(" ", 2+labelWidth+1) // "  " + label-column + 1 separator space
	lines := strings.Split(value, "\n")
	fmt.Fprintf(w, "  %s%s%s\n", label, strings.Repeat(" ", pad), lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

// writeFixBody emits the Fix value indented under the "How to fix:" label.
// The Fix string is written verbatim line-by-line; callers are responsible
// for embedding the numbered-step shape (e.g. "1. Step\n     command").
func writeFixBody(w io.Writer, fix string) {
	if fix == "" {
		return
	}
	for _, line := range strings.Split(fix, "\n") {
		if line == "" {
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "    %s\n", line)
	}
}

// ExitCode maps an error to a process exit code.
//
// Exit code mapping:
//   - CategoryValidation → 1
//   - CategoryConnection → 2
//   - CategoryWorkflow → 3
//   - CategoryConfig → 4
//   - CategoryStorage → 5
//   - any other error (or nil) → 1
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var se *StructuredError
	if stderrors.As(err, &se) {
		switch se.Category {
		case CategoryValidation:
			return 1
		case CategoryConnection:
			return 2
		case CategoryWorkflow:
			return 3
		case CategoryConfig:
			return 4
		case CategoryStorage:
			return 5
		}
	}
	return 1 // default for unknown error types
}

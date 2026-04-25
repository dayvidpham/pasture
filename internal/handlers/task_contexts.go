// Package handlers — task_contexts.go
//
// Handler for `pasture task contexts <event-id>` (PROPOSAL-2 §7.9).
//
// Surface:
//
//	pasture task contexts <event-id> [--format json|text]
//
// Returns the (Kind, ContextID) edges attached to the given audit_events row.
// Routes through TaskTracker.EventContexts, which reads from context_edges
// and is BCNF-keyed on (event_id, context_kind, context_id).
//
// event-id is the SQLite AUTOINCREMENT id from audit_events. We accept it as
// a positive int64 string (no leading zeros enforced — we just parse with
// strconv.ParseInt). Negative or zero values are rejected with an actionable
// error since audit_events.id starts at 1.
package handlers

import (
	"context"
	"fmt"
	"io"
	"strconv"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// TaskContexts looks up the context_edges attached to one audit event and
// prints them.
func TaskContexts(w io.Writer, dbPath, eventIDStr string, format types.OutputFormat) (int, error) {
	eventID, err := parseEventID(eventIDStr)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tracker.Close()

	ctx := context.Background()
	contexts, err := tracker.EventContexts(ctx, eventID)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fErr := formatters.FormatContextList(contexts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// parseEventID converts the user's positional event-id argument to an int64.
// audit_events.id is AUTOINCREMENT and starts at 1, so anything <= 0 is
// rejected with a CategoryValidation error.
func parseEventID(raw string) (int64, error) {
	if raw == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "pasture task contexts: missing event ID",
			Why:      "the positional event-id argument was empty",
			Impact:   "the context-edge query cannot be issued without a target event",
			Fix:      "pass the integer audit_events.id as the first positional argument: pasture task contexts <integer-id>",
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("pasture task contexts: cannot parse event ID %q", raw),
			Why:      err.Error(),
			Impact:   "the context-edge query cannot be issued because the event-id is not a valid integer",
			Fix:      "pass the integer audit_events.id (a positive integer); use 'pasture task events' to discover event ids",
		}
	}
	if n <= 0 {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("pasture task contexts: event ID %d is not positive", n),
			Why:      "audit_events.id is AUTOINCREMENT and starts at 1; zero and negative ids are unreachable",
			Impact:   "the context-edge query cannot be issued because no row would ever match",
			Fix:      "pass a positive integer; use 'pasture task events' to discover valid event ids",
		}
	}
	return n, nil
}

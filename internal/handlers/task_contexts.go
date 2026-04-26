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
			What:     "An event ID is required to look up its context links.",
			Why:      "No event ID was passed as the first positional argument.",
			Impact:   "The contexts query can't be issued without knowing which event to look up.",
			Fix: "1. Pass the integer event ID as the first positional argument:\n" +
				"     pasture task contexts <event-id>\n" +
				"2. To find event IDs, list recent events first:\n" +
				"     pasture task events",
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a valid event ID — event IDs are positive whole numbers.", raw),
			Why:      fmt.Sprintf("The value couldn't be parsed as an integer: %s", err),
			Impact:   "The contexts query can't be issued because the event ID isn't a number.",
			Fix: "1. Pass a positive whole number as the event ID:\n" +
				"     pasture task contexts <event-id>\n" +
				"2. To find valid event IDs, list recent events first:\n" +
				"     pasture task events",
		}
	}
	if n <= 0 {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Event ID %d isn't valid — event IDs start at 1.", n),
			Why:      "Zero and negative numbers are never assigned to events, so no event would ever match.",
			Impact:   "The contexts query can't be issued because no event has the ID you provided.",
			Fix: "1. Pass a positive whole number as the event ID:\n" +
				"     pasture task contexts <event-id>\n" +
				"2. To find valid event IDs, list recent events first:\n" +
				"     pasture task events",
		}
	}
	return n, nil
}

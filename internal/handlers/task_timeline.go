// Package handlers — task_timeline.go
//
// Handler for `pasture task timeline <task-id>` (PROPOSAL-2 §7.9).
//
// Surface:
//
//	pasture task timeline <task-id> [--include-children] [--depth N]
//	                                [--format json|text]
//
// Returns all events tied to the given task ID, in chronological order. The
// task ID is interpreted as an EpochContext (the originating REQUEST or
// SLICE task ID); behind the scenes the handler calls
// TaskTracker.Timeline(ctx, ContextEpoch, taskID) AND
// TaskTracker.QueryEvents(ctx, taskID, nil, nil) and merges the result so
// the timeline works against legacy-v1 databases too (where epoch attribution
// lives in the audit_events.epoch_id column rather than context_edges).
//
// --include-children and --depth are accepted by the CLI but currently no-op:
// child-task timeline traversal requires walking the Provenance task graph,
// which depends on S8 wiring epoch contexts onto child SLICE tasks. This
// slice (S6) ships the read-only single-task path; child traversal is a
// FOLLOWUP_SLICE candidate when S8 lands.
package handlers

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TaskTimelineInput captures the CLI inputs for `pasture task timeline`.
type TaskTimelineInput struct {
	DBPath          string
	TaskIDStr       string // wire-format task ID (e.g. "aura-plugins--01968a3c-9d4f-7c8a-bc12-feedfacecafe")
	IncludeChildren bool   // accepted but currently no-op (see file-level doc)
	Depth           int    // accepted but currently no-op
}

// TaskTimeline queries events for a task ID and prints them.
func TaskTimeline(w io.Writer, in TaskTimelineInput, format types.OutputFormat) (int, error) {
	if in.TaskIDStr == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A task ID is required to show a timeline.",
			Why:      "No task ID was passed as the first positional argument.",
			Where:    "Showing a timeline (internal/handlers/task_timeline.go in handlers.TaskTimeline).",
			Impact:   "The timeline can't be looked up without knowing which task to query.",
			Fix: "1. Pass the task ID as the first positional argument:\n" +
				"     pasture task timeline <task-id>\n" +
				"2. To find a task ID, list tasks first:\n" +
				"     pasture task list",
		}
		return pasterrors.ExitCode(se), se
	}

	// We intentionally do NOT validate via provenance.ParseTaskID here —
	// PROPOSAL-2 §7.9 documents the timeline command as accepting any
	// "task-id" string, and free-floating contexts (Git SHAs, skill run IDs)
	// are routed through `pasture task events --context-kind=...` instead.
	// Strict task-ID validation lives at the events ingest path (S8 §7.12),
	// not the read path. We accept any non-empty string.

	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tracker.Close()

	ctx := context.Background()

	// Path 1: Timeline via context_edges (post-S4 source of truth).
	contextEvents, err := tracker.Timeline(ctx, protocol.ContextEpoch, in.TaskIDStr)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Path 2: Legacy QueryEvents via the v1 epoch_id column. Until S4 lands
	// the epoch attribution still lives in audit_events.epoch_id; this path
	// covers events written by old code OR v1/v2 fixtures. Once S4 backfills
	// every epoch_id row into context_edges, these two queries return the
	// same set and the merge becomes a deduplication. We do that anyway
	// (events from path 1 may overlap with path 2) so the migration boundary
	// is invisible to the caller.
	legacyEvents, err := tracker.QueryEvents(ctx, in.TaskIDStr, nil, nil)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Merge + chronological sort. Because protocol.AuditEvent has no stable
	// row-identity field (id is private to audit_events), we deduplicate on
	// the (Timestamp, EventType, Payload-string) tuple — collisions between
	// the two paths produce byte-identical events, so the duplicate test is
	// safe.
	merged := mergeAuditEvents(contextEvents, legacyEvents)

	// Try to look up the task itself for a one-line header in text mode. We
	// silently ignore lookup failures — the timeline is still useful for
	// non-task contexts (e.g. legacy free strings used as epoch_id).
	if t, terr := lookupTaskForHeader(tracker, in.TaskIDStr); terr == nil && format == types.OutputText {
		// Pre-pend a one-line task header so the user knows which task they
		// are looking at without grep'ing the events.
		fmt.Fprintf(w, "Task: %s — %s\n", t.ID.String(), t.Title)
	}

	out, fErr := formatters.FormatAuditEvents(merged, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// lookupTaskForHeader resolves the task ID to a Task so the text-mode output
// can prepend a "Task: <id> — <title>" header. Returns the parse/lookup
// error unchanged so callers can decide to ignore (timeline tolerates
// non-task IDs) or surface it.
func lookupTaskForHeader(tracker protocol.TaskTracker, raw string) (provenance.Task, error) {
	id, err := provenance.ParseTaskID(raw)
	if err != nil {
		return provenance.Task{}, err
	}
	return tracker.Show(id)
}

// mergeAuditEvents combines two event slices, deduplicates on a stable key,
// and sorts chronologically (timestamp ASC). Used by the timeline path to
// fold the new context_edges JOIN result with the legacy QueryEvents result
// during the v1 → v4 transition.
func mergeAuditEvents(a, b []protocol.AuditEvent) []protocol.AuditEvent {
	seen := make(map[string]struct{}, len(a)+len(b))
	merged := make([]protocol.AuditEvent, 0, len(a)+len(b))
	for _, e := range a {
		k := dedupKey(e)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, e)
	}
	for _, e := range b {
		k := dedupKey(e)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, e)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Timestamp.Before(merged[j].Timestamp)
	})
	return merged
}

// dedupKey returns a stable string identifying an AuditEvent for the merge
// path. We compose from the columns that uniquely identify a row in
// audit_events under the v1 schema (epoch + event_type + timestamp); the
// payload is folded in via fmt.Sprintf with %v — collisions on (epoch,
// event_type, timestamp) with different payloads would mean two distinct
// writes at the same nanosecond, which is improbable in practice and would
// only surface as a benign single-row drop in the timeline.
func dedupKey(e protocol.AuditEvent) string {
	return fmt.Sprintf("%s|%s|%s|%d|%v", e.EpochID, e.Phase, e.EventType, e.Timestamp.UnixNano(), e.Payload)
}

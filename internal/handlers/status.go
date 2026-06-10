package handlers

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// recentEventLimit is the number of audit events surfaced in the status view.
// Large enough to show interesting recent history; small enough to stay readable.
const recentEventLimit = 10

// EpochStatusInput captures the inputs for `pasture status`.
type EpochStatusInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// EpochId is the epoch to inspect. When empty, all known epochs are listed
	// instead of a single-epoch detail view.
	EpochId string
}

// EpochStatus reads the EpochState projection (a pure SQL read — no durable
// workflow round-trip) and the recent audit events for an epoch, then renders
// the status view.
//
// When EpochId is empty, all epochs recorded in the projection table are
// listed. When EpochId is set, the handler renders the full detail view for
// that epoch, including current phase, available transitions (recomputed from
// the FSM), slice progress, active sessions, and the N most recent audit events
// (flagging any EpochCancelled event with its operator reason).
//
// The function is a pure read: it never starts a durable workflow.
//
// Exit codes: 0=success, 1=validation, 3=workflow/not-found, 5=storage.
func EpochStatus(in EpochStatusInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer db.Close()

	if in.EpochId == "" {
		return listEpochs(db, dbPath, format)
	}
	return showEpoch(db, dbPath, in.EpochId, format)
}

// listEpochs reads all rows from the projection table and renders the epoch
// listing. If the projection table does not yet exist (no epoch has ever run),
// the listing is empty — not an error.
func listEpochs(db *sql.DB, dbPath string, format types.OutputFormat) (int, error) {
	// Probe for the projection table. If absent the db is fresh; return an
	// informative empty listing rather than a raw missing-table error.
	var tableName string
	switch err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, projectionTable,
	).Scan(&tableName); {
	case err == sql.ErrNoRows:
		out, fmtErr := formatters.FormatEpochList(nil, format)
		if fmtErr != nil {
			return pasterrors.ExitCode(fmtErr), fmtErr
		}
		fmt.Println(out)
		return 0, nil
	case err != nil:
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't check whether the epoch-state projection table exists.",
			Why:      "The database refused the read from sqlite_master.",
			Where:    "Listing epochs (internal/handlers/status.go in handlers.listEpochs).",
			Impact:   "The epoch list can't be shown.",
			Fix:      "Confirm the database file is readable, then retry.",
			Cause:    err,
		}
		return pasterrors.ExitCode(se), se
	}

	// Read every row from the projection. Each row carries epoch_id and
	// current_phase.
	rows, err := db.Query(
		`SELECT epoch_id, current_phase FROM epoch_state_projection ORDER BY rowid ASC`,
	)
	if err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't list epochs from the projection table.",
			Why:      "The database refused the SELECT on epoch_state_projection.",
			Where:    "Listing epochs (internal/handlers/status.go in handlers.listEpochs).",
			Impact:   "The epoch list can't be shown.",
			Fix:      "Confirm the database file is readable, then retry.",
			Cause:    err,
		}
		return pasterrors.ExitCode(se), se
	}
	defer rows.Close()

	var summaries []formatters.EpochSummary
	for rows.Next() {
		var epochId, currentPhase string
		if err := rows.Scan(&epochId, &currentPhase); err != nil {
			se := &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Couldn't read an epoch row from the projection table.",
				Why:      "A row scan failed while iterating epoch_state_projection.",
				Where:    "Listing epochs (internal/handlers/status.go in handlers.listEpochs).",
				Impact:   "The epoch list may be incomplete.",
				Fix:      "Confirm the database file is not corrupted, then retry.",
				Cause:    err,
			}
			return pasterrors.ExitCode(se), se
		}
		summaries = append(summaries, formatters.EpochSummary{
			EpochId:      epochId,
			CurrentPhase: protocol.PhaseId(currentPhase),
		})
	}
	if err := rows.Err(); err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't finish reading epochs from the projection table.",
			Why:      "Iterating epoch_state_projection returned an error after some rows.",
			Where:    "Listing epochs (internal/handlers/status.go in handlers.listEpochs).",
			Impact:   "The epoch list may be incomplete.",
			Fix:      "Confirm the database file is not corrupted, then retry.",
			Cause:    err,
		}
		return pasterrors.ExitCode(se), se
	}

	// Enrich with event counts via the task tracker (which handles the post-v4
	// context_edges join for epoch-to-event linkage). Best-effort: any failure
	// leaves EventCount=0 rather than aborting the listing.
	if len(summaries) > 0 {
		tracker, tErr := tasks.OpenTaskTracker(dbPath)
		if tErr == nil {
			defer tracker.Close()
			ctx := context.Background()
			for i, s := range summaries {
				evts, qErr := tracker.QueryEvents(ctx, s.EpochId, nil, nil)
				if qErr == nil {
					summaries[i].EventCount = len(evts)
				}
			}
		}
		// If tracker failed to open (fresh db, not yet migrated), silently leave
		// counts as zero — the listing is still useful without event counts.
	}

	out, fmtErr := formatters.FormatEpochList(summaries, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// showEpoch reads the full detail view for one epoch.
func showEpoch(db *sql.DB, dbPath, epochId string, format types.OutputFormat) (int, error) {
	reader := dbProjectionReader{db: db}
	state, err := reader.ReadProjection(epochId)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if state == nil {
		e := epochNotFoundError(epochId, "handlers.showEpoch")
		return pasterrors.ExitCode(e), e
	}

	// Recompute available transitions via the FSM (not stored, always current).
	sm := protocol.NewEpochStateMachineFromState(state, nil)

	// Fetch recent audit events via the task tracker, which correctly handles
	// the post-v4 context_edges JOIN for epoch-to-event linkage.
	events, err := recentEventsForEpoch(dbPath, epochId)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Truncate to the most recent N (QueryEvents returns oldest-first).
	if len(events) > recentEventLimit {
		events = events[len(events)-recentEventLimit:]
	}

	// Scan for a cancel reason so the status view can flag a terminated epoch.
	cancelReason := findCancelReason(events)

	result := formatters.EpochStatusResult{
		EpochId:              state.EpochId,
		CurrentPhase:         state.CurrentPhase,
		CurrentRole:          state.CurrentRole,
		AvailableTransitions: sm.AvailableTransitions(),
		TransitionHistory:    state.TransitionHistory,
		SliceProgress:        state.SliceProgress,
		ActiveSessions:       state.ActiveSessions,
		RecentEvents:         events,
		CancelReason:         cancelReason,
	}

	out, fmtErr := formatters.FormatEpochStatus(result, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// recentEventsForEpoch opens a task tracker (which handles schema migrations +
// the post-v4 context_edges epoch join) and returns the N most recent audit
// events for the epoch, oldest-first.
//
// Returns an empty slice (not an error) when no events exist or when the
// tracker can't open (fresh database).
func recentEventsForEpoch(dbPath, epochId string) ([]protocol.AuditEvent, error) {
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		// Fresh database or pre-migration: no events to show. Return empty, not
		// an error, because the projection may exist while the audit schema is
		// still at v0 (the migrator runs at open, so a successful open implies
		// the schema is current — if it fails, there are no events).
		return nil, nil
	}
	defer tracker.Close()

	events, qErr := tracker.QueryEvents(context.Background(), epochId, nil, nil)
	if qErr != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't read audit events for epoch %q.", epochId),
			Why:      "The audit trail query returned an error.",
			Where:    "Reading recent audit events (internal/handlers/status.go in handlers.recentEventsForEpoch).",
			Impact:   "The recent-events section of the status view will be empty.",
			Fix:      "1. Confirm the database file is readable.\n2. Run 'pasture migrate' if the database schema may be out of date.",
			Cause:    qErr,
		}
	}
	return events, nil
}

// findCancelReason scans events for an EpochCancelled record. Returns a pointer
// to the reason string when found, nil when no cancel event is present.
// Events are searched newest-first (events slice is oldest-first).
func findCancelReason(events []protocol.AuditEvent) *string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType == protocol.EventEpochCancelled {
			var reason string
			if r, ok := events[i].Payload["reason"].(string); ok {
				reason = r
			}
			return &reason
		}
	}
	return nil
}

package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"

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
// The function is a pure read: it never starts a durable workflow, creates
// files, or modifies the database.
//
// Exit codes: 0=success, 2=connection (open failed), 3=workflow/not-found, 5=storage.
func EpochStatus(in EpochStatusInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	// Check file existence before opening. SQLite with a standard DSN creates
	// the file automatically; the read-only path below avoids that, but an
	// explicit existence check makes the error message exact and actionable.
	exists, statErr := fileExistsAt(dbPath)
	if statErr != nil {
		// os.Stat failed for a reason other than not-exist (e.g. permission
		// denied). Report the actual OS error rather than claiming the file is
		// absent — the fix steps are different.
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't check whether the database exists at %q.", dbPath),
			Why:      "An unexpected filesystem error occurred before the database could be opened.",
			Where:    "Checking database existence (internal/handlers/status.go in handlers.EpochStatus).",
			Impact:   "No epoch state or audit history can be read until the path is accessible.",
			Fix: fmt.Sprintf("1. Confirm the path is accessible and its parent directory is readable:\n"+
				"     ls -ld %q\n"+
				"2. Check and correct any permission problems, then retry.\n"+
				"   Expected location: %q", dbPath, dbPath),
			Cause: statErr,
		}
		return pasterrors.ExitCode(e), e
	}
	if !exists {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("No pasture database found at %q.", dbPath),
			Why:      "The database file has not been created yet.",
			Where:    "Opening the database for status (internal/handlers/status.go in handlers.EpochStatus).",
			Impact:   "No epoch state or audit history can be read — no epochs have run, or the daemon has not started yet.",
			Fix: fmt.Sprintf("1. Start the daemon to create and initialize the database:\n"+
				"     pastured\n"+
				"2. Then run an epoch:\n"+
				"     pasture epoch start --epoch-id <id>\n"+
				"3. To use a different database path, pass --db or set PASTURE_DB_PATH.\n"+
				"   Expected location: %q", dbPath),
		}
		return pasterrors.ExitCode(e), e
	}

	// Open the database read-only. Status never migrates the schema: if the
	// schema is mismatched the error directs the operator to upgrade or
	// downgrade. The read-only DSN prevents any unintended writes.
	db, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer db.Close()

	// Hoist the schema-version check so it fires on EVERY code path — list
	// with 0 epochs, unknown-epoch detail, and populated detail. A mismatched
	// database must never silently return an empty listing or a misleading
	// "epoch not found" error when the real problem is version skew.
	// CheckSchemaVersion is SELECT-only and safe to call on the read-only handle.
	if verErr := tasks.CheckSchemaVersion(db, dbPath); verErr != nil {
		return pasterrors.ExitCode(verErr), verErr
	}

	if in.EpochId == "" {
		return listEpochs(db, format)
	}
	return showEpoch(db, dbPath, in.EpochId, format)
}

// listEpochs reads all rows from the projection table and renders the epoch
// listing. If the projection table does not yet exist (no epoch has ever run),
// the listing is empty — not an error.
func listEpochs(db *sql.DB, format types.OutputFormat) (int, error) {
	// Probe for the projection table using the shared helper. If absent the db
	// is fresh; return an informative empty listing rather than a raw
	// missing-table error.
	exists, probeErr := projectionTableExists(db)
	if probeErr != nil {
		return pasterrors.ExitCode(probeErr), probeErr
	}
	if !exists {
		out, fmtErr := formatters.FormatEpochList(nil, format)
		if fmtErr != nil {
			return pasterrors.ExitCode(fmtErr), fmtErr
		}
		fmt.Println(out)
		return 0, nil
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

	// Enrich with event counts via a single grouped COUNT query on the
	// already-open read-only handle. Best-effort: any failure emits a warning
	// to stderr and leaves EventCount=0 rather than aborting the listing. The
	// schema version was already verified in EpochStatus; no second open needed.
	if len(summaries) > 0 {
		reader := tasks.NewStatusReaderFromDB(db)
		counts, cErr := reader.CountEventsByEpoch(context.Background())
		if cErr != nil {
			fmt.Fprintf(os.Stderr, "warning: couldn't read event counts: %v\n", cErr)
		} else {
			for i, s := range summaries {
				summaries[i].EventCount = counts[s.EpochId]
			}
		}
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

	// Fetch recent audit events using the already-open read-only handle (schema
	// version was verified in EpochStatus; no second open needed).
	events, evErr := recentEventsForEpochFromDB(db, dbPath, epochId)
	if evErr != nil {
		return pasterrors.ExitCode(evErr), evErr
	}

	// Scan the FULL event list for a cancel reason BEFORE truncating for
	// display. An EpochCancelled event pushed outside the display window by
	// subsequent events must still be surfaced.
	cancelReason := findCancelReason(events)

	// Truncate to the most recent N for display (QueryEvents returns oldest-first).
	if len(events) > recentEventLimit {
		events = events[len(events)-recentEventLimit:]
	}

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

// recentEventsForEpochFromDB returns all audit events for the epoch using an
// already-open read-only handle, oldest-first. The caller truncates the list
// for display after scanning for cancel reasons.
//
// The schema version has already been verified by EpochStatus before this
// function is reached; no second open or version check is performed here.
func recentEventsForEpochFromDB(db *sql.DB, dbPath, epochId string) ([]protocol.AuditEvent, error) {
	reader := tasks.NewStatusReaderFromDB(db)
	events, qErr := reader.QueryEvents(context.Background(), epochId, nil, nil)
	if qErr != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't read audit events for epoch %q.", epochId),
			Why:      "The audit trail query returned an error.",
			Where:    "Reading recent audit events (internal/handlers/status.go in handlers.recentEventsForEpochFromDB).",
			Impact:   "The recent-events section of the status view will be empty.",
			Fix: "1. Confirm the database file is readable:\n" +
				"     ls -l " + dbPath + "\n" +
				"2. Run 'pasture migrate' if the database schema may be out of date.\n" +
				"3. Retry the status command.",
			Cause: qErr,
		}
	}
	return events, nil
}

// fileExistsAt reports whether anything exists at path (file or directory); the
// caller gets a precise error from the subsequent open if the entry is not a
// usable database file.
//
// Returns (true, nil) when os.Stat succeeds.
// Returns (false, nil) when the path genuinely does not exist (ErrNotExist).
// Returns (false, err) for any other stat failure (permission denied, I/O) so
// the caller can surface the actual OS error rather than claiming the file is
// "not created yet".
func fileExistsAt(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
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

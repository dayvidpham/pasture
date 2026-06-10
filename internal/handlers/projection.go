package handlers

import (
	"database/sql"
	"fmt"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// projectionTable is the name of the table the engine writes the EpochState
// projection into. Read paths probe for it so a database on which no epoch has
// ever run (the projection table absent) reads as "no such epoch" rather than a
// raw missing-table error.
const projectionTable = "epoch_state_projection"

// ProjectionReader reads the persisted EpochState projection for an epoch.
//
// Queries answer from this projection (a SQL read) plus an FSM recompute of the
// available transitions — never a durable-workflow round-trip. *engine.Engine
// satisfies this directly; the lightweight DB-backed reader below satisfies it
// without spinning a DBOS context, which is all a read needs.
type ProjectionReader interface {
	// ReadProjection returns the projected EpochState for epochId, or
	// (nil, nil) when the epoch has not advanced (so callers can distinguish a
	// missing epoch from a read error).
	ReadProjection(epochId string) (*protocol.EpochState, error)
}

// dbProjectionReader is a ProjectionReader backed only by the shared SQLite
// handle. A query is a pure read, so it skips the DBOS lifecycle the durable
// engine needs for running workflows.
type dbProjectionReader struct{ db *sql.DB }

func (r dbProjectionReader) ReadProjection(epochId string) (*protocol.EpochState, error) {
	exists, err := projectionTableExists(r.db)
	if err != nil {
		return nil, err
	}
	if !exists {
		// No epoch has ever advanced on this database, so there is no projection
		// to read. Report "unknown epoch" (nil), not a missing-table error.
		return nil, nil
	}
	return engine.ReadProjection(r.db, epochId)
}

// QueryEpochInput captures the inputs for the projection-backed CLI query verbs.
type QueryEpochInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// EpochId identifies the epoch whose state is read.
	EpochId string
	// Query selects which slice of the state to render.
	Query protocol.QueryName
}

// QueryEpoch opens the shared handle at the resolved path and answers a
// projection-backed query. A query is a pure read, so it opens the file
// directly rather than launching a durable context (which would run a recovery
// sweep). Empty DBPath → tasks.DefaultDBPath().
//
// Exit codes: 0=success, 1=validation, 2=connection (open failed), 3=not found.
func QueryEpoch(in QueryEpochInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer db.Close()
	return QueryEpochState(NewDBProjectionReader(db), in.EpochId, in.Query, format)
}

// NewDBProjectionReader returns a ProjectionReader over an already-open shared
// handle. The caller owns the handle's lifecycle.
func NewDBProjectionReader(db *sql.DB) ProjectionReader { return dbProjectionReader{db: db} }

// QueryStateFromProjection builds the serialization-safe QueryStateResult from a
// projected EpochState, recomputing AvailableTransitions from the current
// vote/blocker state via the FSM. The projection stores raw state; the derived
// transition view is recomputed here so it always reflects the canonical gate
// rules rather than a value frozen at write time.
func QueryStateFromProjection(state *protocol.EpochState) protocol.QueryStateResult {
	sm := protocol.NewEpochStateMachineFromState(state, nil)
	return protocol.QueryStateResult{
		CurrentPhase:         state.CurrentPhase,
		CurrentRole:          state.CurrentRole,
		TransitionHistory:    state.TransitionHistory,
		Votes:                state.ReviewVotes,
		LastError:            state.LastError,
		AvailableTransitions: sm.AvailableTransitions(),
		ActiveSessionCount:   state.ActiveSessionCount,
	}
}

// projectionTableExists reports whether the epoch_state_projection table is
// present in the database. Returns (false, nil) on a fresh database (no epoch
// has ever run), (false, err) on a storage failure, and (true, nil) when the
// table exists.
//
// Both listEpochs (internal/handlers/status.go) and dbProjectionReader.ReadProjection
// need this probe; centralising it here eliminates the duplication.
func projectionTableExists(db *sql.DB) (bool, error) {
	var name string
	switch err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, projectionTable,
	).Scan(&name); {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't check whether the epoch-state projection table exists.",
			Why:      "The database refused the read from sqlite_master.",
			Where:    "Checking for the projection table (internal/handlers/projection.go in handlers.projectionTableExists).",
			Impact:   "Epoch status can't be determined.",
			Fix:      "Confirm the database file is readable, then retry.",
			Cause:    err,
		}
	}
	return true, nil
}

// epochNotFoundError reports that no projected state exists for epochId.
func epochNotFoundError(epochId, caller string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     fmt.Sprintf("No state is recorded for epoch %q yet.", epochId),
		Why: "The epoch has not advanced through any phase, so there is no projection\n" +
			"row to read — either it was never started, or the ID is misspelled.",
		Where:  fmt.Sprintf("Reading the epoch projection (internal/handlers/projection.go in %s).", caller),
		Impact: "There is nothing to report for this epoch.",
		Fix: "1. Confirm the epoch ID is correct.\n" +
			"2. Start the epoch first:\n" +
			"     pasture epoch start --epoch-id <id>\n" +
			"3. Or list the epochs that already have recorded state.",
	}
}

// QueryEpochState answers a projection-backed epoch query. Every query reads the
// same EpochState projection; each renders a different slice of it:
//   - QueryCurrentState         → current phase + role.
//   - QueryAvailableTransitions → the transitions reachable from the current phase.
//   - QueryFullState            → the complete state view.
//   - QueryActiveSessions       → the registered sessions.
//   - QuerySliceProgressState   → the reported slice-progress events.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error (epoch not found).
func QueryEpochState(
	reader ProjectionReader,
	epochId string,
	query protocol.QueryName,
	format types.OutputFormat,
) (int, error) {
	if epochId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An epoch ID is required to query epoch state.",
			Why:      "The --epoch-id flag was not provided.",
			Where:    "Querying epoch state (internal/handlers/projection.go in handlers.QueryEpochState).",
			Impact:   "Without an epoch ID there is no state to read.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture query state --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if !query.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognized epoch query.", query),
			Why:      "The query name must be one of the known epoch-state queries.",
			Where:    "Querying epoch state (internal/handlers/projection.go in handlers.QueryEpochState).",
			Impact:   "The query can't run because its name isn't recognized.",
			Fix: "1. Use one of: current_state, available_transitions, full_state,\n" +
				"   active_sessions, slice_progress_state.",
		}
		return pasterrors.ExitCode(err), err
	}

	state, err := reader.ReadProjection(epochId)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if state == nil {
		e := epochNotFoundError(epochId, "handlers.QueryEpochState")
		return pasterrors.ExitCode(e), e
	}

	out, fmtErr := renderEpochQuery(state, query, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// renderEpochQuery selects and renders the slice of state the query asks for.
func renderEpochQuery(state *protocol.EpochState, query protocol.QueryName, format types.OutputFormat) (string, error) {
	switch query {
	case protocol.QueryActiveSessions:
		return formatters.FormatActiveSessions(state.ActiveSessions, format)
	case protocol.QuerySliceProgressState:
		return formatters.FormatSliceProgressState(state.SliceProgress, format)
	default:
		return formatters.FormatEpochQuery(QueryStateFromProjection(state), query, format)
	}
}

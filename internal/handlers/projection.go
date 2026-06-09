package handlers

import (
	"database/sql"
	"fmt"

	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

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
	return engine.ReadProjection(r.db, epochId)
}

// NewDBProjectionReader returns a ProjectionReader over an already-open shared
// handle. The caller owns the handle's lifecycle.
func NewDBProjectionReader(db *sql.DB) ProjectionReader { return dbProjectionReader{db: db} }

// stateQuerySupported reports whether query is one the projection serves
// directly from the EpochState view (phase/role/votes/transitions/history).
func stateQuerySupported(query protocol.QueryName) bool {
	switch query {
	case protocol.QueryCurrentState, protocol.QueryAvailableTransitions, protocol.QueryFullState:
		return true
	}
	return false
}

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

// QueryEpochState answers a projection-backed epoch query.
//
// All three supported queries read the same projection and the same recomputed
// QueryStateResult; they differ only in which slice of it is rendered:
//   - QueryCurrentState         → current phase + role.
//   - QueryAvailableTransitions → the transitions reachable from the current phase.
//   - QueryFullState            → the complete state view.
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
	if !stateQuerySupported(query) {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not an epoch-state query.", query),
			Why: "This reader answers the state queries that the EpochState projection\n" +
				"carries directly.",
			Where:  "Querying epoch state (internal/handlers/projection.go in handlers.QueryEpochState).",
			Impact: "The query can't run because its name isn't one this command serves.",
			Fix: "1. Use one of: current_state, available_transitions, full_state.",
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

	result := QueryStateFromProjection(state)

	out, fmtErr := formatters.FormatEpochQuery(result, query, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// projectionDDL creates the epoch-state projection table. The projection is a
// serialization of the in-flight EpochState, refreshed on every transition, so
// queries and the status surface read current state from one row instead of
// round-tripping the durable workflow. epoch_id is the primary key (one live
// row per epoch); current_phase is denormalized out of the JSON for cheap
// filtering.
const projectionDDL = `
CREATE TABLE IF NOT EXISTS epoch_state_projection (
	epoch_id      TEXT    PRIMARY KEY,
	current_phase TEXT    NOT NULL,
	state_json    TEXT    NOT NULL,
	updated_at    INTEGER NOT NULL
)`

// ensureProjectionTable creates the projection table if it does not exist.
func ensureProjectionTable(db *sql.DB) error {
	if _, err := db.Exec(projectionDDL); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't create the epoch-state projection table.",
			Why:      "The database refused the CREATE TABLE for the engine's state projection.",
			Where:    "Setting up the engine projection (internal/engine/projection.go in engine.ensureProjectionTable).",
			Impact:   "The engine can't persist epoch state, so status and query surfaces have nothing to read.",
			Fix: "1. Confirm the database file is writable and the disk has free space.\n" +
				"2. Retry once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// WriteProjection upserts the serialized EpochState for state.EpochId. It is
// idempotent (last-write-wins) — safe to re-run when a durable step replays,
// because the projection is a cache of the authoritative FSM state, not an
// append-only log. nowUnixNano timestamps the row's freshness.
func WriteProjection(ctx context.Context, db *sql.DB, state *protocol.EpochState, nowUnixNano int64) error {
	blob, err := json.Marshal(state)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't serialize the epoch-state projection for epoch %q.", state.EpochId),
			Why:      "The EpochState contained a value that can't be encoded as JSON.",
			Where:    "Persisting the epoch projection (internal/engine/projection.go in engine.WriteProjection).",
			Impact:   "This transition's state can't be projected, so queries may report a stale phase.",
			Fix:      "This is an internal encoding bug — please file a report with the epoch id above.",
			Cause:    err,
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO epoch_state_projection (epoch_id, current_phase, state_json, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(epoch_id) DO UPDATE SET
		     current_phase = excluded.current_phase,
		     state_json    = excluded.state_json,
		     updated_at    = excluded.updated_at`,
		state.EpochId, string(state.CurrentPhase), string(blob), nowUnixNano,
	); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't persist the epoch-state projection for epoch %q.", state.EpochId),
			Why:      "The database refused the upsert into epoch_state_projection.",
			Where:    "Persisting the epoch projection (internal/engine/projection.go in engine.WriteProjection).",
			Impact:   "Queries and the status surface may report a stale phase for this epoch.",
			Fix: "1. Confirm the database file is writable and the disk has free space.\n" +
				"2. Retry once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// ReadProjection returns the projected EpochState for epochId. It returns
// (nil, nil) when no projection exists yet (the epoch has not advanced), so
// callers can distinguish "unknown epoch" from a read error.
//
// Callers that need available transitions recompute them from the returned
// state via protocol.NewEpochStateMachineFromState(...).AvailableTransitions();
// the projection stores raw state, not derived views.
func ReadProjection(db *sql.DB, epochId string) (*protocol.EpochState, error) {
	var blob string
	err := db.QueryRow(
		`SELECT state_json FROM epoch_state_projection WHERE epoch_id = ?`, epochId,
	).Scan(&blob)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't read the epoch-state projection for epoch %q.", epochId),
			Why:      "The database refused the read from epoch_state_projection.",
			Where:    "Reading the epoch projection (internal/engine/projection.go in engine.ReadProjection).",
			Impact:   "Status and query surfaces can't report this epoch's current phase.",
			Fix: "1. Confirm the database file is readable.\n" +
				"2. If the projection table is missing, start an epoch through the engine to create it.",
			Cause: err,
		}
	}

	var state protocol.EpochState
	if err := json.Unmarshal([]byte(blob), &state); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The stored epoch-state projection for epoch %q is corrupted.", epochId),
			Why:      "The projection row's JSON couldn't be parsed back into an EpochState.",
			Where:    "Reading the epoch projection (internal/engine/projection.go in engine.ReadProjection).",
			Impact:   "This epoch's state can't be reported until the projection is rewritten by a new transition.",
			Fix:      "Advance the epoch (or re-run it) to overwrite the projection row with a fresh serialization.",
			Cause:    err,
		}
	}
	return &state, nil
}

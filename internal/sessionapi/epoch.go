package sessionapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// Epochs returns the projected epochs and their current session counts.
func (s *Service) Epochs(ctx context.Context) (EpochPage, error) {
	if s == nil || s.DB == nil {
		return EpochPage{}, errors.New("session API has no database handle")
	}
	if err := s.validateWindows(); err != nil {
		return EpochPage{}, err
	}
	now := s.now()
	rows, revision, err := s.epochRows(ctx, now, "")
	if err != nil {
		return EpochPage{}, err
	}
	return EpochPage{Revision: revision, AsOf: now, Items: rows}, nil
}

// Epoch returns one projected epoch.
func (s *Service) Epoch(ctx context.Context, epochID string) (EpochDetail, error) {
	if s == nil || s.DB == nil {
		return EpochDetail{}, errors.New("session API has no database handle")
	}
	if epochID == "" {
		return EpochDetail{}, ErrNotFound
	}
	if err := s.validateWindows(); err != nil {
		return EpochDetail{}, err
	}
	now := s.now()
	rows, revision, err := s.epochRows(ctx, now, epochID)
	if err != nil {
		return EpochDetail{}, err
	}
	if len(rows) == 0 {
		return EpochDetail{}, ErrNotFound
	}
	return EpochDetail{Revision: revision, AsOf: now, Epoch: rows[0]}, nil
}

// EpochSessions returns the sessions whose latest binding names epochID.
func (s *Service) EpochSessions(ctx context.Context, epochID string, opts ListOptions) (Page, error) {
	if _, err := s.Epoch(ctx, epochID); err != nil {
		return Page{}, err
	}
	opts.Epoch = epochID
	return s.List(ctx, opts)
}

// Inspect returns the combined epoch and session snapshot. One response clock
// labels the epochs and the sessions, so the active count and the session rows
// cannot straddle a cutoff and disagree.
func (s *Service) Inspect(ctx context.Context) (InspectPayload, error) {
	if s == nil || s.DB == nil {
		return InspectPayload{}, errors.New("session API has no database handle")
	}
	if err := s.validateWindows(); err != nil {
		return InspectPayload{}, err
	}
	now := s.now()
	epochRows, revision, err := s.epochRows(ctx, now, "")
	if err != nil {
		return InspectPayload{}, err
	}
	sessions, err := s.listAt(context.WithValue(ctx, ensuredContextKey{}, true), now, ListOptions{Limit: 100})
	if err != nil {
		return InspectPayload{}, err
	}
	return InspectPayload{
		Revision:           revision,
		AsOf:               now,
		Truncated:          sessions.Next != "",
		Epochs:             epochRows,
		Sessions:           sessions.Items,
		PeasantIntegration: s.peasantIntegrationState(),
	}, nil
}

func (s *Service) epochRows(ctx context.Context, now time.Time, epochID string) ([]EpochView, int64, error) {
	if err := s.ensureFresh(ctx); err != nil {
		return nil, 0, err
	}
	tx, revision, err := beginSessionRead(ctx, s.DB)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	exists, err := tableExists(ctx, tx, "epoch_state_projection")
	if err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, revision, nil
	}
	query := `
		SELECT p.epoch_id, p.current_phase, p.state_json, COALESCE(t.title, '')
		FROM epoch_state_projection p
		LEFT JOIN tasks t ON t.id = p.epoch_id`
	args := []any{}
	if epochID != "" {
		query += ` WHERE p.epoch_id = ?`
		args = append(args, epochID)
	}
	query += ` ORDER BY p.epoch_id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying epochs: %w", err)
	}
	defer rows.Close()
	out := make([]EpochView, 0, 8)
	for rows.Next() {
		var row EpochView
		var phase, stateJSON string
		if err := rows.Scan(&row.ID, &phase, &stateJSON, &row.Title); err != nil {
			return nil, 0, fmt.Errorf("scanning epoch: %w", err)
		}
		var state protocol.EpochState
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, 0, fmt.Errorf("decoding epoch projection %q: %w", row.ID, err)
		}
		row.Phase = phase
		row.Role = string(state.CurrentRole)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating epochs: %w", err)
	}
	inferredUsable := inferredBindingUsable(ctx, tx)
	counts, err := epochSessionCounts(ctx, tx, s.activeCutoff(now), s.idleCutoff(now), inferredUsable)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		if count, ok := counts[out[i].ID]; ok {
			out[i].SessionCount = count.Total
			out[i].ActiveCount = count.Active
		}
	}
	return out, revision, nil
}

type epochCount struct {
	Total  int
	Active int
}

// epochSessionCounts counts the sessions whose EFFECTIVE current binding names
// each epoch, so an epoch a session was provisionally (inferred) bound to is
// counted exactly as an explicit binding would be. The definition of the
// current binding and of the active cutoff is shared with the session page:
// bindingCurrentCTE and Service.activeCutoff, never a second copy.
func epochSessionCounts(ctx context.Context, tx *sql.Tx, activeCutoff, idleCutoff int64, inferredUsable bool) (map[string]epochCount, error) {
	// SUM over the shared liveness CASE is the same predicate the session page
	// filters on: a session counted active here is a session the page labels
	// active. The two placeholders are the active cutoff then the idle cutoff.
	statement := bindingCurrentCTE(inferredUsable) + `
	SELECT b.epoch_id, COUNT(*),
	       SUM(CASE WHEN ` + livenessStateSQL(
		promptAtExpr, hostEndJournalExpr, lastResumeExpr,
		"b.unbound_at", "b.unbound_reason",
	) + ` = '` + string(StateActive) + `' THEN 1 ELSE 0 END)
	FROM binding_current b
	LEFT JOIN pasture_session_state s ON s.harness = b.harness AND s.session = b.session
	GROUP BY b.epoch_id`
	rows, err := tx.QueryContext(ctx, statement, activeCutoff, idleCutoff)
	if err != nil {
		return nil, fmt.Errorf("querying epoch session counts: %w", err)
	}
	defer rows.Close()
	out := map[string]epochCount{}
	for rows.Next() {
		var epochID string
		var count epochCount
		if err := rows.Scan(&epochID, &count.Total, &count.Active); err != nil {
			return nil, fmt.Errorf("scanning epoch session count: %w", err)
		}
		out[epochID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating epoch session counts: %w", err)
	}
	return out, nil
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking table %s: %w", name, err)
	}
	return true, nil
}

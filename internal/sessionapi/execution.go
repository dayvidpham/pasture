package sessionapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// executionCursor pins one history walk to a journal snapshot. Snapshot is the
// MAX(journal_id) at the first page, and Before is the last journal id already
// returned. Pinning keeps a live replay from changing the page boundaries under
// an open cursor.
type executionCursor struct {
	Snapshot int64 `json:"s"`
	Before   int64 `json:"b"`
}

func encodeExecutionCursor(cursor executionCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeExecutionCursor(encoded string) (executionCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return executionCursor{}, fmt.Errorf("%w: cursor is not base64", ErrInvalidPageToken)
	}
	var cursor executionCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return executionCursor{}, fmt.Errorf("%w: cursor is not JSON", ErrInvalidPageToken)
	}
	if cursor.Snapshot <= 0 || cursor.Before < 0 {
		return executionCursor{}, fmt.Errorf("%w: execution cursor is incomplete", ErrInvalidPageToken)
	}
	return cursor, nil
}

// ExecutionHistory returns one snapshot-pinned page of normalized execution
// observations for a session, newest first. The history includes auxiliary and
// child records; the session summary never uses them. Actor attribution is not
// part of this payload because it is a fact about the session claim, not about
// one receipt.
func (s *Service) ExecutionHistory(ctx context.Context, harness, session string, opts ExecutionHistoryOptions) (ExecutionHistoryPage, error) {
	if s == nil || s.DB == nil {
		return ExecutionHistoryPage{}, errors.New("session API has no database handle")
	}
	if strings.TrimSpace(harness) == "" || strings.TrimSpace(session) == "" {
		return ExecutionHistoryPage{}, ErrNotFound
	}
	if opts.Limit == 0 {
		opts.Limit = 50
	}
	if opts.Limit < 1 || opts.Limit > 100 {
		return ExecutionHistoryPage{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidFilter)
	}
	var cursor executionCursor
	if opts.Cursor != "" {
		decoded, err := decodeExecutionCursor(opts.Cursor)
		if err != nil {
			return ExecutionHistoryPage{}, err
		}
		cursor = decoded
	}
	if s.Ensure != nil {
		if err := s.Ensure(ctx); err != nil {
			return ExecutionHistoryPage{}, err
		}
	}
	now := s.now()
	tx, revision, err := beginSessionRead(ctx, s.DB)
	if err != nil {
		return ExecutionHistoryPage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if cursor.Snapshot == 0 {
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(journal_id), 0)
			FROM pasture_session_execution
			WHERE harness = ? AND session_id = ?`, harness, session).Scan(&cursor.Snapshot); err != nil {
			return ExecutionHistoryPage{}, fmt.Errorf("reading execution snapshot: %w", err)
		}
	}
	page := ExecutionHistoryPage{Revision: revision, AsOf: now, Items: []SessionExecution{}}
	if cursor.Snapshot == 0 {
		return page, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT journal_id, observed_at, execution_json
		FROM pasture_session_execution
		WHERE harness = ? AND session_id = ? AND journal_id <= ?
		  AND (? = 0 OR journal_id < ?)
		ORDER BY journal_id DESC
		LIMIT ?`,
		harness, session, cursor.Snapshot, cursor.Before, cursor.Before, opts.Limit+1)
	if err != nil {
		return ExecutionHistoryPage{}, fmt.Errorf("querying execution history: %w — verify pasture_session_execution matches the projection contract", err)
	}
	defer rows.Close()
	for rows.Next() {
		var journalID, observedAt int64
		var encoded []byte
		if err := rows.Scan(&journalID, &observedAt, &encoded); err != nil {
			return ExecutionHistoryPage{}, fmt.Errorf("scanning execution history: %w", err)
		}
		item, err := decodeSessionExecution(journalID, observedAt, encoded)
		if err != nil {
			return ExecutionHistoryPage{}, err
		}
		page.Items = append(page.Items, *item)
	}
	if err := rows.Err(); err != nil {
		return ExecutionHistoryPage{}, fmt.Errorf("iterating execution history: %w", err)
	}
	if len(page.Items) > opts.Limit {
		page.Items = page.Items[:opts.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = encodeExecutionCursor(executionCursor{Snapshot: cursor.Snapshot, Before: last.OccurrenceID})
	}
	return page, nil
}

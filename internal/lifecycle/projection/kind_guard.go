package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dayvidpham/provenance"
)

type kindQueryReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RefuseUnknownOccurrenceKinds checks the reserved occurrence namespace at a
// journal snapshot. Provenance's exact-kind query cannot discover future kinds.
// A transaction-bound caller supplies its own transaction to preserve its view.
func RefuseUnknownOccurrenceKinds(ctx context.Context, reader kindQueryReader, after, snapshot provenance.JournalID) error {
	var id int64
	var kind string
	if reader == nil {
		return fmt.Errorf("inspect lifecycle occurrence kinds: no database view was supplied; use the same transaction as the evidence reader")
	}
	args := []any{after, snapshot}
	for _, known := range occurrenceEvidenceKinds {
		args = append(args, string(known))
	}
	// LIMIT bounds the returned rows, not SQLite's work to locate one.
	query := `SELECT journal_id,evidence_kind FROM journal_evidence WHERE journal_id>? AND journal_id<=? AND evidence_kind GLOB 'pasture.lifecycle.occurrence.*' AND evidence_kind NOT IN (` + placeholders(len(occurrenceEvidenceKinds)) + `) ORDER BY journal_id LIMIT 1`
	err := reader.QueryRowContext(ctx, query, args...).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect lifecycle occurrence kinds: %w; repair the journal and retry", err)
	}
	return fmt.Errorf("unsupported lifecycle occurrence kind %q at journal row %d; upgrade Pasture before reading this evidence", kind, id)
}

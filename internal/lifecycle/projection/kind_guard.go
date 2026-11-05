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
	// Lead with the indexed column. With journal_id first, SQLite walks the
	// primary-key range and tests a GLOB on every row: measured 402ms at the
	// start of a 1.8M-row journal, and this runs once per replayed page, so a
	// full replay spent about six minutes proving no unknown kind exists.
	// Leading with evidence_kind lets the covering (evidence_kind, journal_id)
	// index answer it in a flat 41ms over the same (after, snapshot] range.
	// ORDER BY is not needed: only existence is asked, and any offending row
	// identifies the kind the reader must upgrade past.
	args := make([]any, 0, len(occurrenceEvidenceKinds)+2)
	for _, known := range occurrenceEvidenceKinds {
		args = append(args, string(known))
	}
	args = append(args, after, snapshot)
	query := `SELECT journal_id,evidence_kind FROM journal_evidence WHERE evidence_kind GLOB 'pasture.lifecycle.occurrence.*' AND evidence_kind NOT IN (` + placeholders(len(occurrenceEvidenceKinds)) + `) AND journal_id>? AND journal_id<=? LIMIT 1`
	err := reader.QueryRowContext(ctx, query, args...).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect lifecycle occurrence kinds: %w; repair the journal and retry", err)
	}
	return fmt.Errorf("unsupported lifecycle occurrence kind %q at journal row %d; upgrade Pasture before reading this evidence", kind, id)
}

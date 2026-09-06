package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

// assignmentIndexPage contains one bounded catch-up page. From is the watermark
// used for its journal read. Through is the last journal position fully covered
// by that read, not the maximum of a journal with more unread facts.
type assignmentIndexPage struct {
	From     provenance.JournalID
	Through  provenance.JournalID
	Rows     []startedEpisode
	Expected *assignmentRecoveryState
}

// IndexStaleError is a storage fault, not a policy decision. A caller can use its
// complete in-memory view or refuse evaluation; it must not infer a missing
// assignment from a failed persist.
type IndexStaleError struct{ Cause error }

func (e *IndexStaleError) Error() string {
	return "The gate assignment index was not persisted: " + e.Cause.Error() +
		". Where: internal/tasks/assignment_index_write.go, during bounded catch-up persistence." +
		" Impact: this storage fault is not a policy denial; a reported dirty invalidation may be committed, but no rejected page is certified." +
		" Fix: release other writers or repair the store, then run pasture gate rebuild-index."
}

func (e *IndexStaleError) Unwrap() error { return e.Cause }

// persistAssignmentIndex commits one page and its watermark atomically. It does
// not read the journal or infer completeness: the caller supplies a contiguous
// covered interval. The stored watermark must reach From before it can advance
// to Through. Concurrent calls over the same interval are idempotent, and an
// older page cannot move the watermark backwards. An absent watermark means
// zero; the first contiguous page creates the singleton in this transaction.
//
// There is one transaction, one bulk upsert, one watermark update, and no retry.
// Only construction of the bounded VALUES list loops. This API does not yet
// count caller invocations; the gate reader must call it at most once per read.
func persistAssignmentIndex(ctx context.Context, db *sql.DB, profile timeouts.Profile, page assignmentIndexPage) (err error) {
	defer func() {
		if err != nil {
			err = &IndexStaleError{Cause: err}
		}
	}()
	if ctx == nil || db == nil {
		return fmt.Errorf("the context or database handle is missing")
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	if page.From < 0 || page.Through < page.From {
		return fmt.Errorf("the page has an invalid covered interval %d..%d", page.From, page.Through)
	}
	if len(page.Rows) > gateauthority.CatchUpPageSize {
		return fmt.Errorf("the page has %d rows, above the catch-up bound %d", len(page.Rows), gateauthority.CatchUpPageSize)
	}
	args := make([]any, 0, len(page.Rows)*5)
	values := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		if err := row.validate("persistAssignmentIndex"); err != nil {
			return fmt.Errorf("the page contains an unusable episode: %s", err)
		}
		if row.Authority > page.Through {
			return fmt.Errorf("episode %q has authority %d above page bound %d", row.Assignment, row.Authority, page.Through)
		}
		values = append(values, "(?, ?, ?, ?, ?)")
		args = append(args, string(row.Assignment), row.Actor.String(), row.Task.String(), row.Role.String(), int64(row.Authority))
	}
	bounded, cancel := context.WithTimeout(ctx, profile.SQLiteBusy())
	defer cancel()
	tx, err := db.BeginTx(bounded, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := readAssignmentRecoveryState(bounded, tx)
	if err != nil {
		return err
	}
	if page.Expected != nil && state != *page.Expected {
		return fmt.Errorf("the captured recovery state changed before persistence")
	}
	if state.Status != assignmentCoverageValid || state.Destructive != state.CertifiedDestructive {
		return fmt.Errorf("index generation is not certified valid; run operator rebuild or reset")
	}
	for _, row := range page.Rows {
		existing, found, err := readStartedEpisodeTx(bounded, tx, state.Generation, row.Assignment)
		if err != nil {
			return commitAssignmentDirtyTx(bounded, tx, state, "catch-up found an unreadable index row: "+err.Error())
		}
		if found && existing != row {
			return commitAssignmentDirtyTx(bounded, tx, state, "catch-up conflicts with an existing assignment")
		}
	}
	if len(values) != 0 {
		// Generation is taken from the guarded state, never supplied by a row.
		for i := range values {
			values[i] = strings.TrimSuffix(values[i], ")") + fmt.Sprintf(", %d)", state.Generation)
		}
		_, err = tx.ExecContext(bounded, `INSERT INTO pasture_actor_assignment (assignment_id, actor_id, task_id, role, authority_journal_id,generation) VALUES `+strings.Join(values, ",")+
			` ON CONFLICT(assignment_id) DO NOTHING`, args...)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(bounded, `INSERT INTO pasture_actor_assignment_watermark (singleton_id, last_indexed_jid)
		SELECT 0, ? WHERE COALESCE((SELECT last_indexed_jid FROM pasture_actor_assignment_watermark WHERE singleton_id = 0), 0) >= ?
		ON CONFLICT(singleton_id) DO UPDATE SET last_indexed_jid = max(last_indexed_jid, excluded.last_indexed_jid)`, int64(page.Through), int64(page.From))
	if err != nil {
		return err
	}
	written, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if written != 1 {
		return fmt.Errorf("the stored watermark is absent or does not reach the page start %d", page.From)
	}
	if err := updateRecoveryStateTx(bounded, tx, state, `completed_through_jid=max(completed_through_jid,?),state_revision=state_revision+1`, page.Through); err != nil {
		return err
	}
	return tx.Commit()
}

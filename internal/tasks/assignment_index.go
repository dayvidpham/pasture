package tasks

// assignment_index.go owns the ordinary command writer for the started-episode
// index. Authenticated recovery pages use the shared guarded prefix core in
// assignment_recovery.go; they do not create or change journal assignments.
//
// WHY AN INDEX AT ALL. A gate has milliseconds and must answer "which task does
// this actor hold?". The journal can answer it, but only by walking, and a gate
// cannot walk a journal. So pasture keeps a small table of started episodes and
// asks the journal only the questions a table cannot answer.
//
// WHAT THE TABLE DOES NOT HOLD. It has no active column. Whether an episode is
// still active is decided at READ time by the journal's governance predicate,
// because an episode can end in the journal with no pasture-side write at all.
// The table records that an episode STARTED and under which authority; the
// journal says whether it still holds.
//
// WHERE authority_journal_id COMES FROM. It is not carried in the material
// event, and it exists only AFTER the journal commit, so the index is written
// AFTER the commit and never inside it. Each writer obtains it from what its own
// commit returned:
//   - the four assignment commands read it from the closure of their composed
//     allocation, which binds the child's assignment row;
//   - the transfer path has no such closure and no result slot, so it recovers
//     it by a bounded forward scan over its own operation's rows.
// A row with no authority is refused. An index row nobody can evaluate is worse
// than a missing one, because a caller would read it and get an answer.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// startedEpisode is one row of the started-episode index.
type startedEpisode struct {
	Assignment provenance.AssignmentID
	Actor      provenance.ActorID
	Task       provenance.TaskID
	Role       AssignmentRole
	Authority  provenance.JournalID
}

// validate refuses a record that cannot answer a gate's question.
func (e startedEpisode) validate(where string) error {
	switch {
	case e.Assignment == "":
		return indexError(where, "the episode has no assignment id", "an index row is keyed by its assignment, so a row without one would overwrite another episode's row")
	case e.Actor == (provenance.ActorID{}):
		return indexError(where, fmt.Sprintf("episode %q has no occupant", e.Assignment), "the gate looks an episode up by its occupant, so a row without one can never be found")
	case e.Task == (provenance.TaskID{}):
		return indexError(where, fmt.Sprintf("episode %q has no task", e.Assignment), "an episode is an actor holding a TASK; without the task the row says nothing a gate can use")
	case !e.Role.valid():
		return indexError(where, fmt.Sprintf("episode %q has role %s, which is not a known slot", e.Assignment, e.Role), "the legality rules are keyed on the slot, so an unknown slot cannot be judged")
	case e.Authority <= 0:
		return indexError(where, fmt.Sprintf("episode %q has no governing authority", e.Assignment), "activeness is decided by asking the journal whether that authority still governs the task; a row without one can never be evaluated, and an index row nobody can evaluate is worse than a missing one, because a caller reads it and gets an answer")
	}
	return nil
}

func indexError(where, what, why string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Pasture could not record who holds a task.",
		Why:      what + ": " + why + ".",
		Where:    "Recording a started assignment (internal/tasks/assignment_index.go in tasks." + where + ").",
		Impact:   "The assignment itself is committed and is not lost. Until the record is written, a gate on that task sees no holder, so the new holder is refused work they now own.",
		Fix: "1. Re-run the command; the record is written again from the same committed assignment.\n" +
			"2. If it keeps failing, rebuild the record from the task history:\n" +
			"     pasture gate rebuild-index",
	}
}

// recordAssignmentStart is the choke point for ordinary command index writes.
//
// Every writer of an assignment-start fact goes through here, and a test derives
// that set from the source rather than trusting this comment: a writer added
// later that does not call this function turns that test red.
//
// Exact replay is a no-op; a conflicting re-record commits dirty invalidation
// without overwriting the original row. It runs AFTER the journal commit,
// never inside it, because the authority id it stores does not exist until then.
func recordAssignmentStart(ctx context.Context, db *sql.DB, episode startedEpisode) error {
	if db == nil || ctx == nil {
		return indexError("recordAssignmentStart", "the database handle or cancellation context is missing", "an internal caller reached the index without its required store and operation context")
	}
	if err := episode.validate("recordAssignmentStart"); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return recoveryFault("ordinary writer transaction", err)
	}
	defer tx.Rollback()
	state, err := readAssignmentRecoveryState(ctx, tx)
	if err != nil {
		return recoveryFault("ordinary writer state; reopen through OpenTaskTracker", err)
	}
	if state.Status == assignmentCoverageDirty {
		return recoveryFault("ordinary writer", fmt.Errorf("generation %d is dirty; reset required", state.Generation))
	}
	existing, found, err := readStartedEpisodeTx(ctx, tx, state.Generation, episode.Assignment)
	if err != nil {
		return commitAssignmentDirtyTx(ctx, tx, state, "existing index row cannot be decoded: "+err.Error())
	}
	if found && existing == episode {
		return nil
	}
	if found || episode.Authority <= state.Through {
		return commitAssignmentDirtyTx(ctx, tx, state, "ordinary write conflicts with an existing row or the certified prefix")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pasture_actor_assignment
	 (assignment_id,actor_id,task_id,role,authority_journal_id,generation) VALUES(?,?,?,?,?,?)`,
		string(episode.Assignment), episode.Actor.String(), episode.Task.String(), episode.Role.String(), int64(episode.Authority), state.Generation)
	if err != nil {
		// Preserve the command writer's established structured storage error in
		// the typed stale chain. Callers report its detailed database failure.
		return recoveryFault("ordinary index insert", indexError(
			"recordAssignmentStart",
			fmt.Sprintf("writing the record for episode %q failed", episode.Assignment),
			"the database refused the write: "+err.Error(),
		))
	}
	if err := updateRecoveryStateTx(ctx, tx, state, `state_revision=state_revision+1`); err != nil {
		return recoveryFault("ordinary writer compare-and-swap", err)
	}
	if err := tx.Commit(); err != nil {
		return recoveryFault("ordinary writer commit", err)
	}
	return nil
}

// composedAssignmentAuthority reads the child's assignment-authority journal id
// out of a composed allocation's own closure.
//
// This is where the four assignment commands get their authority id. The
// closure binds each child to two produced rows, its task row and its assignment
// row; the assignment row IS the authority. Nothing is scanned.
func composedAssignmentAuthority(closure provenance.OperationClosure, task provenance.TaskID, assignment provenance.AssignmentID) (provenance.JournalID, error) {
	for _, child := range closure.Children() {
		if child.TaskID != task || child.AssignmentID != assignment {
			continue
		}
		if child.AssignmentRow.JournalID <= 0 {
			return 0, indexError("composedAssignmentAuthority", fmt.Sprintf("the committed allocation bound no authority row for episode %q", assignment), "a composed allocation always produces an assignment row for each child, so a closure without one is a broken receipt")
		}
		return child.AssignmentRow.JournalID, nil
	}
	return 0, indexError("composedAssignmentAuthority", fmt.Sprintf("the committed allocation contains no child for task %q and episode %q", task, assignment), "the index is written from the same closure the command returned, so a missing child means the command and the receipt disagree")
}

// maxTransferOperationRows bounds the forward scan below. A transfer commits its
// anchor and a fixed, small number of rows after it: the end row of the previous
// episode and the start row of the new one. The bound is deliberately a little
// wider than that, so a future transfer shape that writes one more row still
// works, and deliberately FIXED, so the scan can never become a journal walk.
const maxTransferOperationRows = 8

// transferAssignmentAuthority recovers the authority journal id of the episode a
// transfer just started, by a BOUNDED FORWARD SCAN over that operation's own
// rows.
//
// WHY A SCAN AT ALL, when every other writer reads the id from its result: a
// transfer is a provenance-owned atomic operation. It binds no result slot on
// its start effect, and the result it returns carries no journal id by design.
// The operation's anchor IS obtainable, so the rows above it can be tested.
//
// WHY THE UPPER BOUND IS SOUND. The scan runs from the anchor to the journal
// maximum read immediately after the transfer committed. That maximum is the
// operation's own last row for a STRUCTURAL reason, not by luck: the caller
// holds the tracker's single cross-connection write lock for the whole body of
// the transfer, so no other pasture writer can commit in between. The scan is
// additionally capped at maxTransferOperationRows, so even if that reasoning
// ever stopped holding, this would stay an operation-sized read and never a
// journal walk.
//
// It must therefore be called INSIDE the lock the transfer already holds.
func transferAssignmentAuthority(
	journal provenance.Journal,
	anchor provenance.JournalID,
	journalMax provenance.JournalID,
	task provenance.TaskID,
) (provenance.JournalID, error) {
	if anchor <= 0 {
		return 0, indexError("transferAssignmentAuthority", "the committed transfer has no anchor", "the authority row is found among the rows of the transfer's own operation, and without the anchor there is nothing to scan from")
	}
	if journalMax < anchor {
		return 0, indexError("transferAssignmentAuthority", fmt.Sprintf("the journal maximum %d is below the transfer anchor %d", journalMax, anchor), "the maximum is read immediately after the transfer commits, so it cannot be below the transfer's own anchor")
	}
	ceiling := anchor + maxTransferOperationRows
	if journalMax < ceiling {
		ceiling = journalMax
	}
	// The predicate's upper bound is EXCLUSIVE of the row it is given, and the
	// authority row can be the journal maximum itself, so the bound passed here
	// is one above the maximum. Measured: asked at exactly its own row, an
	// authority reports that it does not govern, because the episode has not
	// opened yet at that point.
	before := journalMax + 1
	for candidate := anchor + 1; candidate <= ceiling; candidate++ {
		governs, err := journal.AuthorityGovernsTaskAt(candidate, task, before)
		if err != nil {
			return 0, indexError("transferAssignmentAuthority", fmt.Sprintf("testing journal row %d as the new authority for task %q failed", candidate, task), err.Error())
		}
		if governs {
			return candidate, nil
		}
	}
	return 0, indexError("transferAssignmentAuthority", fmt.Sprintf("no row of the transfer operation anchored at %d governs task %q", anchor, task), "a committed transfer always starts one episode, so its own rows must contain the authority that governs the task")
}

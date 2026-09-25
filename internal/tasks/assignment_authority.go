package tasks

// assignment_authority.go owns ONE remaining question on the write path: after a
// task assignment is transferred, which journal row is the authority of the
// episode the transfer just started?
//
// A transfer is a Provenance-owned atomic operation. It binds no result slot on
// its start effect, and the result it returns carries no journal id, so the
// authority id has to be recovered from the operation's own rows. Every other
// assignment command reads the same id out of the closure its commit returned;
// only the transfer has to look for it.
//
// There is no Pasture-owned table of started episodes behind any of this. What a
// gate reads comes from the journal, and what a command writes is the journal
// rows themselves. The type below is a plain value used to carry an
// authenticated start from the proof to its caller; it is not a stored record.

import (
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// startedEpisode is one authenticated started episode: who holds it, on which
// task, in which slot, and the journal id of the authority that governs it. It
// is the row type of an authenticated start page and of its predecessor map.
type startedEpisode struct {
	Assignment provenance.AssignmentID
	Actor      provenance.ActorID
	Task       provenance.TaskID
	Role       AssignmentRole
	Authority  provenance.JournalID
}

func transferAuthorityError(task, what, why string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Pasture could not find the authority that the transfer gave to the new holder of task %s.", task),
		Why:      what + ": " + why + ".",
		Where:    "Writing the transferred holder's start fact (internal/tasks/assignment_authority.go in tasks.transferAssignmentAuthority).",
		Impact:   "The transfer is committed and the new holder owns the task. Pasture did not write the new holder's assignment-start fact, so a later transfer of this task cannot find the current holder.",
		Fix:      "Run the same transfer again with the same request. The journal replays the committed transfer and Pasture writes the fact once.",
	}
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
		return 0, transferAuthorityError(task.String(), "the committed transfer has no anchor", "the authority row is found among the rows of the transfer's own operation, and without the anchor there is nothing to scan from")
	}
	if journalMax < anchor {
		return 0, transferAuthorityError(task.String(), fmt.Sprintf("the journal maximum %d is below the transfer anchor %d", journalMax, anchor), "the maximum is read immediately after the transfer commits, so it cannot be below the transfer's own anchor")
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
			return 0, transferAuthorityError(task.String(), fmt.Sprintf("testing journal row %d as the new authority for the task failed", candidate), err.Error())
		}
		if governs {
			return candidate, nil
		}
	}
	return 0, transferAuthorityError(task.String(), fmt.Sprintf("no row of the transfer operation anchored at %d governs the task", anchor), "a committed transfer always starts one episode, so its own rows must contain the authority that governs the task")
}

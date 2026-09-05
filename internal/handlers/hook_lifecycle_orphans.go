package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// OrphanPayloadNote is the whole meaning of the number, and it is printed
// beside EVERY reading of it, including a reading of zero.
//
// The number without this sentence teaches the wrong lesson, and that is the
// expensive failure here. An operator who reads "orphan payload blobs: 4213"
// and nothing else will reasonably conclude that the store is damaged and go
// hunting for corruption. It is not damaged: the corrupting state is an
// occurrence naming a blob that is absent, and that state is the one thing the
// write order makes impossible. What the number actually reports is the LEGAL
// side of the same boundary, and a large reading is a signal about CONTENTION,
// not about damage. So the note ships with the number rather than living only
// in documentation a reader at a terminal does not have open.
//
// It is printed at zero as well, on one code path, because an operator meets
// the term for the first time whichever number is beside it, and a term whose
// meaning appears only above some threshold is a term the reader learns at the
// worst possible moment.
const OrphanPayloadNote = "An orphan is a payload blob that no recorded occurrence names. " +
	"One is left behind by a hook invocation that was abandoned between its two durable writes, and at most one arises per abandoned invocation. " +
	"This is expected and reclaimable, not damage: the blob is written before the journal row deliberately, because a spare blob can be reclaimed later while a journal row naming an absent blob could not be repaired at all. " +
	"A large number therefore does not mean the store is corrupt. It means invocations were abandoned repeatedly, so the thing to investigate is the store contention that caused the abandonment, such as another writer holding the pasture store." +
	"Every read command reclaims orphans that are older than the writer window, at most 1024 per run, so the number remaining can be smaller than the number that was there before the command ran; the reclaimed count says by how much."

// HookLifecycleOrphansInput selects the store to inspect.
type HookLifecycleOrphansInput struct {
	DBPath string
}

// HookLifecycleOrphans reports how many committed payload blobs no occurrence
// names. It changes no journal truth, but IT IS NOT A PURE READ: like every
// read command it rebuilds the disposable occurrence projection first, and
// the rebuild reclaims orphan blobs older than the writer window, up to the
// projection package's cap. A command that MEASURES orphans therefore
// MUTATES the thing it measures by running, so it prints TWO numbers, and
// each is true of the run that printed it: how many blobs this run
// reclaimed, and how many remain. A single number that shrank because the
// command ran would be a defect unless the output said so.
//
// It REBUILDS THE DISPOSABLE OCCURRENCE PROJECTION from the journal first, for
// the same reason `pasture hook lifecycle list` does. The count is a LEFT JOIN
// against that projection; measured against a projection that was never
// rebuilt, every blob would look unnamed and the command would report the
// largest and most alarming wrong number this store can produce. The rebuild is
// derived state only, so journal truth is untouched.
//
// This report lives on an operator command and NEVER on the hook path. That is
// not tidiness. On the hook path the count would spend the hook-invocation
// deadline on a question no host asks, and it reads the store, which is the
// thing that contends. So it would be slowest under exactly the condition that
// produces orphans, and a slow enough count would push the invocation into its
// deadline and leave one more orphan behind. The counter would become a cause
// of the thing it counts.
func HookLifecycleOrphans(ctx context.Context, out io.Writer, in HookLifecycleOrphansInput, format string) (int, error) {
	// The two refusals are separated because they have DIFFERENT READERS. A
	// missing context or writer cannot be produced from a terminal; it is a
	// wiring fault inside pasture, and naming it as one stops an operator
	// hunting for a flag that does not exist. A refused format IS an operator
	// mistake, so that refusal names the value it was given, the values it
	// accepts, and what each one prints. The order of the checks is unchanged.
	if ctx == nil || out == nil {
		return listResult(fmt.Errorf(
			"count orphan lifecycle payloads: pasture called the orphan report without a context or " +
				"without an output writer, so nothing could be counted or printed. This is a wiring " +
				"fault inside pasture (internal/handlers.HookLifecycleOrphans), not something a " +
				"command line can cause; report it with the command you ran"))
	}
	if format != "text" && format != "json" {
		return listResult(fmt.Errorf(
			"count orphan lifecycle payloads: --format %q is not a format this command can print, "+
				"so no store was opened and nothing was counted. The accepted values are text and "+
				"json. Re-run with --format text to read the count with the sentence that says what "+
				"it means, or --format json to read the same two fields as one object",
			format))
	}
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return listResult(err)
	}
	defer tracker.Close()
	reclaim, err := tasks.RebuildLifecycleOccurrencesReporting(ctx, tracker)
	if err != nil {
		return listResult(err)
	}
	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	if err != nil {
		return listResult(err)
	}
	count, err := blobs.ReclaimableCount(ctx)
	if err != nil {
		return listResult(err)
	}

	if format == "json" {
		if err := json.NewEncoder(out).Encode(orphanPayloadView{ReclaimedThisRun: int64(reclaim.Count()), Remaining: count, Note: OrphanPayloadNote}); err != nil {
			return listResult(err)
		}
		return 0, nil
	}
	if _, err := fmt.Fprintf(out, "orphan payload blobs reclaimed by this run: %d\norphan payload blobs remaining: %d\nwhat these numbers mean: %s\n", reclaim.Count(), count, OrphanPayloadNote); err != nil {
		return listResult(err)
	}
	return 0, nil
}

// orphanPayloadView carries the same facts to a machine reader: what this run
// reclaimed, what remains, and the note. The note travels with the numbers in
// JSON as well, because a dashboard that renders a count alone reproduces the
// misreading this text exists to prevent.
type orphanPayloadView struct {
	ReclaimedThisRun int64  `json:"orphanPayloadBlobsReclaimedThisRun"`
	Remaining        int64  `json:"orphanPayloadBlobsRemaining"`
	Note             string `json:"note"`
}

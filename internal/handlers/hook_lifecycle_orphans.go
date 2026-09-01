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
	"A large number therefore does not mean the store is corrupt. It means invocations were abandoned repeatedly, so the thing to investigate is the store contention that caused the abandonment, such as another writer holding the pasture store."

// HookLifecycleOrphansInput selects the store to inspect.
type HookLifecycleOrphansInput struct {
	DBPath string
}

// HookLifecycleOrphans reports how many committed payload blobs no occurrence
// names. It deletes nothing and it changes no journal truth.
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
	if ctx == nil || out == nil || (format != "text" && format != "json") {
		return listResult(fmt.Errorf("count orphan lifecycle payloads: context, output, and format text|json are required"))
	}
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return listResult(err)
	}
	defer tracker.Close()
	if err := tasks.RebuildLifecycleOccurrences(ctx, tracker); err != nil {
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
		if err := json.NewEncoder(out).Encode(orphanPayloadView{Count: count, Note: OrphanPayloadNote}); err != nil {
			return listResult(err)
		}
		return 0, nil
	}
	if _, err := fmt.Fprintf(out, "orphan payload blobs: %d\nwhat this number means: %s\n", count, OrphanPayloadNote); err != nil {
		return listResult(err)
	}
	return 0, nil
}

// orphanPayloadView carries the same two facts to a machine reader. The note
// travels with the count in JSON as well, because a dashboard that renders the
// number alone reproduces the misreading this text exists to prevent.
type orphanPayloadView struct {
	Count int64  `json:"orphanPayloadBlobs"`
	Note  string `json:"note"`
}

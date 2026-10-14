package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// PruneRetentionNote is the whole meaning of the prune, printed beside its
// every report. The command DELETES rows, so the operator must leave knowing
// what class of row was deleted, what class was kept, and what is genuinely
// lost — not only how many rows went away.
//
// It ships with the numbers for the same reason the orphan note does: a bare
// "reclaimed 24416" invites the reader to guess whether their recorded
// lifecycle evidence survived. It did. What was deleted is the other side of
// the write order's boundary: a payload blob no recorded occurrence names,
// which is a blob an abandoned hook invocation left between its two durable
// writes. No occurrence points at those bytes, so no record loses evidence.
const PruneRetentionNote = "A pruned blob is an orphan payload blob: a raw host payload that no recorded occurrence names, left by a hook invocation abandoned between its two durable writes. " +
	"Only orphan payload blobs are deleted. Every blob a recorded occurrence names is kept at its exact original bytes, because the occurrence projection names its digest and dropping the blob would leave a recorded occurrence pointing at nothing; the newest blob in the store is kept whatever its age; and a blob whose written-at stamp is unknown is kept, because an unknown age never satisfies an age bound. " +
	"What is lost is the raw bytes of abandoned invocations only. Every recorded occurrence keeps its digest, contract, event, bindings, envelope and interpreted evidence in the journal, and none of that is touched. " +
	"Deleting rows returns their pages to SQLite's free list, where new writes reuse them; the database FILE only shrinks after VACUUM, which this command runs only when asked with --vacuum. " +
	"THIS IS A HYGIENE RECLAIM AND NOT A CAPACITY FIX FOR A FULLY-REFERENCED STORE. It reclaims whatever orphans the store has left, which on a store that abandoned many invocations can be most of the blob bytes; and it reclaims nothing where every blob is named by a recorded occurrence, because every blob is then evidence. The bytes such a store holds are its audit history, and bounding them is a retention policy over the lifecycle occurrence journal itself, which this command does not implement and does not change."

// PruneVacuumAdvice is what the command tells an operator who did not ask for
// VACUUM: the reclaim is real, and the file has not shrunk yet.
const PruneVacuumAdvice = "Reclaimed pages are free for reuse but the database file has not shrunk. Run `pasture prune --vacuum` to rewrite the file and return those pages to the filesystem; VACUUM takes the store's write lock, so run it when no hook writer is active."

// PruneTruncatedAdvice is what the command tells an operator whose run stopped
// at its batch bound or byte budget.
const PruneTruncatedAdvice = "This run stopped at its bound with eligible orphan blobs still present. Run `pasture prune` again to reclaim the rest."

// PruneInput selects the store to prune and the bounds on the reclaim.
type PruneInput struct {
	// DBPath is the unified store; empty resolves to the default.
	DBPath string
	// Retain keeps orphan payload blobs younger than this. Zero keeps none
	// beyond the writer window, which is the read path's bound.
	Retain time.Duration
	// MaxBytes, when positive, stops the run once this many bytes were
	// reclaimed. Zero removes the size bound.
	MaxBytes int64
	// Vacuum runs VACUUM after reclaiming, returning the freed pages to the
	// filesystem.
	Vacuum bool
}

// PrunePayloads reclaims orphan lifecycle payload blobs and reports, in the
// operator's terms, what it deleted and what it kept. It is the explicit
// database-level garbage collection for the payload blob table; the read
// commands keep their small incremental reclaim unchanged.
func PrunePayloads(ctx context.Context, out io.Writer, in PruneInput, format string) (int, error) {
	// The two refusals are separated because they have different readers: a
	// missing context or writer cannot be produced from a terminal and is a
	// wiring fault inside pasture, while a bad bound or format IS an operator
	// mistake and must name the value and the accepted values.
	if ctx == nil || out == nil {
		return listResult(fmt.Errorf(
			"prune lifecycle payload blobs: pasture called the prune without a context or without an " +
				"output writer, so nothing could be reclaimed or printed. This is a wiring fault inside " +
				"pasture (internal/handlers.PrunePayloads), not something a command line can cause; " +
				"report it with the command you ran"))
	}
	if format != "text" && format != "json" {
		return listResult(fmt.Errorf(
			"prune lifecycle payload blobs: --format %q is not a format this command can print, so "+
				"no store was opened and nothing was deleted. The accepted values are text and json. "+
				"Re-run with --format text to read the reclaim with the sentence that says what was "+
				"kept and what was lost, or --format json to read the same facts as one object",
			format))
	}
	if in.Retain < 0 || in.MaxBytes < 0 {
		return listResult(fmt.Errorf(
			"prune lifecycle payload blobs: a retention age and a byte budget are a duration and a "+
				"count, and this run was given %s and %d. Pass --max-age of zero or more (zero drops "+
				"every orphan past the writer-window safety floor) and --max-bytes of zero (no budget) "+
				"or more",
			in.Retain, in.MaxBytes))
	}
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return listResult(err)
	}
	defer tracker.Close()
	outcome, err := tasks.PruneLifecyclePayloads(ctx, tracker, tasks.PruneOptions{Retain: in.Retain, MaxBytes: in.MaxBytes, Vacuum: in.Vacuum})
	if err != nil {
		return listResult(err)
	}
	return renderPrune(out, outcome, in, format)
}

func renderPrune(out io.Writer, outcome tasks.PruneOutcome, in PruneInput, format string) (int, error) {
	vacuumFailure := ""
	if outcome.VacuumFailure != nil {
		vacuumFailure = outcome.VacuumFailure.Error()
	}
	if format == "json" {
		view := pruneView{
			OrphanPayloadBlobsReclaimed:     outcome.Reclaimed,
			OrphanPayloadBlobBytesReclaimed: outcome.ReclaimedBytes,
			Batches:                         outcome.Batches,
			Truncated:                       outcome.Truncated,
			VacuumRequested:                 in.Vacuum,
			Vacuumed:                        outcome.Vacuumed,
			VacuumFailure:                   vacuumFailure,
			Note:                            PruneRetentionNote,
		}
		if outcome.Truncated {
			view.Advice = append(view.Advice, PruneTruncatedAdvice)
		}
		if in.Vacuum && outcome.VacuumFailure != nil {
			view.Advice = append(view.Advice, fmt.Sprintf("VACUUM did not run: %v. The reclaim is already committed and its pages are free for reuse; retry `pasture prune --vacuum` when no other process holds the store.", outcome.VacuumFailure))
		} else if !in.Vacuum {
			view.Advice = append(view.Advice, PruneVacuumAdvice)
		}
		if err := json.NewEncoder(out).Encode(view); err != nil {
			return listResult(err)
		}
		return 0, nil
	}
	if _, err := fmt.Fprintf(out, "orphan payload blobs reclaimed: %d\norphan payload blob bytes reclaimed: %d\n", outcome.Reclaimed, outcome.ReclaimedBytes); err != nil {
		return listResult(err)
	}
	if outcome.Truncated {
		if _, err := fmt.Fprintf(out, "this run stopped at its bound; run `pasture prune` again to reclaim the rest\n"); err != nil {
			return listResult(err)
		}
	}
	if in.Vacuum && outcome.VacuumFailure != nil {
		if _, err := fmt.Fprintf(out, "VACUUM did not run: %v\nthe reclaim is already committed and its pages are free for reuse; retry `pasture prune --vacuum` when no other process holds the store\n", outcome.VacuumFailure); err != nil {
			return listResult(err)
		}
	} else if !in.Vacuum {
		if _, err := fmt.Fprintf(out, "%s\n", PruneVacuumAdvice); err != nil {
			return listResult(err)
		}
	}
	if _, err := fmt.Fprintf(out, "what this means: %s\n", PruneRetentionNote); err != nil {
		return listResult(err)
	}
	return 0, nil
}

// pruneView carries the same facts to a machine reader. The note travels with
// the numbers so a dashboard that renders a count alone cannot teach the
// misreading the sentence exists to prevent: the operator must be able to tell
// a reclaimed orphan from deleted recorded evidence.
type pruneView struct {
	OrphanPayloadBlobsReclaimed     int64    `json:"orphanPayloadBlobsReclaimed"`
	OrphanPayloadBlobBytesReclaimed int64    `json:"orphanPayloadBlobBytesReclaimed"`
	Batches                         int      `json:"batches"`
	Truncated                       bool     `json:"truncated"`
	VacuumRequested                 bool     `json:"vacuumRequested"`
	Vacuumed                        bool     `json:"vacuumed"`
	VacuumFailure                   string   `json:"vacuumFailure,omitempty"`
	Note                            string   `json:"note"`
	Advice                          []string `json:"advice,omitempty"`
}

package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

var pruneMaxAge time.Duration
var pruneMaxBytes int64
var pruneVacuum bool

// pruneCmd reclaims the orphan payload blobs an abandoned hook invocation
// leaves behind, in bulk, and reports exactly what it kept and what it lost.
//
// It is a TOP-LEVEL command, beside migrate, because it is a database-level
// operation rather than a lifecycle read: it takes the store's write lock and
// may run VACUUM. The read commands keep their small incremental reclaim
// unchanged; this verb exists for the backlog those bounded runs cannot drain
// in one sitting, and for the file shrink only VACUUM can deliver.
//
// IT IS A HYGIENE RECLAIM, AND IT IS NOT A CAPACITY FIX FOR A FULLY-REFERENCED
// STORE, and the help says both. It deletes only blobs NO occurrence names: it
// reclaims whatever orphans the store has left — often most of the blob bytes
// on a store that abandoned many invocations — and reclaims NOTHING where every
// blob is referenced. The bytes such a store holds are its recorded evidence,
// and bounding those is a retention policy over the lifecycle occurrence
// journal itself, which this command neither implements nor changes.
var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Reclaim unreferenced lifecycle payload blobs (a hygiene reclaim, not a capacity fix for a fully-referenced store)",
	Long: `Reclaim the orphan payload blobs the lifecycle store has accumulated.

A lifecycle hook writes each host payload to the store before it appends the
journal row that records the event. An invocation abandoned between those two
writes leaves a payload blob that no occurrence names. That blob is reclaimable
and, on a busy or contended store, they accumulate.

This command deletes those orphan blobs and nothing else. It never deletes a
blob that a recorded occurrence names, because the occurrence points at that
digest and its exact original bytes are the recorded evidence; it never deletes
the newest blob in the store, whatever its age; and it never deletes a blob
whose written-at stamp is unknown, because an unknown age never satisfies an
age bound. What is lost is the raw bytes of abandoned invocations only: every
recorded occurrence keeps its digest, contract, event, bindings, envelope and
interpreted evidence in the journal.

IT IS A HYGIENE RECLAIM AND NOT A CAPACITY FIX FOR A FULLY-REFERENCED STORE.
It reclaims whatever orphans the store has left, which on a store that abandoned
many invocations can be most of the blob bytes; and it reclaims nothing where
every blob is referenced by a recorded occurrence, because every one of them is
evidence. The bytes such a store holds are its audit history, and bounding that
is a retention policy over the lifecycle occurrence journal itself — an operator
policy decision this command neither implements nor changes.

Deleting rows returns their pages to SQLite's free list for reuse. The database
file only shrinks after VACUUM, which --vacuum runs. VACUUM rewrites the whole
file and takes the store's write lock, so run it when no hook writer is active.

The command prints how many orphan blobs and bytes it reclaimed, whether it
stopped at its bound with more remaining, and the sentence that says what was
deleted and what was kept. --max-age keeps a recent window of orphans for
inspection; --max-bytes bounds how much one run reclaims.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		code, err := handlers.PrunePayloads(cmd.Context(), cmd.OutOrStdout(), handlers.PruneInput{
			DBPath:   flagDBPath,
			Retain:   pruneMaxAge,
			MaxBytes: pruneMaxBytes,
			Vacuum:   pruneVacuum,
		}, flagFormat)
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

func init() {
	pruneCmd.Flags().DurationVar(&pruneMaxAge, "max-age", 0,
		"Keep orphan payload blobs younger than this; 0 (default) keeps none beyond the writer-window safety floor")
	pruneCmd.Flags().Int64Var(&pruneMaxBytes, "max-bytes", 0,
		"Stop once this many bytes of orphan payload blobs have been reclaimed; 0 (default) has no size bound")
	pruneCmd.Flags().BoolVar(&pruneVacuum, "vacuum", false,
		"Run VACUUM after reclaiming, returning the freed pages to the filesystem (takes the store's write lock)")
	rootCmd.AddCommand(pruneCmd)
}

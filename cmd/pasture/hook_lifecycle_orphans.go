package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleOrphansInput handlers.HookLifecycleOrphansInput
var hookLifecycleOrphansFormat string

// hookLifecycleOrphansCmd reports how many payload blobs no occurrence names.
//
// It sits beside `list` and `manifest`, on the read side of the lifecycle
// surface, and never on the hook path. A hook invocation runs under a deadline
// that exists so a host is never left waiting, and this count reads the store,
// which is the resource that contends. Asking the question during an invocation
// would spend that deadline on something no host asked for, and would be
// slowest precisely when orphans are being produced.
var hookLifecycleOrphansCmd = &cobra.Command{
	Use:   "orphans",
	Short: "Reclaim and count the payload blobs that no recorded occurrence names",
	Long: `Reclaim and count the payload blobs that no recorded occurrence names.

An orphan is a payload blob that no recorded occurrence names. One is left
behind by a hook invocation that was abandoned between its two durable writes,
and at most one arises per abandoned invocation. It is expected and reclaimable,
not damage: the blob is written before the journal row deliberately, because a
spare blob can be reclaimed later while a journal row naming an absent blob
could not be repaired at all.

A large number does not mean the store is corrupt. It means invocations were
abandoned repeatedly, so the thing to investigate is the store contention that
caused the abandonment.

The command changes no journal truth, but it is not a pure read. Like every
read command it rebuilds the disposable occurrence projection from the journal
first, because a count taken against a projection that was never rebuilt would
report every blob as an orphan; and inside that rebuild it reclaims orphan
blobs older than the writer window, at most 1024 per run. So it prints TWO
numbers, each true of the run that printed it: how many blobs this run
reclaimed, and how many remain.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		hookLifecycleOrphansInput.DBPath = flagDBPath
		code, err := handlers.HookLifecycleOrphans(cmd.Context(), cmd.OutOrStdout(), hookLifecycleOrphansInput, hookLifecycleOrphansFormat)
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
	hookLifecycleOrphansCmd.Flags().StringVar(&hookLifecycleOrphansFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleOrphansCmd)
}

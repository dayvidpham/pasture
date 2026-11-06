package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// rebuildCmd regenerates derived data from the durable journal. It is the
// counterpart of migrate: migrate changes the durable schema, rebuild
// regenerates what is derived from it.
var rebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Regenerate derived data from the journal",
	Long: `Regenerate derived data from the durable journal.

Derived data is disposable: the journal is the source of truth. A rebuild
replays it and republishes the projection, so it is safe to run at any time.
It never creates a store, never records journal evidence, and never contacts a
running daemon.

"migrate" changes the durable schema; "rebuild" regenerates what is derived
from it.`,
}

var rebuildSessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Rebuild the disposable session cache",
	Long: `Rebuild the disposable session cache from the lifecycle journal.

Without --full the command does what a read does: it repairs a dirty, absent or
old-layout cache in full and applies only the suffix when the cache is already
current. With --full it discards the cache and replays the whole journal, which
takes longer on a large store.

The command requires an existing store and never creates one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		full, _ := cmd.Flags().GetBool("full")
		code, err := handlers.RebuildSession(cmd.Context(), handlers.RebuildSessionInput{
			DBPath: flagDBPath,
			Full:   full,
			Output: cmd.OutOrStdout(),
		}, resolveFormat())
		return finishCommand(code, err)
	},
}

func init() {
	rebuildSessionCmd.Flags().Bool("full", false, "Discard the cache and replay the whole journal")
	rebuildCmd.AddCommand(rebuildSessionCmd)
	rootCmd.AddCommand(rebuildCmd)
}

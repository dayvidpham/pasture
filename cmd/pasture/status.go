package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// statusCmd shows the current state of the epoch workflow engine. Without
// --epoch-id it lists all recorded epochs; with --epoch-id it shows a full
// detail view for that epoch.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of recorded epoch workflows",
	Long: `Show the status of epoch workflows recorded in the local pasture database.

Without --epoch-id: lists all known epochs with their current phase and event count.

With --epoch-id: shows the full detail view for that epoch — current phase, agent
role, available transitions (recomputed from the protocol state machine), slice
progress, active sessions, the most recent audit events, and any cancellation
reason recorded by an operator.

All output reads directly from the local pasture database (read-only) — no
running daemon is contacted and no state is changed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")

		code, err := handlers.EpochStatus(handlers.EpochStatusInput{
			DBPath:  flagDBPath,
			EpochId: epochId,
		}, resolveFormat())
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
	statusCmd.Flags().String("epoch-id", "", "Epoch ID to inspect (omit to list all epochs)")
	rootCmd.AddCommand(statusCmd)
}

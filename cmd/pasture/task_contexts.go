package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskContextsCmd implements `pasture task contexts <event-id>` (PROPOSAL-2 §7.9).
var taskContextsCmd = &cobra.Command{
	Use:   "contexts EVENT-ID",
	Short: "List all context_edges attached to an audit event",
	Long: `Show every (Kind, ContextId) edge attached to one audit event.

EVENT-ID is the integer audit_events.id (the AUTOINCREMENT primary key);
discover IDs via 'pasture task events'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, hErr := handlers.TaskContexts(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
		if hErr != nil {
			printError(hErr)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskContextsCmd)
}

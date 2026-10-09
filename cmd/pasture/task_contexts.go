package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskContextsCmd implements `pasture task contexts <event-id>`.
var taskContextsCmd = &cobra.Command{
	Use:   "contexts EVENT-ID",
	Short: "List all context links attached to an audit event",
	Long: `Show every (kind, context-id) link attached to one audit event.

EVENT-ID is the integer event ID shown by 'pasture task events'; discover IDs
there. Each link names another object the event is attached to, such as an
epoch, a commit, a skill run, or a session.`,
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

package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskTimelineCmd implements `pasture task timeline <task-id>` (PROPOSAL-2 §7.9).
var taskTimelineCmd = &cobra.Command{
	Use:   "timeline TASK-ID",
	Short: "Show all events for a task in chronological order",
	Long:  `Show all audit events tied to a task ID, ordered by timestamp.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in := handlers.TaskTimelineInput{
			DBPath:    flagDBPath,
			TaskIDStr: args[0],
		}
		code, hErr := handlers.TaskTimeline(cmd.OutOrStdout(), in, resolveFormat())
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
	taskCmd.AddCommand(taskTimelineCmd)
}

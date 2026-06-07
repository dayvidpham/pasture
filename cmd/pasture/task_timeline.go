package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskTimelineCmd implements `pasture task timeline <task-id>` (PROPOSAL-2 §7.9).
var taskTimelineCmd = &cobra.Command{
	Use:   "timeline TASK-ID",
	Short: "Show all events for a task in chronological order",
	Long: `Show all audit events tied to a task ID, ordered by timestamp.

The handler queries both the context_edges JOIN (current source of truth)
AND the legacy audit_events.epoch_id column (v1/v2 fallback) and merges the
result, so the timeline works against any database version.

--include-children and --depth are accepted for forward compatibility but are
currently no-op; child-task traversal is not yet implemented.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in := handlers.TaskTimelineInput{
			DBPath:    flagDBPath,
			TaskIDStr: args[0],
		}
		if cmd.Flags().Changed("include-children") {
			in.IncludeChildren, _ = cmd.Flags().GetBool("include-children")
		}
		if cmd.Flags().Changed("depth") {
			in.Depth, _ = cmd.Flags().GetInt("depth")
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
	taskTimelineCmd.Flags().Bool("include-children", false,
		"Include events for child tasks (currently no-op; not yet implemented)")
	taskTimelineCmd.Flags().Int("depth", 0,
		"Max child traversal depth (currently no-op; not yet implemented)")

	taskCmd.AddCommand(taskTimelineCmd)
}

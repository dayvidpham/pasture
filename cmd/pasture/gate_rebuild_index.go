package main

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/spf13/cobra"
)

func newGateRebuildIndexCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "rebuild-index", Short: "Recover the assignment index from authenticated task history",
		Long: "Recover assignment coverage through one fixed journal snapshot. Incomplete scans resume from their durable cursor. A dirty generation requires --reset; reset retires only the derived index, never task history.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reset, err := cmd.Flags().GetBool("reset")
			if err != nil {
				return err
			}
			tracker, err := tasks.OpenTaskTracker(flagDBPath)
			if err != nil {
				return err
			}
			defer tracker.Close()
			options := tasks.AssignmentIndexRebuildOptions{Reset: reset}
			if cmd.Flags().Changed("generation") {
				value, err := cmd.Flags().GetInt64("generation")
				if err != nil {
					return err
				}
				options.ExpectedGeneration = &value
			}
			if err := tasks.RebuildAssignmentIndex(cmd.Context(), tracker, options); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Assignment index rebuilt and its captured journal prefix verified.")
			return err
		},
	}
	command.Flags().Bool("reset", false, "Start a new recovery generation from zero, retiring the derived index only")
	command.Flags().Int64("generation", 0, "Refuse the operation if the current generation differs from this value")
	return command
}

func init() {
	gate := &cobra.Command{Use: "gate", Short: "Maintain the derived assignment index used by task gates"}
	gate.AddCommand(newGateRebuildIndexCommand())
	rootCmd.AddCommand(gate)
}

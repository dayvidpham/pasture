package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// sliceCmd groups slice sub-workflow configuration and completion override verbs.
var sliceCmd = &cobra.Command{
	Use:   "slice",
	Short: "Configure and complete slice sub-workflows",
	Long: `Configure how a slice sub-workflow executes, or record its final outcome.

Slice sub-workflows are addressed by their slice workflow id (--slice-id).
Signals are delivered to the running sub-workflow; the parent epoch
receives progress updates through the slice_progress channel.`,
}

var sliceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Configure a slice sub-workflow's execution mode",
	Long: `Send a start-slice signal to a slice sub-workflow.

The --mode flag selects the execution strategy:
  mock        Run the slice as a no-op (useful for testing).
  tmux        Spawn an agent session in a new tmux window.
  subprocess  Run a shell command to completion.

For tmux and subprocess modes, --command provides the command to run.
--timeout overrides the default start-to-close timeout (in seconds).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sliceId, _ := cmd.Flags().GetString("slice-id")
		mode, _ := cmd.Flags().GetString("mode")
		command, _ := cmd.Flags().GetString("command")
		timeout, _ := cmd.Flags().GetInt("timeout")
		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.SliceStart(ctrl, sliceId, mode, command, timeout, resolveFormat())
		})
	},
}

var sliceCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Record the final outcome of a slice sub-workflow",
	Long: `Send a complete-slice signal to override a slice sub-workflow's outcome.

Use --output to record a successful completion with an optional message.
Use --error to record a failure with an error description.

--output and --error are mutually exclusive.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sliceId, _ := cmd.Flags().GetString("slice-id")

		var output, errMsg *string
		if cmd.Flags().Changed("output") {
			v, _ := cmd.Flags().GetString("output")
			output = &v
		}
		if cmd.Flags().Changed("error") {
			v, _ := cmd.Flags().GetString("error")
			errMsg = &v
		}

		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.SliceComplete(ctrl, sliceId, output, errMsg, resolveFormat())
		})
	},
}

func init() {
	sliceStartCmd.Flags().String("slice-id", "", "Slice workflow ID to configure (required)")
	sliceStartCmd.Flags().String("mode", "", "Execution mode: mock, tmux, or subprocess (required)")
	sliceStartCmd.Flags().String("command", "", "Shell command for tmux or subprocess mode")
	sliceStartCmd.Flags().Int("timeout", 0, "Start-to-close timeout in seconds (0 uses the default)")
	_ = sliceStartCmd.MarkFlagRequired("slice-id")
	_ = sliceStartCmd.MarkFlagRequired("mode")

	sliceCompleteCmd.Flags().String("slice-id", "", "Slice workflow ID to complete (required)")
	sliceCompleteCmd.Flags().String("output", "", "Success message (mutually exclusive with --error)")
	sliceCompleteCmd.Flags().String("error", "", "Failure reason (mutually exclusive with --output)")
	_ = sliceCompleteCmd.MarkFlagRequired("slice-id")

	sliceCmd.AddCommand(sliceStartCmd, sliceCompleteCmd)
	rootCmd.AddCommand(sliceCmd)
}

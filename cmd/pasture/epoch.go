package main

import (
	"github.com/spf13/cobra"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// runWithController opens a durable epoch controller on the resolved database,
// runs fn against it, and maps the result onto the process exit code. It
// centralizes the open/close/exit handling every lifecycle and signal verb
// shares so each command body stays a thin argument-marshalling step.
func runWithController(fn func(handlers.EpochController) (int, error)) error {
	ctrl, err := handlers.OpenEpochController(flagDBPath)
	if err != nil {
		printError(err)
		exitWithCode(pasterrors.ExitCode(err))
		return nil
	}
	defer ctrl.Close()

	code, hErr := fn(ctrl)
	if hErr != nil {
		printError(hErr)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}

// epochCmd groups epoch lifecycle verbs.
var epochCmd = &cobra.Command{
	Use:   "epoch",
	Short: "Manage epoch lifecycle (start, cancel)",
	Long: `Start and stop durable epoch workflows.

An epoch's id is its task id; signals address the running epoch by that id. The
durable epoch runs on the local pasture database.`,
}

var epochStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new durable epoch",
	Long: `Start the durable control workflow for an epoch.

The --epoch-id must be a valid task id ("<project>--<uuid>"); create one with
"pasture task create" first if needed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.EpochStart(ctrl, epochId, resolveFormat())
		})
	},
}

var epochCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel a running epoch",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.EpochCancel(ctrl, epochId, resolveFormat())
		})
	},
}

// epochTerminateCmd stops a running epoch and records an operator note (reason)
// in the audit trail before cancelling. When --reason is not provided, the
// audit event is still written with an empty reason.
var epochTerminateCmd = &cobra.Command{
	Use:   "terminate",
	Short: "Terminate a running epoch with an optional operator reason",
	Long: `Stop a running epoch and record the reason in the audit trail.

The --reason flag lets the operator attach a plain-language note explaining why
the epoch was stopped. The note is written to the audit trail before the epoch
is cancelled, so it is preserved even when the cancellation targets a wedged
workflow. When --reason is omitted, the audit event is still written (with an
empty reason).

For a plain cancel without an audit record, use "pasture epoch cancel".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		reason, _ := cmd.Flags().GetString("reason")
		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.EpochTerminate(ctrl, epochId, reason, resolveFormat())
		})
	},
}

func init() {
	epochStartCmd.Flags().String("epoch-id", "", "Epoch ID (a task id) to start (required)")
	_ = epochStartCmd.MarkFlagRequired("epoch-id")

	epochCancelCmd.Flags().String("epoch-id", "", "Epoch ID to cancel (required)")
	_ = epochCancelCmd.MarkFlagRequired("epoch-id")

	epochTerminateCmd.Flags().String("epoch-id", "", "Epoch ID to terminate (required)")
	epochTerminateCmd.Flags().String("reason", "", "Reason for termination (operator note)")
	_ = epochTerminateCmd.MarkFlagRequired("epoch-id")

	epochCmd.AddCommand(epochStartCmd, epochCancelCmd, epochTerminateCmd)
	rootCmd.AddCommand(epochCmd)
}

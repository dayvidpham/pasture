package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// epochCmd is the "epoch" subcommand group.
var epochCmd = &cobra.Command{
	Use:   "epoch",
	Short: "Epoch lifecycle management",
	Long:  "Manage epoch workflow lifecycle: start, cancel, or terminate a running epoch.",
}

// epochStartCmd implements "pasture-msg epoch start".
var epochStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new epoch workflow",
	Long: `Start a new epoch workflow in the pastured daemon.

The epoch-id becomes the Temporal workflow ID, so it must be unique within
the namespace. If an epoch with this ID is already running, the command fails
with exit code 3.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig(cmd)
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		description, _ := cmd.Flags().GetString("description")
		taskQueue, _ := cmd.Flags().GetString("task-queue-override")

		code, err := handlers.EpochStart(
			context.Background(),
			cfg.Connection,
			epochID, description, taskQueue,
			format,
			nil, // DefaultClientFactory
		)
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// epochCancelCmd implements "pasture-msg epoch cancel".
var epochCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Request graceful cancellation of a running epoch workflow",
	Long: `Request graceful cancellation of a running epoch workflow.

The workflow receives a cancellation request and can perform cleanup before
stopping. For immediate (non-graceful) termination, use "epoch terminate".`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig(cmd)
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")

		code, err := handlers.EpochCancel(
			context.Background(),
			cfg.Connection,
			epochID,
			format,
			nil,
		)
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// epochTerminateCmd implements "pasture-msg epoch terminate".
var epochTerminateCmd = &cobra.Command{
	Use:   "terminate",
	Short: "Immediately terminate a running epoch workflow",
	Long: `Immediately terminate a running epoch workflow (non-graceful).

Unlike "cancel", terminate stops the workflow immediately without giving it a
chance to run cleanup handlers. Provide a descriptive reason so the audit trail
is informative.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig(cmd)
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		reason, _ := cmd.Flags().GetString("reason")

		code, err := handlers.EpochTerminate(
			context.Background(),
			cfg.Connection,
			epochID, reason,
			format,
			nil,
		)
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
	// epoch start flags
	epochStartCmd.Flags().String("epoch-id", "", "Epoch workflow ID (required)")
	epochStartCmd.Flags().String("description", "", "Human-readable epoch description")
	epochStartCmd.Flags().String("task-queue-override", "", "Override task queue for this epoch (default: from config)")
	_ = epochStartCmd.MarkFlagRequired("epoch-id")

	// epoch cancel flags
	epochCancelCmd.Flags().String("epoch-id", "", "Epoch workflow ID to cancel (required)")
	_ = epochCancelCmd.MarkFlagRequired("epoch-id")

	// epoch terminate flags
	epochTerminateCmd.Flags().String("epoch-id", "", "Epoch workflow ID to terminate (required)")
	epochTerminateCmd.Flags().String("reason", "", "Reason for termination (optional)")
	_ = epochTerminateCmd.MarkFlagRequired("epoch-id")

	// Register subcommands
	epochCmd.AddCommand(epochStartCmd)
	epochCmd.AddCommand(epochCancelCmd)
	epochCmd.AddCommand(epochTerminateCmd)

	rootCmd.AddCommand(epochCmd)
}

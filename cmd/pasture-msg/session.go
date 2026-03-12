package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// sessionCmd is the "session" subcommand group.
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Session registration",
	Long:  "Register a Claude Code session with a running epoch for observability.",
}

// sessionRegisterCmd implements "pasture-msg session register".
var sessionRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a session with an epoch",
	Long: `Register a Claude Code session with a running epoch workflow.

Sends a RegisterSessionSignal to the EpochWorkflow. Duplicate session-id
registrations are silently ignored by the workflow (idempotent).

The model-harness identifies the runtime harness (e.g., claude-code).
The model identifies the specific model version (e.g., claude-sonnet-4-6).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resolveConfig(cmd)
		if err != nil {
			return err
		}
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")
		sessionID, _ := cmd.Flags().GetString("session-id")
		role, _ := cmd.Flags().GetString("role")
		modelHarness, _ := cmd.Flags().GetString("model-harness")
		model, _ := cmd.Flags().GetString("model")

		code, err := handlers.SessionRegister(
			context.Background(),
			cfg.Connection,
			epochID, sessionID, role, modelHarness, model,
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
	sessionRegisterCmd.Flags().String("epoch-id", "", "Epoch workflow ID (required)")
	sessionRegisterCmd.Flags().String("session-id", "", "Unique session identifier (required)")
	sessionRegisterCmd.Flags().String("role", "", "Session role (e.g., worker, supervisor, reviewer) (required)")
	sessionRegisterCmd.Flags().String("model-harness", "", "Runtime harness name (e.g., claude-code)")
	sessionRegisterCmd.Flags().String("model", "", "Model version (e.g., claude-sonnet-4-6)")
	_ = sessionRegisterCmd.MarkFlagRequired("epoch-id")
	_ = sessionRegisterCmd.MarkFlagRequired("session-id")
	_ = sessionRegisterCmd.MarkFlagRequired("role")

	sessionCmd.AddCommand(sessionRegisterCmd)
	rootCmd.AddCommand(sessionCmd)
}

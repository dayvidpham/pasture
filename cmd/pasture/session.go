package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// sessionCmd groups session-registration verbs.
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Register sessions with an epoch",
}

var sessionRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a session with an epoch",
	Long: `Send a register-session signal to a running epoch.

Duplicate session-ids are ignored (idempotent). The model-harness identifies the
runtime (e.g. claude-code); the model identifies the version.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		epochId, _ := cmd.Flags().GetString("epoch-id")
		sessionId, _ := cmd.Flags().GetString("session-id")
		role, _ := cmd.Flags().GetString("role")
		modelHarness, _ := cmd.Flags().GetString("model-harness")
		model, _ := cmd.Flags().GetString("model")

		return runWithController(func(ctrl handlers.EpochController) (int, error) {
			return handlers.SessionRegister(ctrl, epochId, sessionId, role, modelHarness, model, resolveFormat())
		})
	},
}

func init() {
	sessionRegisterCmd.Flags().String("epoch-id", "", "Epoch ID (required)")
	sessionRegisterCmd.Flags().String("session-id", "", "Unique session identifier (required)")
	sessionRegisterCmd.Flags().String("role", "", "Session role (worker, supervisor, reviewer, ...) (required)")
	sessionRegisterCmd.Flags().String("model-harness", "", "Runtime harness (e.g. claude-code)")
	sessionRegisterCmd.Flags().String("model", "", "Model version")
	_ = sessionRegisterCmd.MarkFlagRequired("epoch-id")
	_ = sessionRegisterCmd.MarkFlagRequired("session-id")
	_ = sessionRegisterCmd.MarkFlagRequired("role")

	sessionCmd.AddCommand(sessionRegisterCmd)
	rootCmd.AddCommand(sessionCmd)
}

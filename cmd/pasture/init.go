package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the local Pasture database",
	Long: `Create or upgrade the unified local Pasture database without starting the
background executor. This initializes the task, audit, projection, and durable
execution schemas and registers Pasture's built-in agents.

The command is idempotent and safe to run again against an initialized database.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.Initialize(cmd.Context(), cmd.OutOrStdout(), handlers.InitInput{
			DBPath: flagDBPath,
		}, resolveFormat())
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
	rootCmd.AddCommand(initCmd)
}

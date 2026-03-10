package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// queryCmd is the "query" subcommand group.
var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query workflow state",
	Long:  "Query the state of a running epoch workflow.",
}

// queryStateCmd implements "pasture-msg query state".
var queryStateCmd = &cobra.Command{
	Use:   "state",
	Short: "Query the current epoch state",
	Long: `Query the full state of a running epoch workflow.

Sends a full_state query to the Temporal workflow and prints the result.
Output includes current phase, role, vote history, available transitions,
and active session count.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig(cmd)
		format := resolveFormat(cmd, cfg)

		epochID, _ := cmd.Flags().GetString("epoch-id")

		code, err := handlers.QueryState(
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

func init() {
	queryStateCmd.Flags().String("epoch-id", "", "Epoch workflow ID to query (required)")
	_ = queryStateCmd.MarkFlagRequired("epoch-id")

	queryCmd.AddCommand(queryStateCmd)
	rootCmd.AddCommand(queryCmd)
}

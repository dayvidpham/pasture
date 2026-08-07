package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleCodebookInput handlers.HookLifecycleCodebookInput
var hookLifecycleCodebookFormat string

var hookLifecycleCodebookCmd = &cobra.Command{Use: "codebook", Short: "Show the active lifecycle interpretation codebook coordinate and whether it is journaled", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	hookLifecycleCodebookInput.DBPath = flagDBPath
	code, err := handlers.HookLifecycleCodebook(cmd.Context(), cmd.OutOrStdout(), hookLifecycleCodebookInput, hookLifecycleCodebookFormat)
	if err != nil {
		printError(err)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}}

func init() {
	f := hookLifecycleCodebookCmd.Flags()
	f.BoolVar(&hookLifecycleCodebookInput.Body, "body", false, "Include the canonical codebook body")
	f.StringVar(&hookLifecycleCodebookFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleCodebookCmd)
}

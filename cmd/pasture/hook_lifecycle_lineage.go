package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleLineageInput handlers.HookLifecycleLineageInput
var hookLifecycleLineageFormat string

var hookLifecycleLineageCmd = &cobra.Command{
	Use:   "lineage",
	Short: "Materialize and print the committed occurrence lineage for one native identity binding",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		hookLifecycleLineageInput.DBPath = flagDBPath
		hookLifecycleLineageInput.Clock = lifecycleCLIClock{}
		hookLifecycleLineageInput.Operations = lifecycleCLIOperations{}
		code, err := handlers.HookLifecycleLineage(cmd.Context(), cmd.OutOrStdout(), hookLifecycleLineageInput, hookLifecycleLineageFormat)
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
	f := hookLifecycleLineageCmd.Flags()
	f.StringVar(&hookLifecycleLineageInput.Binding, "binding", "", "Native identity binding to materialize: <kind>:<native-name>=<exact-value> (required)")
	f.StringVar(&hookLifecycleLineageFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleLineageCmd)
}

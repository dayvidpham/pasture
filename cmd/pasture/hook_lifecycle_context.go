package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleContextInput handlers.HookLifecycleContextInput
var hookLifecycleContextFormat string

var hookLifecycleContextCmd = &cobra.Command{
	Use:   "context",
	Short: "Disclose and durably record the bounded lifecycle context for one native identity binding",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		hookLifecycleContextInput.DBPath = flagDBPath
		hookLifecycleContextInput.Clock = lifecycleCLIClock{}
		hookLifecycleContextInput.Operations = lifecycleCLIOperations{}
		code, err := handlers.HookLifecycleContext(cmd.Context(), cmd.OutOrStdout(), hookLifecycleContextInput, hookLifecycleContextFormat)
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
	f := hookLifecycleContextCmd.Flags()
	f.StringVar(&hookLifecycleContextInput.Binding, "binding", "", "Native identity binding to disclose: <kind>:<native-name>=<exact-value> (required)")
	f.StringVar(&hookLifecycleContextFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleContextCmd)
}

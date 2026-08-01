package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleListInput handlers.HookLifecycleListInput
var hookLifecycleListFormat string

var hookLifecycleListCmd = &cobra.Command{Use: "list", Short: "List recorded lifecycle events", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	return handlers.HookLifecycleList(cmd.Context(), cmd.OutOrStdout(), hookLifecycleListInput, hookLifecycleListFormat)
}}

func init() {
	f := hookLifecycleListCmd.Flags()
	f.StringVar(&hookLifecycleListInput.DBPath, "db", "", "Unified Pasture database path")
	f.StringArrayVar(&hookLifecycleListInput.Contracts, "contract", nil, "Exact runtime contract filter")
	f.StringArrayVar(&hookLifecycleListInput.Events, "event", nil, "Exact event ordinal filter")
	f.StringArrayVar(&hookLifecycleListInput.Bindings, "binding", nil, "Exact binding filter (<kind>:<native-name>=<value>)")
	f.Uint16Var(&hookLifecycleListInput.PageSize, "page-size", 50, "Maximum records to return")
	f.StringVar(&hookLifecycleListFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleListCmd)
}

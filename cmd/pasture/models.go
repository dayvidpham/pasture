package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "models", Short: "Manage user-wide model allowances and catalog snapshots"}
	allow := &cobra.Command{Use: "allow", Short: "Allow an exact provider/model identity offline", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			provider, _ := cmd.Flags().GetString("provider")
			model, _ := cmd.Flags().GetString("model")
			return handlers.ModelsAllow(cmd.Context(), cmd.OutOrStdout(), provider, model, resolveFormat())
		}}
	allow.Flags().String("provider", "", "Exact provider name (no slash or hash)")
	allow.Flags().String("model", "", "Exact base model name (inner slashes supported; no #variant)")
	refresh := &cobra.Command{Use: "refresh", Short: "Fetch a model catalog snapshot (five-second request budget)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return handlers.ModelsRefresh(cmd.Context(), cmd.OutOrStdout(), resolveFormat())
		}}
	cmd.AddCommand(allow, refresh)
	return cmd
}

func init() { rootCmd.AddCommand(newModelsCmd()) }

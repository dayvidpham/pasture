package main

import (
	"github.com/spf13/cobra"
)

// bundleCmd is the parent of the verbs that work with Pasture's per-cell
// content bundles. Invoked bare it prints help rather than acting on an
// unstated intent.
var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Work with Pasture's per-cell content bundles",
	Long: `bundle groups the verbs that work with Pasture's per-cell content bundles: the
exact skills, agents, and hooks trees the installer activates for each harness.

  pasture bundle export --version 1.4.0 --out ./build/1.4.0

Invoked without a subcommand it prints this help.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(bundleCmd)
}

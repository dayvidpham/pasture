package main

import (
	"text/template"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show the pasture version",
		Args:  cobra.NoArgs,
		RunE:  runVersionCommand,
	})
}

func runVersionCommand(cmd *cobra.Command, _ []string) error {
	root := cmd.Root()
	// Use Cobra's version template and root data, just like --version.
	tmpl, err := template.New("version").Parse(root.VersionTemplate())
	if err != nil {
		return err
	}
	return tmpl.Execute(cmd.OutOrStdout(), root)
}

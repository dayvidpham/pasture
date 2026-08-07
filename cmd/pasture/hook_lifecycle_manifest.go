package main

import (
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/spf13/cobra"
)

var hookLifecycleManifestInput handlers.HookLifecycleMetamodelInput
var hookLifecycleManifestFormat string

var hookLifecycleManifestCmd = &cobra.Command{Use: "manifest", Short: "Show the active lifecycle metamodel manifest and whether it is journaled", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	hookLifecycleManifestInput.DBPath = flagDBPath
	code, err := handlers.HookLifecycleMetamodel(cmd.Context(), cmd.OutOrStdout(), hookLifecycleManifestInput, hookLifecycleManifestFormat)
	if err != nil {
		printError(err)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}}

func init() {
	f := hookLifecycleManifestCmd.Flags()
	f.BoolVar(&hookLifecycleManifestInput.Body, "body", false, "Include the canonical metamodel body")
	f.StringVar(&hookLifecycleManifestFormat, "format", "text", "Output format: text or json")
	hookLifecycleCmd.AddCommand(hookLifecycleManifestCmd)
}

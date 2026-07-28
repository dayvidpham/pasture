package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskRelationCmd groups generic Provenance relationship operations. Epoch
// lifecycle relationships remain exclusively owned by EpochService commands.
var taskRelationCmd = &cobra.Command{
	Use:   "relation",
	Short: "Manage typed relationships between tasks",
}

var taskRelationAddCmd = &cobra.Command{
	Use:   "add SOURCE",
	Short: "Add a typed relationship from SOURCE to TARGET",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("target")
		kindText, _ := cmd.Flags().GetString("kind")
		if target == "" {
			err := fmt.Errorf("task relation add requires --target: specify the task or provenance target related to %q", args[0])
			printError(err)
			exitWithCode(1)
		}
		if kindText == "" {
			err := fmt.Errorf("task relation add requires --kind: use blocked_by, derived_from, supersedes, or discovered_from")
			printError(err)
			exitWithCode(1)
		}
		var kind provenance.EdgeKind
		if err := kind.UnmarshalText([]byte(kindText)); err != nil {
			printError(fmt.Errorf("task relation add rejected --kind %q: use blocked_by, derived_from, supersedes, or discovered_from: %w", kindText, err))
			exitWithCode(1)
		}
		code, err := handlers.TaskDepAdd(cmd.OutOrStdout(), flagDBPath, args[0], target, kind, resolveFormat())
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
	taskRelationAddCmd.Flags().String("target", "", "Target task or provenance identity")
	taskRelationAddCmd.Flags().String("kind", "", "Relationship kind: blocked_by, derived_from, supersedes, or discovered_from")
	taskRelationCmd.AddCommand(taskRelationAddCmd)
	taskCmd.AddCommand(taskRelationCmd)
}

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskDepCmd groups generic Provenance relationship operations. Epoch
// lifecycle relationships remain exclusively owned by EpochService commands.
var taskDepCmd = &cobra.Command{
	Use:     "dep",
	Aliases: []string{"relation"},
	Short:   "Manage typed relationships between tasks",
}

var taskDepAddCmd = &cobra.Command{
	Use:   "add SOURCE",
	Short: "Add a typed relationship from SOURCE to TARGET",
	Long:  "Add an edge without changing task status. Use `pasture task dep add SOURCE --blocked-by TARGET` or `pasture task dep add SOURCE --target TARGET --kind KIND`. Only blocked_by edges affect readiness.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("target")
		kindText, _ := cmd.Flags().GetString("kind")
		if cmd.Flags().Changed("blocked-by") {
			if cmd.Flags().Changed("target") || cmd.Flags().Changed("kind") {
				printError(fmt.Errorf("task dep add cannot mix --blocked-by with --target or --kind; no edge was added: use `pasture task dep add SOURCE --blocked-by TARGET` or `pasture task dep add SOURCE --target TARGET --kind KIND`"))
				exitWithCode(1)
				return nil
			}
			target, _ = cmd.Flags().GetString("blocked-by")
			kindText = "blocked_by"
		}
		if target == "" {
			err := fmt.Errorf("task dep add has no target for %q; no edge was added: use `pasture task dep add SOURCE --blocked-by TARGET` or `pasture task dep add SOURCE --target TARGET --kind KIND`", args[0])
			printError(err)
			exitWithCode(1)
		}
		if kindText == "" {
			err := fmt.Errorf("task dep add requires --kind with --target; no edge was added: use blocked_by, derived_from, supersedes, or discovered_from, or use `pasture task dep add SOURCE --blocked-by TARGET` alone")
			printError(err)
			exitWithCode(1)
		}
		var kind provenance.EdgeKind
		if err := kind.UnmarshalText([]byte(kindText)); err != nil {
			printError(fmt.Errorf("task dep add rejected --kind %q: use blocked_by, derived_from, supersedes, or discovered_from: %w", kindText, err))
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
	taskDepAddCmd.Flags().String("blocked-by", "", "Blocking task; do not combine with --target or --kind")
	taskDepAddCmd.Flags().String("target", "", "Target task or provenance identity")
	taskDepAddCmd.Flags().String("kind", "", "Relationship kind: blocked_by, derived_from, supersedes, or discovered_from")
	taskDepCmd.AddCommand(taskDepAddCmd)
	taskCmd.AddCommand(taskDepCmd)
	taskDepCmd.AddCommand(&cobra.Command{
		Use:   "tree ID",
		Short: "Show outgoing blocked_by dependencies only",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := handlers.TaskDepTree(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
			return finishTaskCommand(code, err)
		},
	})

	for _, name := range []string{"ready", "blocked"} {
		c := &cobra.Command{
			Use:   name,
			Short: "List " + name + " non-closed tasks",
			Args:  cobra.NoArgs,
		}
		c.Flags().String("label", "", "Filter candidates by one exact label after readiness")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			label, _ := cmd.Flags().GetString("label")
			var code int
			var err error
			if cmd.Name() == "ready" {
				code, err = handlers.TaskReady(cmd.OutOrStdout(), flagDBPath, resolveFormat(), label)
			} else {
				code, err = handlers.TaskBlocked(cmd.OutOrStdout(), flagDBPath, resolveFormat(), label)
			}
			return finishTaskCommand(code, err)
		}
		taskCmd.AddCommand(c)
	}
}

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// taskReadyCmd implements `pasture task ready`.
var taskReadyCmd = &cobra.Command{
	Use:   "ready",
	Short: "List tasks that are open and have no open blockers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskReady(cmd.OutOrStdout(), flagDBPath, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskBlockedCmd implements `pasture task blocked`.
var taskBlockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "List tasks that are open but waiting on at least one open blocker",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskBlocked(cmd.OutOrStdout(), flagDBPath, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskDepCmd is the parent for dependency edge management subcommands.
var taskDepCmd = &cobra.Command{
	Use:   "dep",
	Short: "Manage typed dependency edges between tasks",
}

// taskDepAddCmd implements `pasture task dep add SOURCE --blocked-by TARGET`.
//
// Convention: SOURCE is the task that's blocked, TARGET is the blocker. This
// matches the bd convention: "A is blocked by B" -> A cannot proceed until
// B closes.
var taskDepAddCmd = &cobra.Command{
	Use:   "add SOURCE",
	Short: "Add a typed edge between two tasks",
	Args:  cobra.ExactArgs(1),
	Long: `Add a typed dependency edge originating at SOURCE.

The default edge kind is "blocked_by", set via --blocked-by TARGET. Use
--target and --kind explicitly for other edge kinds (derived_from,
supersedes, discovered_from).

Convention: "A is blocked by B" means SOURCE=A, TARGET=B — A cannot
proceed until B closes. Cycles in the blocked-by graph are rejected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		blockedBy, _ := cmd.Flags().GetString("blocked-by")
		target, _ := cmd.Flags().GetString("target")
		kindStr, _ := cmd.Flags().GetString("kind")

		// --blocked-by is the convenience form; --target + --kind is the explicit form.
		// Exactly one must be present.
		var (
			finalTarget string
			finalKind   provenance.EdgeKind
		)
		switch {
		case blockedBy != "" && target != "":
			err := fmt.Errorf("validation error: pass either --blocked-by or --target, not both — use --target with --kind for non-blocked-by edges")
			printError(err)
			exitWithCode(1)
		case blockedBy != "":
			finalTarget = blockedBy
			finalKind = provenance.EdgeBlockedBy
		case target != "":
			if kindStr == "" {
				err := fmt.Errorf("validation error: --kind is required when using --target — valid kinds: blocked_by, derived_from, supersedes, discovered_from")
				printError(err)
				exitWithCode(1)
			}
			var k provenance.EdgeKind
			if uerr := k.UnmarshalText([]byte(kindStr)); uerr != nil {
				err := fmt.Errorf("validation error: invalid --kind %q: %w — valid kinds: blocked_by, derived_from, supersedes, discovered_from", kindStr, uerr)
				printError(err)
				exitWithCode(1)
			}
			finalTarget = target
			finalKind = k
		default:
			err := fmt.Errorf("validation error: missing target — pass --blocked-by ID, or --target ID --kind KIND")
			printError(err)
			exitWithCode(1)
		}

		code, err := handlers.TaskDepAdd(cmd.OutOrStdout(), flagDBPath, args[0], finalTarget, finalKind, resolveFormat())
		if err != nil {
			printError(err)
		}
		if code != 0 {
			exitWithCode(code)
		}
		return nil
	},
}

// taskDepTreeCmd implements `pasture task dep tree ID`.
var taskDepTreeCmd = &cobra.Command{
	Use:   "tree ID",
	Short: "Print the blocked-by tree reachable from a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := handlers.TaskDepTree(cmd.OutOrStdout(), flagDBPath, args[0], resolveFormat())
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
	taskDepAddCmd.Flags().String("blocked-by", "", "Convenience flag: target task that this one is blocked by")
	taskDepAddCmd.Flags().String("target", "", "Explicit target ID (Task / Agent / Activity, depending on --kind)")
	taskDepAddCmd.Flags().String("kind", "", "Edge kind (used with --target): blocked_by, derived_from, supersedes, discovered_from")

	taskDepCmd.AddCommand(taskDepAddCmd)
	taskDepCmd.AddCommand(taskDepTreeCmd)

	taskCmd.AddCommand(taskReadyCmd)
	taskCmd.AddCommand(taskBlockedCmd)
	taskCmd.AddCommand(taskDepCmd)
}

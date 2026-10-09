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

// taskDepTreeCmd implements `pasture task dep tree ID [--kind KIND]`.
var taskDepTreeCmd = &cobra.Command{
	Use:   "tree ID",
	Short: "Show outgoing typed relation trees for a task",
	Long: "Follow outgoing task-to-task relations from ID and print the tree.\n\n" +
		"--kind selects which relation kind to traverse: blocked_by (the default),\n" +
		"derived_from, supersedes, discovered_from, or all. Only blocked_by affects\n" +
		"readiness (`pasture task ready` / `pasture task blocked`). Traversal is\n" +
		"outgoing only; a node that repeats prints once and is never expanded again.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kindText, _ := cmd.Flags().GetString("kind")
		kinds, err := parseTreeKinds(kindText)
		if err != nil {
			// Validate before any DB open so a bad selection creates no file.
			printError(err)
			exitWithCode(1)
			return nil
		}
		code, err := handlers.TaskDepTree(cmd.OutOrStdout(), flagDBPath, args[0], kinds, resolveFormat())
		return finishTaskCommand(code, err)
	},
}

func init() {
	taskDepAddCmd.Flags().String("blocked-by", "", "Blocking task; do not combine with --target or --kind")
	taskDepAddCmd.Flags().String("target", "", "Target task or provenance identity")
	taskDepAddCmd.Flags().String("kind", "", "Relationship kind: blocked_by, derived_from, supersedes, or discovered_from")
	taskDepCmd.AddCommand(taskDepAddCmd)

	taskDepTreeCmd.Flags().String("kind", "blocked_by",
		"Relation kind to traverse: blocked_by, derived_from, supersedes, discovered_from, or all")
	taskDepCmd.AddCommand(taskDepTreeCmd)

	taskCmd.AddCommand(taskDepCmd)

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

// parseTreeKinds maps the `--kind` selection to the task-to-task kinds a tree
// may traverse. The allowlist is exactly the four relation kinds plus "all";
// the error names those values so the caller knows the truthful set. The
// pre-existing `dep add` surface accepts six spellings via EdgeKind.UnmarshalText,
// but a tree must only follow task-to-task relations.
func parseTreeKinds(text string) ([]provenance.EdgeKind, error) {
	switch text {
	case "blocked_by":
		return []provenance.EdgeKind{provenance.EdgeBlockedBy}, nil
	case "derived_from":
		return []provenance.EdgeKind{provenance.EdgeDerivedFrom}, nil
	case "supersedes":
		return []provenance.EdgeKind{provenance.EdgeSupersedes}, nil
	case "discovered_from":
		return []provenance.EdgeKind{provenance.EdgeDiscoveredFrom}, nil
	case "all":
		return []provenance.EdgeKind{
			provenance.EdgeBlockedBy,
			provenance.EdgeDerivedFrom,
			provenance.EdgeSupersedes,
			provenance.EdgeDiscoveredFrom,
		}, nil
	default:
		return nil, fmt.Errorf("task dep tree rejected --kind %q: no tree was rendered — valid values are blocked_by, derived_from, supersedes, discovered_from, or all; use --kind all to traverse every typed relation kind", text)
	}
}

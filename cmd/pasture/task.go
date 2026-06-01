package main

import "github.com/spf13/cobra"

// taskCmd is the parent for all task-management subcommands. Each leaf
// subcommand is registered in its own file (task_create.go, task_show.go, …)
// to keep this skeleton focused on shared wiring.
var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage Provenance-backed tasks",
	Long: `Manage tasks in the local Provenance task tracker.

Subcommands cover the full task lifecycle: creation, retrieval, updates,
closure, listing, readiness queries, dependency edges, labels, and comments.

All subcommands accept the global flags --db, --format, and --namespace.`,
}

func init() {
	rootCmd.AddCommand(taskCmd)
}

package main

import "github.com/spf13/cobra"

// taskCmd is the parent for all task-management subcommands. Each leaf
// subcommand is registered in its own file (task_create.go, task_show.go, …)
// to keep this skeleton focused on shared wiring.
var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage Provenance-backed tasks",
	Long: `Manage tasks and their generic Provenance-backed relationships.

Subcommands cover task creation, retrieval, updates, closure, relationships,
comments, and timelines. Epoch lifecycle operations are available only below
"pasture epoch".

All subcommands accept the global flags --db, --format, and --namespace.`,
}

func init() {
	rootCmd.AddCommand(taskCmd)
}

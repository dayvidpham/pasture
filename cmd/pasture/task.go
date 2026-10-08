package main

import "github.com/spf13/cobra"

func finishTaskCommand(code int, err error) error {
	if err != nil {
		printError(err)
	}
	if code != 0 {
		exitWithCode(code)
	}
	return nil
}

// taskCmd is the parent for all task-management subcommands. Each leaf
// subcommand is registered in its own file (task_create.go, task_show.go, …)
// to keep this skeleton focused on shared wiring.
var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage Provenance-backed tasks",
	Long: `Manage tasks and their generic Provenance-backed relationships.

Subcommands cover task creation, retrieval, updates, closure, readiness, labels,
dependencies, registered agents, comments, and timelines. Epoch lifecycle operations are available only below
"pasture epoch".

All subcommands accept the global flags --db, --format, and --namespace.`,
}

func init() {
	rootCmd.AddCommand(taskCmd)
}

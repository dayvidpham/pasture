// Command pasture is the local task management and epoch control CLI for the
// Pasture toolkit.
//
// It manages tasks, dependencies, labels, comments, and the audit-event record
// backed by the unified Pasture SQLite database
// (~/.local/share/pasture/pasture.db). It also starts, signals, and queries
// durable epoch workflows without requiring a separately running daemon.
//
// Task commands route through the unified protocol.TaskTracker constructor
// (tasks.OpenTaskTracker) so the auto-on-open audit migrator runs against
// legacy databases on first use.
//
// Exit codes (from internal/errors):
//
//	0  success
//	1  validation error (bad flags, missing arguments)
//	2  connection error (cannot open the database file)
//	3  task or workflow error (task not found, cycle detected; epoch start rejected, signal undeliverable)
//	4  config error
//	5  storage error (migration / schema failure)
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// exitWithCode terminates the process with the given exit code.
// Called by RunE handlers after printing a human-readable error to stderr.
func exitWithCode(code int) {
	os.Exit(code)
}

// Command pasture is the local task management CLI for the Pasture toolkit.
//
// It manages tasks, dependencies, labels, comments, and the audit-event
// record backed by the unified Pasture SQLite database
// (~/.local/share/pasture/pasture.db, per PROPOSAL-2 §7.1). Unlike
// pasture-msg (which sends Temporal signals to the pastured daemon),
// pasture operates entirely on the local task + audit tracker — no daemon
// required.
//
// Routes through the unified `protocol.TaskTracker` constructor
// (`tasks.OpenTaskTracker`) so the auto-on-open audit migrator runs against
// legacy databases on first use; pre-PROPOSAL-2 the CLI used the
// Provenance-only `tasks.OpenTracker` and would not have triggered the
// migration. See PROPOSAL-2 §7.4 + §7.10 for the unification rationale.
//
// Exit codes (from internal/errors):
//
//	0  success
//	1  validation error (bad flags, missing arguments)
//	2  connection error (cannot open the database file)
//	3  task error (not found, cycle detected, already closed, etc.)
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

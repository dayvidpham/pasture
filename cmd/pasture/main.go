// Command pasture is the local task management CLI for the Pasture toolkit.
//
// It manages tasks, dependencies, labels, and comments backed by a local
// Provenance (PROV-O) SQLite database. Unlike pasture-msg (which sends
// Temporal signals to the pastured daemon), pasture operates entirely on
// the local task tracker — no daemon required.
//
// Exit codes:
//
//	0  success
//	1  validation error (bad flags, missing arguments)
//	2  storage error (cannot open or read tracker database)
//	3  task error (not found, cycle detected, already closed, etc.)
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

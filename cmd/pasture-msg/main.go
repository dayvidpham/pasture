// Command pasture-msg is the Pasture CLI — sends control messages to the
// pastured daemon via Temporal signals and queries.
//
// Exit codes:
//
//	0  success
//	1  validation or configuration error
//	2  connection error (Temporal server unreachable)
//	3  workflow error (workflow not found, signal or query failed)
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

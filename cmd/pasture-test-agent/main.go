// Package main implements pasture-test-agent — a fake ACP-compatible agent
// binary used only in tests.
//
// It reads a JSON fixture file path from argv[1], emits the file contents
// verbatim to stdout (expected to be newline-delimited JSON-RPC), then exits 0.
//
// Usage:
//
//	pasture-test-agent <fixture-file.json>
//
// The fixture file must contain one or more JSON-RPC session/update lines, each
// terminated by a newline. Tests build this binary via "go build" and pass a
// temp fixture file as the first argument.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pasture-test-agent <fixture-file.json>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading fixture: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(data))
}

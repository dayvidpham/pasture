//go:build ignore

// Command gen writes proofs_claude.gen.go, proofs_codex.gen.go and
// proofs_opencode.gen.go from the proof declaration tables in the three
// harness target files of this package. It is invoked by
// `go generate ./internal/lifecycle/activation/...` (wired into
// `make generate`). The output is deterministic, so a second run is zero-diff.
package main

import (
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation/internal/proofgen"
)

func main() {
	if err := proofgen.Write("."); err != nil {
		fmt.Fprintf(os.Stderr, "activation generate: %v\n", err)
		os.Exit(1)
	}
}

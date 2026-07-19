// Command codegen is the generation entry point for the Pasture protocol.
// The canonical `make generate` command invokes the go:generate directive in
// internal/codegen/codegen.go, which selects both registered harness targets:
//
//	go run ../../tools/codegen --targets claude-code,opencode
//
// The tool writes schema.xml once, then emits each selected harness's skills,
// agents, verbatim skill copies, and manifest. Direct invocations may use
// --targets for focused generator development, but repository validation uses
// the canonical all-target command.
//
// Exits non-zero if any generator returns an error.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen"
)

// moduleRoot walks upward from the current working directory until it finds go.mod,
// then returns that directory as the module (repo) root.
//
// When go:generate runs from internal/codegen/, the cwd is that package directory.
// Walking upward finds the repo root regardless of the exact invocation method.
func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("codegen: os.Getwd failed: %w", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"codegen: could not find go.mod walking up from %q — "+
					"ensure the tool is run from inside the pasture module",
				wd,
			)
		}
		dir = parent
	}
}

func main() {
	outputRoot := flag.String("output", "", "output root directory (default: module root, found by walking up from cwd to go.mod)")
	targetFlag := flag.String("targets", string(codegen.HarnessClaudeCode), "comma-separated generation targets (registered: claude-code, opencode, codex)")
	flag.Parse()

	var root string
	if *outputRoot != "" {
		root = *outputRoot
	} else {
		var err error
		root, err = moduleRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
	}

	opts := codegen.DefaultOptions // Diff: true, Write: true

	targets, err := codegen.ResolveHarness(strings.Split(*targetFlag, ","))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	// codegen.Generate runs the strict source-migration gate first (so an
	// unclassified harness-syntax candidate aborts generation with no partial
	// output), then emits schema.xml, every requested harness target, and the
	// global-ID uniqueness check. Steps after the gate accumulate their errors
	// so one run reports every problem at once.
	result, errors := codegen.Generate(root, targets, opts)

	if result.SchemaPath != "" {
		fmt.Printf("Generated %s\n", result.SchemaPath)
	}
	for _, file := range result.Files {
		fmt.Printf("Generated %s\n", file.Path)
	}

	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d error(s) encountered:\n", len(errors))
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", e)
		}
		os.Exit(1)
	}
}

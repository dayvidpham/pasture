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
	targetFlag := flag.String("targets", string(codegen.HarnessClaudeCode), "comma-separated generation targets (registered: claude-code, opencode)")
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

	var errors []error
	targets, err := codegen.ResolveHarness(strings.Split(*targetFlag, ","))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	// ── 1. Generate schema.xml ────────────────────────────────────────────────
	schemaPath := filepath.Join(root, "schema.xml")
	if _, err := codegen.GenerateSchemaToFile(schemaPath, opts); err != nil {
		errors = append(errors, fmt.Errorf("schema: %w", err))
	} else {
		fmt.Printf("Generated %s\n", schemaPath)
	}

	// ── 2. Generate harness-specific skills and agents ────────────────────────
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	for _, target := range targets {
		files, err := codegen.EmitHarness(root, target, figuresDir, opts)
		if err != nil {
			errors = append(errors, fmt.Errorf("target %s: %w", target.Name, err))
		} else {
			for _, file := range files {
				fmt.Printf("Generated %s\n", file.Path)
			}
		}
	}

	// ── 3. Global-ID uniqueness enforcement (SLICE-2, URD R5+R7) ─────────────
	// Must run AFTER all generators so the full registry is assembled, and
	// BEFORE exit so a violation causes go generate to fail immediately.
	if err := codegen.ValidateGlobalIds(); err != nil {
		errors = append(errors, fmt.Errorf("global-id validation: %w", err))
	}

	// ── Report errors and exit ────────────────────────────────────────────────
	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d error(s) encountered:\n", len(errors))
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", e)
		}
		os.Exit(1)
	}
}

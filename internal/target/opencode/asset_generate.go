//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	root, err := moduleRoot(wd)
	if err != nil {
		panic(err)
	}
	files, err := codegen.EmitHarness(codegen.RepoRoots(root), codegen.OpenCodeTarget,
		filepath.Join(root, "skills", "protocol", "figures"),
		codegen.GenerateOptions{Diff: false, Write: false})
	if err != nil {
		panic(fmt.Errorf("render OpenCode harness assets: %w", err))
	}
	assets := filepath.Join(wd, "assets")
	if err := os.RemoveAll(assets); err != nil {
		panic(fmt.Errorf("remove stale OpenCode assets %q: %w", assets, err))
	}
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		if err != nil {
			panic(err)
		}
		rel = filepath.ToSlash(rel)
		var destination string
		switch {
		case strings.HasPrefix(rel, ".opencode/skill/"):
			destination = filepath.Join(assets, "skills", filepath.FromSlash(strings.TrimPrefix(rel, ".opencode/skill/")))
		case strings.HasPrefix(rel, ".opencode/agent/"):
			destination = filepath.Join(assets, "agents", filepath.FromSlash(strings.TrimPrefix(rel, ".opencode/agent/")))
		case rel == codegen.OpenCodeHooksModulePath:
			destination = filepath.Join(assets, "hooks", "pasture-hooks.ts")
		default:
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			panic(fmt.Errorf("create generated asset parent for %q: %w", destination, err))
		}
		if err := os.WriteFile(destination, []byte(file.Content), 0o644); err != nil {
			panic(fmt.Errorf("write generated asset %q: %w", destination, err))
		}
	}
}

func moduleRoot(start string) (string, error) {
	for current := start; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no go.mod found above %q; run generation from the Pasture module", start)
		}
	}
}

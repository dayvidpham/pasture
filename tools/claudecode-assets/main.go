// Command claudecode-assets regenerates the embedded Claude Code plugin assets
// (internal/target/claudecode/assets/pasture-skills and
// .../assets/pasture-agents) from the live codegen pipeline.
//
// It is the write half of the anti-drift mechanism whose read half is
// TestEmbeddedAssetsMatchLivePipeline: both call claudecode.RenderGeneratedAssets
// so they can never disagree about what "correct" means. The go:generate
// directive in internal/target/claudecode/generate.go invokes this command, and
// `make generate` runs it after the canonical skills/ and agents/ trees are
// regenerated.
//
// It manages only the codegen-driven files. The plugin scaffolding
// (.claude-plugin/plugin.json), the pasture-hooks package, and the hand-authored
// verbatim skill trees (skills/protocol, skills/install-cli) are never touched.
//
// Exits non-zero if rendering or any filesystem write fails.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// go:generate runs with the working directory set to the package that holds
	// the directive (internal/target/claudecode), so the embedded assets tree is
	// ./assets relative to the cwd, and the module root is found by walking up.
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("claudecode-assets: os.Getwd failed: %w", err)
	}
	assetsDir := filepath.Join(wd, "assets")
	if info, statErr := os.Stat(assetsDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf(
			"claudecode-assets: embedded assets directory %q not found — "+
				"why: this generator must run from the internal/target/claudecode package directory "+
				"(the go:generate directive sets that working directory) — "+
				"fix: run `make generate` or `go generate ./internal/target/claudecode/...`",
			assetsDir,
		)
	}

	repoRoot, err := claudecode.FindModuleRootFrom(wd)
	if err != nil {
		return fmt.Errorf("claudecode-assets: %w", err)
	}

	generated, err := claudecode.RenderGeneratedAssets(repoRoot)
	if err != nil {
		return fmt.Errorf("claudecode-assets: %w", err)
	}

	written := 0
	for _, assetsRel := range claudecode.SortedAssetKeys(generated) {
		dst := filepath.Join(assetsDir, filepath.FromSlash(assetsRel))
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return fmt.Errorf("claudecode-assets: create parent directory for %q: %w", dst, mkErr)
		}
		if wErr := os.WriteFile(dst, generated[assetsRel], 0o644); wErr != nil {
			return fmt.Errorf("claudecode-assets: write generated asset %q: %w", dst, wErr)
		}
		written++
	}

	// Remove any codegen-managed asset the live pipeline no longer emits, so a
	// removed skill or renamed agent cannot linger in the embed. Verbatim skill
	// trees and plugin scaffolding are never enumerated here.
	stale, err := claudecode.StaleGeneratedAssets(assetsDir, generated)
	if err != nil {
		return fmt.Errorf("claudecode-assets: %w", err)
	}
	for _, dst := range stale {
		if rmErr := os.Remove(dst); rmErr != nil {
			return fmt.Errorf("claudecode-assets: remove stale generated asset %q: %w", dst, rmErr)
		}
	}

	fmt.Printf("claudecode-assets: wrote %d generated asset(s), removed %d stale\n", written, len(stale))
	return nil
}

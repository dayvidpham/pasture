package claudecode

// The embedded Claude Code plugin assets under assets/pasture-skills and
// assets/pasture-agents are codegen output, not hand-maintained files. This
// directive rewrites them from the live pipeline (codegen.EmitHarness for the
// claude-code target, via RenderGeneratedAssets) so the packaged CLI's embedded
// snapshot always tracks the canonical skills/ and agents/ source of truth.
//
// It must run after the root skills/ and agents/ trees are regenerated, so the
// Makefile invokes `go generate ./internal/codegen/...` before
// `go generate ./internal/target/claudecode/...`. TestEmbeddedAssetsMatchLivePipeline
// fails CI if this snapshot is ever left stale.
//
//go:generate go run ../../../tools/claudecode-assets

package claudecode

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedVerbatimAssetsMatchCanonicalSources(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err := FindModuleRootFrom(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"protocol", "install-cli"} {
		source := filepath.Join(repoRoot, "skills", dir)
		expected := map[string]bool{}
		err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, name)
			if err != nil {
				return err
			}
			want, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			embedded := path.Join(assetsDirName, skillsPackageDir, filepath.ToSlash(rel))
			expected[embedded] = true
			got, err := assetsFS.ReadFile(embedded)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("verbatim asset %s differs from canonical source: %v; %s", rel, err, regenerateHint)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		err = fs.WalkDir(assetsFS, path.Join(assetsDirName, skillsPackageDir, "skills", dir), func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && !expected[name] {
				t.Errorf("verbatim asset %s has no canonical source; %s", name, regenerateHint)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Join(repoRoot, "hooks/scripts/git-discipline.sh"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := assetsFS.ReadFile("assets/pasture-hooks/hooks/scripts/git-discipline.sh")
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("git discipline payload differs from canonical source: %v; %s", err, regenerateHint)
	}
	sourceInfo, err := os.Stat(filepath.Join(repoRoot, "hooks/scripts/git-discipline.sh"))
	if err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(filepath.Join(repoRoot, "internal/target/claudecode/assets/pasture-hooks/hooks/scripts/git-discipline.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if sourceInfo.Mode().Perm() != copyInfo.Mode().Perm() {
		t.Errorf("git discipline payload mode differs from canonical source; %s", regenerateHint)
	}
}

// regenerateHint is the actionable remediation printed on any drift failure. It
// names the exact command that rewrites the embedded assets from the live
// pipeline so the fix is unambiguous.
const regenerateHint = "run `make generate` (or `go generate ./internal/target/claudecode/...`) to rewrite internal/target/claudecode/assets from the live codegen pipeline, then commit the result"

// TestEmbeddedAssetsMatchLivePipeline is the anti-drift guard for the embedded
// Claude Code plugin assets. The installed CLI materializes its skills and
// agents from the //go:embed assets tree, never from the source checkout, so
// nothing at runtime would notice if that snapshot fell behind the canonical
// codegen source. This test renders the live claude-code pipeline
// (codegen.EmitHarness via RenderGeneratedAssets) in memory and compares it
// byte-for-byte against the embedded bytes, so CI reddens the moment a specs or
// template change updates the canonical skills/agents but leaves the embed
// stale.
//
// It checks emitted files plus canonical verbatim skills and Git discipline.
// Independently authored plugin scaffolding remains out of scope.
func TestEmbeddedAssetsMatchLivePipeline(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	repoRoot, err := FindModuleRootFrom(wd)
	if err != nil {
		t.Fatalf("locate module root: %v", err)
	}

	expected, err := RenderGeneratedAssets(repoRoot)
	if err != nil {
		t.Fatalf("render live claude-code pipeline: %v", err)
	}
	if len(expected) == 0 {
		t.Fatalf("live pipeline produced no generated assets — the codegen source is empty or broken; %s", regenerateHint)
	}

	// 1. Every live-pipeline file must be embedded with byte-identical content.
	for _, assetsRel := range SortedAssetKeys(expected) {
		embedPath := path.Join(assetsDirName, assetsRel)
		got, readErr := assetsFS.ReadFile(embedPath)
		if readErr != nil {
			t.Errorf("embedded asset %q is missing but the live claude-code pipeline emits it — %s", assetsRel, regenerateHint)
			continue
		}
		if !bytes.Equal(got, expected[assetsRel]) {
			t.Errorf(
				"embedded asset %q drifted from the live claude-code pipeline (embedded %d bytes, pipeline %d bytes) — %s",
				assetsRel, len(got), len(expected[assetsRel]), regenerateHint,
			)
		}
	}

	// 2. Deletion guard: no embedded codegen-driven file may exist that the live
	//    pipeline no longer emits (a skill removed from specs, or a renamed
	//    agent, would otherwise linger in the embed forever). Canonical verbatim
	//    trees are included; independently authored scaffolding is excluded.
	for _, packageDir := range []string{skillsPackageDir, agentsPackageDir, hooksPackageDir} {
		root := path.Join(assetsDirName, packageDir)
		walkErr := fs.WalkDir(assetsFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			assetsRel := strings.TrimPrefix(p, assetsDirName+"/")
			if !isManagedGeneratedPath(packageDir, assetsRel) {
				return nil
			}
			if _, ok := expected[assetsRel]; !ok {
				t.Errorf("embedded asset %q is codegen-managed but the live claude-code pipeline no longer emits it — %s", assetsRel, regenerateHint)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk embedded package %q: %v", packageDir, walkErr)
		}
	}
}

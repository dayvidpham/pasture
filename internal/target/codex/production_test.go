package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

func TestProductionDescriptorIsEmbeddedAndSiblingFree(t *testing.T) {
	t.Parallel()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatalf("Descriptor() failed: %v", err)
	}
	if !descriptor.IsValid() || descriptor.RuntimeContractID() != codegen.CodexRuntimeContractID() {
		t.Fatalf("Descriptor() returned invalid or drifted contract %q", descriptor.RuntimeContractID())
	}
	wantPrefixes := map[artifact.Extension]string{
		artifact.ExtensionSkills: ".agents/skills/",
		artifact.ExtensionAgents: ".codex/agents/",
		artifact.ExtensionHooks:  ".codex/hooks/",
	}
	hookPublicFile := map[string]bool{
		".codex/hooks.json":                    true,
		".codex/pasture-codex-activation.json": true,
	}
	for _, component := range descriptor.Components() {
		if component.Extension() == artifact.ExtensionHooks && component.DefaultEnabled() {
			t.Fatal("production hooks component is default-enabled")
		}
		regular := 0
		for _, entry := range component.Bundle().Manifest().Entries() {
			if !entry.IsRegular() {
				continue
			}
			regular++
			if !strings.HasPrefix(entry.Path().String(), wantPrefixes[component.Extension()]) && !(component.Extension() == artifact.ExtensionHooks && hookPublicFile[entry.Path().String()]) {
				t.Fatalf("%s bundle contains sibling/out-of-layout leaf %q", component.Extension(), entry.Path())
			}
		}
		if regular == 0 {
			t.Fatalf("%s bundle has no regular files", component.Extension())
		}
	}
}

func TestEmbeddedSnapshotMatchesCanonicalEmitter(t *testing.T) {
	t.Parallel()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(working, "..", "..", ".."))
	files, err := codegen.EmitHarness(root, codegen.CodexTarget, filepath.Join(root, "skills", "protocol", "figures"), codegen.GenerateOptions{Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(codex) failed: %v", err)
	}
	live, err := codegen.NewCodexTargetDescriptor(root, files)
	if err != nil {
		t.Fatalf("NewCodexTargetDescriptor(live) failed: %v", err)
	}
	embedded, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatalf("Descriptor() failed: %v", err)
	}
	for index, component := range embedded.Components() {
		if component.Extension() == artifact.ExtensionHooks {
			// The activation target adds the two public target-level hook files to
			// the upstream hooks package, so its bundle identity intentionally
			// differs while every source byte remains drift-checked above.
			continue
		}
		if got, want := component.Bundle().ID(), live.Packages()[index].Bundle().ID(); got != want {
			t.Fatalf("embedded %s bundle drifted: got %s want %s; run go generate ./internal/target/codex/...", component.Extension(), got, want)
		}
	}
}

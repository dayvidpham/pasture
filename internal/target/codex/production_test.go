package codex_test

import (
	"bytes"
	"io"
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
			handle, err := component.Bundle().Open(entry.Path().String())
			if err != nil {
				t.Fatalf("open %s artifact %q: %v", component.Extension(), entry.Path(), err)
			}
			content, err := io.ReadAll(handle)
			handle.Close()
			if err != nil || artifact.DigestBytes(content) != entry.Digest() {
				t.Fatalf("%s artifact %q bytes do not match immutable digest", component.Extension(), entry.Path())
			}
			if !strings.HasPrefix(entry.Path().String(), wantPrefixes[component.Extension()]) && !(component.Extension() == artifact.ExtensionHooks && hookPublicFile[entry.Path().String()]) {
				t.Fatalf("%s bundle contains sibling/out-of-layout leaf %q", component.Extension(), entry.Path())
			}
		}
		if regular == 0 {
			t.Fatalf("%s bundle has no regular files", component.Extension())
		}
	}
}

func TestGlobalHookConfigurationUsesHomeRelativeRunners(t *testing.T) {
	t.Parallel()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := descriptor.Hooks().Bundle().Open(".codex/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(handle)
	handle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("sh .codex/hooks/events/")) || !bytes.Contains(content, []byte("sh ~/.codex/hooks/events/")) {
		t.Fatalf("global hooks configuration does not exclusively use home-relative runners: %s", content)
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

	t.Run("independent global hook merge", func(t *testing.T) {
		type expectedFile struct {
			content []byte
			mode    uint32
		}
		expected := map[string]expectedFile{}
		hooksPackage := live.Packages()[2].Bundle()
		for _, entry := range hooksPackage.Manifest().Entries() {
			if !entry.IsRegular() {
				continue
			}
			handle, openErr := hooksPackage.Open(entry.Path().String())
			if openErr != nil {
				t.Fatal(openErr)
			}
			content, readErr := io.ReadAll(handle)
			handle.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			expected[entry.Path().String()] = expectedFile{content: content, mode: entry.Mode().Bits()}
		}
		manifest := live.ManifestBundle()
		for _, path := range []string{".codex/hooks.json", ".codex/pasture-codex-activation.json"} {
			var entry artifact.Entry
			present := false
			for _, candidate := range manifest.Manifest().Entries() {
				if candidate.Path().String() == path {
					entry, present = candidate, true
					break
				}
			}
			if !present || !entry.IsRegular() {
				t.Fatalf("canonical target manifest lacks regular %q", path)
			}
			handle, openErr := manifest.Open(path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			content, readErr := io.ReadAll(handle)
			handle.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if path == ".codex/hooks.json" {
				content = bytes.ReplaceAll(content, []byte("sh .codex/hooks/events/"), []byte("sh ~/.codex/hooks/events/"))
			}
			expected[path] = expectedFile{content: content, mode: entry.Mode().Bits()}
		}

		actual := embedded.Hooks().Bundle()
		seen := 0
		for _, entry := range actual.Manifest().Entries() {
			if !entry.IsRegular() {
				continue
			}
			want, present := expected[entry.Path().String()]
			if !present {
				t.Fatalf("merged global hook bundle has unexpected regular path %q", entry.Path())
			}
			handle, openErr := actual.Open(entry.Path().String())
			if openErr != nil {
				t.Fatal(openErr)
			}
			content, readErr := io.ReadAll(handle)
			handle.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(content, want.content) || entry.Mode().Bits() != want.mode || entry.Digest() != artifact.DigestBytes(want.content) {
				t.Fatalf("merged global hook path %q differs in bytes, mode, or digest", entry.Path())
			}
			seen++
		}
		if seen != len(expected) {
			t.Fatalf("merged global hook regular count = %d, independently expected %d", seen, len(expected))
		}
	})
}

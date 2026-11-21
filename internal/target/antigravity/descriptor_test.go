package antigravity_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/target/antigravity"
)

func TestEmbeddedDescriptorCoversCanonicalMatrixAndLayout(t *testing.T) {
	t.Parallel()

	desc, err := antigravity.Descriptor()
	if err != nil {
		t.Fatalf("Descriptor(): %v", err)
	}
	if !desc.IsValid() {
		t.Fatal("Descriptor() returned invalid descriptor")
	}
	if desc.Harness() != ir.HarnessAntigravityCLI {
		t.Fatalf("Harness() = %q, want %q", desc.Harness(), ir.HarnessAntigravityCLI)
	}
	if desc.RuntimeContractID() != codegen.AntigravityCLIRuntimeContractID() {
		t.Fatalf("RuntimeContractID() = %q, want %q", desc.RuntimeContractID(), codegen.AntigravityCLIRuntimeContractID())
	}

	skills := desc.Skills()
	if !skills.DefaultEnabled() {
		t.Fatal("skills should be default-enabled")
	}
	regular := 0
	foundWorker := false
	for _, entry := range skills.Bundle().Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		regular++
		p := entry.Path().String()
		if !strings.HasPrefix(p, ".gemini/config/skills/") {
			t.Fatalf("skills entry %q is outside .gemini/config/skills/", p)
		}
		if p == ".gemini/config/skills/worker/SKILL.md" {
			foundWorker = true
		}
	}
	if regular == 0 || !foundWorker {
		t.Fatalf("skills bundle missing expected regular files (regular=%d foundWorker=%v)", regular, foundWorker)
	}

	for _, ext := range []artifact.Extension{artifact.ExtensionAgents, artifact.ExtensionHooks} {
		comp, err := desc.Component(ext)
		if err != nil {
			t.Fatalf("Component(%s): %v", ext, err)
		}
		if comp.DefaultEnabled() {
			t.Fatalf("unsupported slot %s must not be default-enabled", ext)
		}
		if len(comp.Bundle().Manifest().Entries()) != 0 {
			t.Fatalf("unsupported slot %s must carry an empty bundle, got %d entries", ext, len(comp.Bundle().Manifest().Entries()))
		}
	}
}

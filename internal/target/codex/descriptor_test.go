package codex_test

import (
	"github.com/dayvidpham/pasture/artifact"
	codex "github.com/dayvidpham/pasture/internal/target/codex"
	"testing"
	"testing/fstest"
)

func testBundle(t *testing.T, name string) artifact.Bundle {
	p, _ := artifact.NewPath(name)
	m, _ := artifact.NewMode(0o644)
	data := []byte(name)
	e, _ := artifact.NewFileEntry(p, m, artifact.DigestBytes(data))
	manifest, _ := artifact.NewManifest(e)
	b, err := artifact.NewBundle(fstest.MapFS{name: &fstest.MapFile{Data: data, Mode: 0o644}}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDescriptorIsExhaustiveAndHooksDefaultOff(t *testing.T) {
	s, _ := codex.NewComponent(artifact.ExtensionSkills, testBundle(t, "SKILL.md"), true)
	a, _ := codex.NewComponent(artifact.ExtensionAgents, testBundle(t, "worker.toml"), true)
	h, _ := codex.NewComponent(artifact.ExtensionHooks, testBundle(t, "hooks.json"), false)
	d, err := codex.NewTargetDescriptor(s, a, h)
	if err != nil {
		t.Fatal(err)
	}
	if !d.IsValid() || len(d.Components()) != 3 || d.Hooks().DefaultEnabled() {
		t.Fatalf("invalid descriptor")
	}
	if _, err := codex.NewComponent(artifact.ExtensionHooks, testBundle(t, "bad.json"), true); err == nil {
		t.Fatal("enabled hooks accepted")
	}
}

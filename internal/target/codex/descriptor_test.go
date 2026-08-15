package codex_test

import (
	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen"
	codex "github.com/dayvidpham/pasture/internal/target/codex"
	"testing"
	"testing/fstest"
)

func testPackageID(t *testing.T, extension artifact.Extension) codex.PackageID {
	t.Helper()
	descriptor, err := codex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	component, err := descriptor.Component(extension)
	if err != nil {
		t.Fatal(err)
	}
	return component.PackageID()
}

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
	s, _ := codex.NewComponent(artifact.ExtensionSkills, testPackageID(t, artifact.ExtensionSkills), testBundle(t, ".agents/skills/example/SKILL.md"), true)
	a, _ := codex.NewComponent(artifact.ExtensionAgents, testPackageID(t, artifact.ExtensionAgents), testBundle(t, ".codex/agents/worker.toml"), true)
	h, _ := codex.NewComponent(artifact.ExtensionHooks, testPackageID(t, artifact.ExtensionHooks), testBundle(t, ".codex/hooks/events/SessionStart.sh"), false)
	d, err := codex.NewTargetDescriptor(s, a, h)
	if err != nil {
		t.Fatal(err)
	}
	if !d.IsValid() || len(d.Components()) != 3 || d.Hooks().DefaultEnabled() {
		t.Fatalf("invalid descriptor")
	}
	if _, err := codex.NewComponent(artifact.ExtensionHooks, testPackageID(t, artifact.ExtensionHooks), testBundle(t, ".codex/hooks/events/bad.sh"), true); err == nil {
		t.Fatal("enabled hooks accepted")
	}
}

func TestComponentConstructionRejectsInvalidLayouts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		extension artifact.Extension
		bundle    artifact.Bundle
	}{
		{name: "wrong skills prefix", extension: artifact.ExtensionSkills, bundle: testBundle(t, ".codex/skills/example/SKILL.md")},
		{name: "agent sibling prefix", extension: artifact.ExtensionAgents, bundle: testBundle(t, ".agents/skills/example/SKILL.md")},
		{name: "unapproved hook public file", extension: artifact.ExtensionHooks, bundle: testBundle(t, ".codex/private-trust.json")},
		{name: "empty hook bundle", extension: artifact.ExtensionHooks, bundle: artifact.Bundle{}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codex.NewComponent(test.extension, testPackageID(t, test.extension), test.bundle, false); err == nil {
				t.Fatalf("NewComponent(%s) accepted invalid immutable layout", test.extension)
			}
		})
	}
}

func TestComponentConstructionRequiresTypedMatchingPackageIdentity(t *testing.T) {
	t.Parallel()
	bundle := testBundle(t, ".agents/skills/example/SKILL.md")
	if _, err := codex.NewComponent(artifact.ExtensionSkills, codex.PackageID{}, bundle, true); err == nil {
		t.Fatal("NewComponent accepted a zero package identity")
	}
	if _, err := codex.NewComponent(artifact.ExtensionSkills, testPackageID(t, artifact.ExtensionAgents), bundle, true); err == nil {
		t.Fatal("NewComponent accepted an agents package identity for skills")
	}
	if _, err := codex.NewPackageID(artifact.ExtensionSkills, codegen.CodexComponentID{}); err == nil {
		t.Fatal("NewPackageID accepted the generated package ID zero value")
	}
}

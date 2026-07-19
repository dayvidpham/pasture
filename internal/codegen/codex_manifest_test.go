package codegen

import (
	"strings"
	"testing"
)

// TestCodexManifestDeclaresThreePackages proves the Codex plugin manifest
// declares the skills, agents, and hooks packages with their stable component
// identities and roots, marks hooks default-off, and stamps the pinned
// RuntimeContractID.
func TestCodexManifestDeclaresThreePackages(t *testing.T) {
	t.Parallel()

	got := renderCodexManifest()

	for _, want := range []string{
		`schema = "` + codexManifestSchema + `"`,
		`runtime_contract = "` + CodexRuntimeContractID().String() + `"`,
		"[packages.skills]",
		`id = "` + codexSkillsComponent.String() + `"`,
		`path = "` + codexSkillRoot + `"`,
		"[packages.agents]",
		`id = "` + codexAgentsComponent.String() + `"`,
		`path = "` + codexAgentsRoot + `"`,
		"[packages.hooks]",
		`id = "` + codexHooksComponent.String() + `"`,
		`path = "` + codexHooksRoot + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("manifest is missing %q\n--- manifest ---\n%s", want, got)
		}
	}

	// The hooks package must be default-off; skills and agents on.
	hooksBlock := got[strings.Index(got, "[packages.hooks]"):]
	if !strings.Contains(hooksBlock, "enabled = false") {
		t.Fatalf("hooks package is not default-off:\n%s", hooksBlock)
	}
	skillsBlock := got[strings.Index(got, "[packages.skills]"):strings.Index(got, "[packages.agents]")]
	if !strings.Contains(skillsBlock, "enabled = true") {
		t.Fatalf("skills package is not enabled:\n%s", skillsBlock)
	}
}

// TestCodexManifestIsDeterministic proves the manifest renderer is pure.
func TestCodexManifestIsDeterministic(t *testing.T) {
	t.Parallel()

	if renderCodexManifest() != renderCodexManifest() {
		t.Fatal("renderCodexManifest is not deterministic")
	}
	if renderCodexHooksMarker() != renderCodexHooksMarker() {
		t.Fatal("renderCodexHooksMarker is not deterministic")
	}
}

// TestCodexHooksMarkerIsDefaultOffAndHookSafe proves the hooks package marker
// documents the intentional default-off inertness, cites the pinned host
// version, and states the no-Git/Beads-hooks guarantee.
func TestCodexHooksMarkerIsDefaultOffAndHookSafe(t *testing.T) {
	t.Parallel()

	got := renderCodexHooksMarker()
	for _, want := range []string{
		"default-off",
		codexHostVersionLabel(),
		"no harness hook runtime",
		"never installs Git hooks",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hooks marker is missing %q\n--- marker ---\n%s", want, got)
		}
	}
}

// TestCodexHostVersionLabelTracksContract proves the host-version label is
// derived from the pinned contract identity (so generated prose can never drift
// from the contract the target lowers against).
func TestCodexHostVersionLabelTracksContract(t *testing.T) {
	t.Parallel()

	id := CodexRuntimeContractID().String()
	label := codexHostVersionLabel()
	if !strings.HasSuffix(id, label) {
		t.Fatalf("host version label %q is not the suffix of contract id %q", label, id)
	}
	if label == "" || strings.ContainsAny(label, "@/") {
		t.Fatalf("host version label %q is not a clean version", label)
	}
}

package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/runtime"
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
	metadata, err := lifecycleMetadata(runtime.Codex0_146_0Lifecycle(), "0.146.0", codexNativeFields)
	if err != nil {
		t.Fatalf("lifecycleMetadata: %v", err)
	}
	first, err := renderCodexHooksConfig(metadata.Events)
	if err != nil {
		t.Fatalf("first config render: %v", err)
	}
	second, err := renderCodexHooksConfig(metadata.Events)
	if err != nil {
		t.Fatalf("second config render: %v", err)
	}
	if first != second {
		t.Fatal("renderCodexHooksConfig is not deterministic")
	}
}

// TestCodexHooksConfigCoversPinnedEvents proves the executable hook inventory is
// exactly the runtime contract's closed event set.
func TestCodexHooksConfigCoversPinnedEvents(t *testing.T) {
	t.Parallel()
	metadata, err := lifecycleMetadata(runtime.Codex0_146_0Lifecycle(), "0.146.0", codexNativeFields)
	if err != nil {
		t.Fatalf("lifecycleMetadata: %v", err)
	}
	wire, err := renderCodexHooksConfig(metadata.Events)
	if err != nil {
		t.Fatalf("renderCodexHooksConfig: %v", err)
	}
	var config codexHooksConfig
	if err := json.Unmarshal([]byte(wire), &config); err != nil {
		t.Fatalf("decode hooks config: %v", err)
	}
	if len(config.Hooks) != len(runtime.CodexLifecycleEvents()) {
		t.Fatalf("hook event count = %d, want %d", len(config.Hooks), len(runtime.CodexLifecycleEvents()))
	}
	for _, event := range runtime.CodexLifecycleEvents() {
		groups, ok := config.Hooks[event.NativeName()]
		if !ok || len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("event %s config = %+v, want one command hook", event, groups)
		}
		command := groups[0].Hooks[0].Command
		if !strings.Contains(command, "/hooks/events/"+event.NativeName()+".sh") {
			t.Fatalf("event %s command %q does not select its fixed-event executable", event, command)
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

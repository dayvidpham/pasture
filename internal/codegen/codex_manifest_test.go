package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestCodexGlobalHooksEmitterIsSeparateFromProjectConfiguration(t *testing.T) {
	t.Parallel()
	events := runtime.CodexLifecycleEvents()
	names := make([]string, len(events))
	for index, event := range events {
		names[index] = event.NativeName()
	}
	project, err := renderCodexHooksConfig(names)
	if err != nil {
		t.Fatal(err)
	}
	global, err := EmitCodexGlobalHooksConfig()
	if err != nil {
		t.Fatal(err)
	}
	if global.Path != ".codex/hooks.json" {
		t.Fatalf("global hooks path = %q", global.Path)
	}
	if !strings.Contains(project, "sh .codex/hooks/events/") || strings.Contains(project, "sh ~/.codex/hooks/events/") {
		t.Fatal("project hook configuration semantics changed while adding the global emitter")
	}
	if strings.Contains(global.Content, "sh .codex/hooks/events/") || !strings.Contains(global.Content, "sh ~/.codex/hooks/events/") {
		t.Fatal("global hook configuration does not exclusively address user-wide runners")
	}
}

// TestCodexManifestIsDeterministic proves the manifest renderer is pure.
func TestCodexManifestIsDeterministic(t *testing.T) {
	t.Parallel()

	if renderCodexManifest() != renderCodexManifest() {
		t.Fatal("renderCodexManifest is not deterministic")
	}
	eventNames := codexLifecycleEventNamesForTest()
	first, err := renderCodexHooksConfig(eventNames)
	if err != nil {
		t.Fatalf("first config render: %v", err)
	}
	second, err := renderCodexHooksConfig(eventNames)
	if err != nil {
		t.Fatalf("second config render: %v", err)
	}
	if first != second {
		t.Fatal("renderCodexHooksConfig is not deterministic")
	}
}

// codexLifecycleEventNamesForTest returns the pinned Codex lifecycle event
// native names in catalog order, the exact input the production transport
// renderer consumes.
func codexLifecycleEventNamesForTest() []string {
	events := runtime.CodexLifecycleEvents()
	names := make([]string, len(events))
	for i, event := range events {
		names[i] = event.NativeName()
	}
	return names
}

// TestCodexHooksConfigCoversPinnedEvents proves the executable hook inventory is
// exactly the runtime contract's closed event set.
func TestCodexHooksConfigCoversPinnedEvents(t *testing.T) {
	t.Parallel()
	wire, err := renderCodexHooksConfig(codexLifecycleEventNamesForTest())
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

// TestCodexHooksMatchersMatchAuthenticCapture pins the generated hooks.json
// matcher values for the two authentically-proven, activation-bound Codex
// 0.146.0 events to the EXACT values recorded by the irreplaceable authentic
// capture configuration. These values can never be re-verified (Codex usage is
// exhausted), so this golden freezes them against silent drift.
//
// Provenance: authentic Codex capture configuration recorded 2026-08-03 —
// SessionStart used matcher "startup", PreToolUse used matcher "*". Evidence
// report .agents.local/opencode-codex-authentic-capture-2026-08-03.md (digest
// e4af95db2b8098e90f212c0a962fa824f777ba4ec778143c2534047f47693a24); user
// clearance aura-plugins-a6h3d.
func TestCodexHooksMatchersMatchAuthenticCapture(t *testing.T) {
	t.Parallel()
	wire, err := renderCodexHooksConfig(codexLifecycleEventNamesForTest())
	if err != nil {
		t.Fatalf("renderCodexHooksConfig: %v", err)
	}
	var config codexHooksConfig
	if err := json.Unmarshal([]byte(wire), &config); err != nil {
		t.Fatalf("decode hooks config: %v", err)
	}

	requireMatcher := func(event, want string) {
		t.Helper()
		groups := config.Hooks[event]
		if len(groups) != 1 {
			t.Fatalf("event %s has %d groups, want 1", event, len(groups))
		}
		if groups[0].Matcher == nil {
			t.Fatalf("event %s matcher is omitted, want the authentic value %q", event, want)
		}
		if *groups[0].Matcher != want {
			t.Errorf("event %s matcher = %q, want authentic-capture value %q (capture 2026-08-03, clearance aura-plugins-a6h3d)", event, *groups[0].Matcher, want)
		}
	}
	// Authentic, activation-bound events pinned to their proven matchers.
	requireMatcher("SessionStart", "startup")
	requireMatcher("PreToolUse", "*")

	// Non-authentic events retain the inherited empty-matcher convention; they
	// carry no matcher evidence and are never activated in M3.
	for _, event := range []string{"PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop"} {
		groups := config.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher == nil || *groups[0].Matcher != "" {
			t.Errorf("non-authentic event %s matcher = %+v, want the inherited empty-matcher convention", event, groups)
		}
	}
	// Stop and UserPromptSubmit omit the matcher entirely.
	for _, event := range []string{"Stop", "UserPromptSubmit"} {
		groups := config.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher != nil {
			t.Errorf("event %s matcher = %+v, want the matcher omitted", event, groups)
		}
	}
}

// TestCodexCommittedHooksMatchersMatchAuthenticCapture asserts the committed
// .codex/hooks.json carries the pinned authentic matchers, guarding the shipped
// artifact (not just the renderer) against drift.
func TestCodexCommittedHooksMatchersMatchAuthenticCapture(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read committed .codex/hooks.json: %v; run make generate", err)
	}
	var config codexHooksConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode committed hooks.json: %v", err)
	}
	for event, want := range codexAuthenticMatchers {
		groups := config.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher == nil || *groups[0].Matcher != want {
			t.Errorf("committed .codex/hooks.json event %s matcher = %+v, want authentic value %q; run make generate", event, groups, want)
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

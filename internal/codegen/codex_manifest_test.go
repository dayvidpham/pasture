package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
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
	project, err := renderCodexHooksConfig(codexLifecycleEventNamesForTest())
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

// codexLifecycleEventNamesForTest returns the native names of exactly the Codex
// events the pinned activation catalog enables, in runtime catalog order — the
// exact input the production transport renderer consumes. It derives the set
// from the proved target declaration (activation.Codex0_146_0TargetEvents) and
// the generated registration manifest, independently of the production filter,
// so a filter that widened back to the whole catalog would fail these tests.
func codexLifecycleEventNamesForTest() []string {
	enabled := make(map[string]struct{})
	for _, kind := range activation.Codex0_146_0TargetEvents() {
		for _, event := range registration.Codex0_146_0().Events {
			if event.Kind == kind {
				enabled[event.NativeName] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(enabled))
	for _, event := range runtime.CodexLifecycleEvents() {
		if _, ok := enabled[event.NativeName()]; ok {
			names = append(names, event.NativeName())
		}
	}
	return names
}

// TestCodexHooksConfigCoversActivatedEvents proves the executable hook inventory
// is exactly the activated subset of the runtime contract's closed event set:
// one command hook per enabled event, and no entry at all for a withheld one.
func TestCodexHooksConfigCoversActivatedEvents(t *testing.T) {
	t.Parallel()
	activated := codexLifecycleEventNamesForTest()
	produced, err := codexEnabledEventNames()
	if err != nil {
		t.Fatalf("codexEnabledEventNames: %v", err)
	}
	if strings.Join(produced, ",") != strings.Join(activated, ",") {
		t.Fatalf("production wired event set = %v, want the activated set %v", produced, activated)
	}
	wire, err := renderCodexHooksConfig(produced)
	if err != nil {
		t.Fatalf("renderCodexHooksConfig: %v", err)
	}
	var config codexHooksConfig
	if err := json.Unmarshal([]byte(wire), &config); err != nil {
		t.Fatalf("decode hooks config: %v", err)
	}
	if len(config.Hooks) != len(activated) {
		t.Fatalf("hook event count = %d, want %d", len(config.Hooks), len(activated))
	}
	for _, event := range activated {
		groups, ok := config.Hooks[event]
		if !ok || len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("event %s config = %+v, want one command hook", event, groups)
		}
		command := groups[0].Hooks[0].Command
		if !strings.Contains(command, "/hooks/events/"+event+".sh") {
			t.Fatalf("event %s command %q does not select its fixed-event executable", event, command)
		}
	}
	for _, event := range runtime.CodexLifecycleEvents() {
		if _, wired := config.Hooks[event.NativeName()]; wired {
			continue
		}
		for _, name := range activated {
			if name == event.NativeName() {
				t.Fatalf("activated event %s has no hooks configuration entry", name)
			}
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

	// No non-activated event is wired at all: the transport carries only the
	// activated set, so a withheld event owns no hooks configuration entry.
	for _, event := range []string{"PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop", "Stop", "UserPromptSubmit"} {
		if groups, wired := config.Hooks[event]; wired {
			t.Errorf("withheld event %s is wired as %+v; the transport must carry only activated events", event, groups)
		}
	}

	// The renderer keeps its matcher convention for the events a later
	// activation may admit: an empty matcher where no capture evidence exists,
	// and an omitted matcher for the two events that never carried one. This
	// checks the renderer alone; the wired set above stays the activated set.
	full := make([]string, 0)
	for _, event := range runtime.CodexLifecycleEvents() {
		full = append(full, event.NativeName())
	}
	futureWire, err := renderCodexHooksConfig(full)
	if err != nil {
		t.Fatalf("renderCodexHooksConfig over the full catalog: %v", err)
	}
	var futureConfig codexHooksConfig
	if err := json.Unmarshal([]byte(futureWire), &futureConfig); err != nil {
		t.Fatalf("decode full-catalog hooks config: %v", err)
	}
	for _, event := range []string{"PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop"} {
		groups := futureConfig.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher == nil || *groups[0].Matcher != "" {
			t.Errorf("non-authentic event %s matcher = %+v, want the inherited empty-matcher convention", event, groups)
		}
	}
	for _, event := range []string{"Stop", "UserPromptSubmit"} {
		groups := futureConfig.Hooks[event]
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

// TestCodexEnabledEventNamesFailsClosed drives every fail-closed branch of the
// Codex transport event-set derivation through its injection seam.
//
// The four inputs a drifted catalog can present — an invalid decision, a
// duplicated decision, a generated event with no decision, and a decision that
// enables an event the runtime lifecycle catalog does not carry — must all stop
// generation. If any of them returned a partial set instead, generation would
// silently ship a transport that disagrees with the activation audit report,
// which is the exact defect the transport parity check exists to prevent.
//
// Production callers pass the pinned sources; only this test injects mutated
// ones, so the branches are reachable without a build tag or a test-only export.
func TestCodexEnabledEventNamesFailsClosed(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_146_0()
	states, err := activation.Codex0_146_0()
	if err != nil {
		t.Fatalf("activation.Codex0_146_0: %v", err)
	}
	catalog := runtime.CodexLifecycleEvents()
	enabledName := "SessionStart"

	for name, tc := range map[string]struct {
		mutateStates  func([]activation.Entry) []activation.Entry
		mutateCatalog func([]runtime.CodexLifecycleEvent) []runtime.CodexLifecycleEvent
		want          string
		// alsoByKind marks the cases the index builder itself must reject, so
		// the injection seam is proven at both helper boundaries.
		alsoByKind bool
	}{
		"invalid-state": {
			mutateStates: func(in []activation.Entry) []activation.Entry { in[0].State = 0; return in },
			want:         "is invalid",
			alsoByKind:   true,
		},
		"invalid-reason": {
			mutateStates: func(in []activation.Entry) []activation.Entry { in[1].Reason = 0; return in },
			want:         "is invalid",
			alsoByKind:   true,
		},
		"duplicate": {
			mutateStates: func(in []activation.Entry) []activation.Entry { return append(in, in[0]) },
			want:         "duplicate activation entry",
			alsoByKind:   true,
		},
		"missing": {
			mutateStates: func(in []activation.Entry) []activation.Entry { return in[1:] },
			want:         "has no activation entry",
		},
		"enabled-event-outside-runtime-catalog": {
			mutateCatalog: func(in []runtime.CodexLifecycleEvent) []runtime.CodexLifecycleEvent {
				out := make([]runtime.CodexLifecycleEvent, 0, len(in))
				for _, event := range in {
					if event.NativeName() == enabledName {
						continue
					}
					out = append(out, event)
				}
				return out
			},
			want: "the pinned runtime lifecycle catalog does not carry",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mutatedStates := append([]activation.Entry(nil), states...)
			if tc.mutateStates != nil {
				mutatedStates = tc.mutateStates(mutatedStates)
			}
			mutatedCatalog := append([]runtime.CodexLifecycleEvent(nil), catalog...)
			if tc.mutateCatalog != nil {
				mutatedCatalog = tc.mutateCatalog(mutatedCatalog)
			}

			names, err := codexEnabledEventNamesFrom(manifest, mutatedStates, mutatedCatalog)
			if err == nil {
				t.Fatalf("codexEnabledEventNamesFrom returned %v and no error; the drifted input must stop generation", names)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want an actionable message containing %q", err, tc.want)
			}
			if names != nil {
				t.Fatalf("codexEnabledEventNamesFrom returned %v with an error; a failed derivation must yield no event set", names)
			}

			if !tc.alsoByKind {
				return
			}
			byKind, err := codexActivationByKindFrom(mutatedStates)
			if err == nil {
				t.Fatalf("codexActivationByKindFrom accepted %d drifted decisions; it must reject them", len(byKind))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("codexActivationByKindFrom error = %v, want an actionable message containing %q", err, tc.want)
			}
		})
	}
}

// TestCodexEnabledEventNamesInjectionMatchesProduction proves the injection
// seam is the same code path production uses: called with the pinned sources it
// returns exactly what the production helper returns. Without this, the
// fail-closed matrix above could pass against a divergent test-only path.
func TestCodexEnabledEventNamesInjectionMatchesProduction(t *testing.T) {
	t.Parallel()
	states, err := activation.Codex0_146_0()
	if err != nil {
		t.Fatalf("activation.Codex0_146_0: %v", err)
	}
	injected, err := codexEnabledEventNamesFrom(registration.Codex0_146_0(), states, runtime.CodexLifecycleEvents())
	if err != nil {
		t.Fatalf("codexEnabledEventNamesFrom with the pinned sources: %v", err)
	}
	production, err := codexEnabledEventNames()
	if err != nil {
		t.Fatalf("codexEnabledEventNames: %v", err)
	}
	if strings.Join(injected, ",") != strings.Join(production, ",") {
		t.Fatalf("injected seam produced %v, production produced %v; the seam must be one code path, not two", injected, production)
	}
}

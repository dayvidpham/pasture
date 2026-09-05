// Package codegen - Codex plugin manifest and lifecycle hook package.
//
// codexManifestEmitter emits the Codex plugin manifest at `.codex/codex.toml`
// plus the pinned host hook configuration and per-event exec-only sh runners
// under `.codex/hooks/`.
//
// The manifest declares the three independently selectable Codex packages
// (skills, agents, hooks), each by its stable component identity and package
// root, plus the pinned RuntimeContractID the packages were generated against.
// The hooks package remains default-off at the Pasture component-selection
// boundary. When selected, Codex loads the generated per-event command runners;
// none of these files installs a Git hook or changes core.hooksPath.
//
// Transport shape (Codex at the recorded version, Python-free per #65 and Phase 8 decision 2):
// each `.codex/hooks.json` entry is a PLAIN command string — `sh
// .codex/hooks/events/<Event>.sh` — with no host-side ${VAR} expansion, because
// authentic capture only proves plain-command-string invocation. Each runner is
// exec-only: it resolves the built Pasture CLI (PASTURE_BIN override) and execs
// `pasture hook lifecycle --harness codex --event <Event> --host-version
// <version>`, passing the native event JSON through by exec stdin inheritance.
// The runner performs zero JSON handling and zero branching; all semantic
// derivation and native continuation encoding live Go-side in the CLI. No
// generated Python adapter and no caller-selected PASTURE_ADAPTER_* env remain
// on this path.
package codegen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexManifestSchema tags the Codex plugin manifest so a consumer can reject
// an incompatible manifest shape.
const codexManifestSchema = "pasture.codex.manifest.v1"

// codexActivationReportRelPath is the committed Codex activation audit report:
// a target-level file directly under .codex/, owned by no package (like
// .codex/hooks.json). Kept as a shared const so the emitter and the target
// partitioner (isCodexTargetManifestPath) agree on the exact path.
const codexActivationReportRelPath = ".codex/pasture-codex-activation.json"

// codexManifestEmitter implements ManifestEmitter for the Codex target.
type codexManifestEmitter struct{}

// Emit writes the Codex package manifest, host hook configuration, and one
// exec-only sh runner per ACTIVATED lifecycle event. It generates no Python
// adapter and no caller-selected PASTURE_ADAPTER_* env: the transport is
// mechanical and carries no activation or operation-selection logic.
//
// The event vocabulary is taken from the pinned runtime lifecycle catalog
// (runtime.CodexLifecycleEvents), never from the hidden-adapter operation
// metadata, so the transport can never smuggle a semantic operation vocabulary.
// The catalog is then narrowed to the events the activation manifest enables
// (codexEnabledEventNames), exactly as the Claude emitter narrows its own
// manifest — the transport carries only activated events. A withheld event is
// therefore never wired: it has no hooks.json entry and no runner, so the host
// never invokes it, and it can never reach the lifecycle handler as a
// validation failure.
//
// Wiring a withheld event costs the user directly. The host would spawn a
// process for every occurrence, the handler would refuse the event as withheld,
// and the host would receive a refusal diagnostic instead of a decision. The
// wiring would also contradict the committed activation audit report, which
// records that same event as withheld. The transport and the audit report must
// state one activation decision, not two.
func (codexManifestEmitter) Emit(root string, opts GenerateOptions) ([]GeneratedFile, error) {
	eventNames, err := codexEnabledEventNames()
	if err != nil {
		return nil, fmt.Errorf("codegen.codexManifestEmitter.Emit: %w", err)
	}

	config, err := renderCodexHooksConfig(eventNames)
	if err != nil {
		return nil, fmt.Errorf("codegen.codexManifestEmitter.Emit: hooks config: %w", err)
	}
	outputs := []struct {
		path    string
		content string
	}{
		{path: filepath.Join(root, ".codex", "codex.toml"), content: renderCodexManifest()},
		{path: filepath.Join(root, ".codex", "hooks.json"), content: config},
	}
	for _, name := range eventNames {
		outputs = append(outputs, struct {
			path    string
			content string
		}{
			path:    filepath.Join(root, ".codex", "hooks", "events", name+".sh"),
			content: renderCodexEventRunner(name),
		})
	}

	report, err := renderCodexActivationReport()
	if err != nil {
		return nil, fmt.Errorf("codegen.codexManifestEmitter.Emit: activation report: %w", err)
	}
	outputs = append(outputs, struct {
		path    string
		content string
	}{
		path:    filepath.Join(root, filepath.FromSlash(codexActivationReportRelPath)),
		content: report,
	})
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].path < outputs[j].path })

	files := make([]GeneratedFile, 0, len(outputs))
	for _, output := range outputs {
		generated, err := writeFullGeneratedFile(output.path, output.content, opts)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.codexManifestEmitter.Emit: write %q failed - check that output root %q is writable: %w",
				output.path, root, err,
			)
		}
		files = append(files, generated)
	}
	return files, nil
}

type codexHookCommand struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout"`
	StatusMessage string `json:"statusMessage"`
}

type codexHookGroup struct {
	Matcher *string            `json:"matcher,omitempty"`
	Hooks   []codexHookCommand `json:"hooks"`
}

type codexHooksConfig struct {
	Hooks map[string][]codexHookGroup `json:"hooks"`
}

// codexRunnerRelPath is the plain (variable-free) command path each hooks.json
// entry uses to invoke an event runner. The committed authentic Codex capture proves
// only plain-command-string invocation; host-side ${VAR} expansion inside
// hooks.json command strings is unproven and must not be relied on, so the path
// is spelled literally relative to the project root.
func codexRunnerRelPath(event string) string {
	return codexHooksRoot + "/events/" + event + ".sh"
}

// codexAuthenticMatchers pins the two authentically-proven, activation-bound
// Codex events to the EXACT matcher values recorded by the irreplaceable
// authentic capture configuration. Matcher input-selection semantics carry no
// in-tree contract backing (the host contract documents identities/semantics and
// nativeresponse documents only OUTPUT continuation), and Codex usage is
// exhausted, so these proven values can never be re-verified or regained. If a
// non-proven value ("") were shipped for these two events and the recorded Codex version selects
// differently at runtime, S5/Wave-3 activation would silently fail to fire with
// no evidence left to diagnose. These are exactly the two events M3 activates,
// so they must carry the proven matcher, not the inherited empty convention.
//
// Provenance: authentic Codex capture configuration recorded 2026-08-03 —
// SessionStart used "startup", PreToolUse used "*". Any deviation from these
// values must be justified against an in-tree contract fact (none currently
// exists).
var codexAuthenticMatchers = map[string]string{
	"SessionStart": "startup",
	"PreToolUse":   "*",
}

func renderCodexHooksConfig(eventNames []string) (string, error) {
	return renderCodexHooksConfigWithRunner(eventNames, codexRunnerRelPath)
}

func renderCodexHooksConfigWithRunner(eventNames []string, runner func(string) string) (string, error) {
	config := codexHooksConfig{Hooks: make(map[string][]codexHookGroup, len(eventNames))}
	for _, name := range eventNames {
		var matcher *string
		if proven, ok := codexAuthenticMatchers[name]; ok {
			// Authentic, activation-bound event: pin its proven matcher exactly.
			value := proven
			matcher = &value
		} else {
			switch name {
			case "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop":
				// Non-authentic events, never activated in M3: no matcher
				// evidence exists, so retain the inherited empty-matcher
				// convention rather than invent a value. Stop and
				// UserPromptSubmit omit the matcher entirely.
				value := ""
				matcher = &value
			}
		}
		config.Hooks[name] = []codexHookGroup{{
			Matcher: matcher,
			Hooks: []codexHookCommand{{
				Type:          "command",
				Command:       "sh " + runner(name),
				Timeout:       600,
				StatusMessage: "Consulting Pasture lifecycle state",
			}},
		}}
	}
	wire, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(wire) + "\n", nil
}

// EmitCodexGlobalHooksConfig returns the immutable user-wide hooks
// configuration. It is deliberately separate from the project emitter: the
// project contract remains relative to its repository, while this configuration
// reaches the globally installed runners from every working directory. The
// pinned Codex contract executes plain command strings through a shell, where
// the unquoted leading tilde is expanded to the invoking user's home directory.
func EmitCodexGlobalHooksConfig() (GeneratedFile, error) {
	names, err := codexEnabledEventNames()
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("emit immutable global Codex hooks configuration: %w", err)
	}
	content, err := renderCodexHooksConfigWithRunner(names, func(event string) string {
		return "~/.codex/hooks/events/" + event + ".sh"
	})
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("emit immutable global Codex hooks configuration: %w", err)
	}
	return GeneratedFile{Path: ".codex/hooks.json", Content: content}, nil
}

// codexActivationByKind derives the exhaustive Codex activation decisions from
// the pinned activation catalog and indexes them by contract event kind. It
// fails closed on an invalid or duplicated decision, so a drifted catalog stops
// generation instead of shipping a partial transport or a partial audit report.
func codexActivationByKind() (map[model.ContractEventKind]activation.Entry, error) {
	states, err := activation.Codex0_146_0()
	if err != nil {
		return nil, fmt.Errorf("build activation manifest: %w", err)
	}
	return codexActivationByKindFrom(states)
}

// codexActivationByKindFrom is the injectable body of codexActivationByKind. It
// takes the activation decisions as an argument instead of reading the pinned
// catalog, so a test can drive every fail-closed branch with a mutated decision
// set. Production callers pass activation.Codex0_146_0().
func codexActivationByKindFrom(states []activation.Entry) (map[model.ContractEventKind]activation.Entry, error) {
	stateByKind := make(map[model.ContractEventKind]activation.Entry, len(states))
	for _, state := range states {
		if !state.IsValid() {
			return nil, fmt.Errorf("activation entry for event %d is invalid; construct it with activation.NewEnabled or activation.NewWithheld", state.Event)
		}
		if _, duplicate := stateByKind[state.Event]; duplicate {
			return nil, fmt.Errorf("duplicate activation entry for event %d; provide exactly one decision per generated event", state.Event)
		}
		stateByKind[state.Event] = state
	}
	return stateByKind, nil
}

// codexEnabledEventNames returns the native names of exactly the Codex events
// the activation manifest enables, in pinned runtime catalog order. It is the
// single event set every Codex transport artifact wires: the project hooks
// configuration, the per-event runners, and the immutable global hooks
// configuration all derive from it, so those three can never disagree.
//
// The order and the vocabulary come from the runtime catalog; only membership
// comes from activation. An enabled event that the runtime catalog does not
// carry is a contract drift, not a transport decision, so it fails generation
// rather than silently disappearing from the wiring.
func codexEnabledEventNames() ([]string, error) {
	states, err := activation.Codex0_146_0()
	if err != nil {
		return nil, fmt.Errorf("codegen.codexEnabledEventNames: build activation manifest: %w", err)
	}
	return codexEnabledEventNamesFrom(registration.Codex0_146_0(), states, runtime.CodexLifecycleEvents())
}

// codexEnabledEventNamesFrom is the injectable body of codexEnabledEventNames.
// It takes the generated registration manifest, the activation decisions, and
// the runtime lifecycle catalog as arguments instead of reading the three pinned
// sources, so a test can drive every fail-closed branch — invalid decision,
// duplicate decision, missing decision, and an enabled event the catalog does
// not carry. Production callers pass the pinned sources unchanged.
func codexEnabledEventNamesFrom(manifest registration.Manifest, states []activation.Entry, catalog []runtime.CodexLifecycleEvent) ([]string, error) {
	stateByKind, err := codexActivationByKindFrom(states)
	if err != nil {
		return nil, fmt.Errorf("codegen.codexEnabledEventNames: %w", err)
	}
	enabled := make(map[string]struct{}, len(stateByKind))
	for _, event := range manifest.Events {
		state, present := stateByKind[event.Kind]
		if !present {
			return nil, fmt.Errorf(
				"codegen.codexEnabledEventNames: generated event %q has no activation entry; add one exhaustive typed decision in internal/lifecycle/activation/codex_0_146_0.go before generating the transport",
				event.NativeName)
		}
		if state.State == activation.Enabled {
			enabled[event.NativeName] = struct{}{}
		}
	}

	names := make([]string, 0, len(enabled))
	for _, event := range catalog {
		name := event.NativeName()
		if _, ok := enabled[name]; ok {
			names = append(names, name)
			delete(enabled, name)
		}
	}
	if len(enabled) != 0 {
		missing := make([]string, 0, len(enabled))
		for name := range enabled {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"codegen.codexEnabledEventNames: activation enables %v, which the pinned runtime lifecycle catalog does not carry; the activation catalog drifted from runtime.CodexLifecycleEvents — align both against the same pinned Codex contract before generating",
			missing)
	}
	return names, nil
}

// renderCodexActivationReport builds the committed Codex activation audit report
// written to .codex/pasture-codex-activation.json. It is emitted
// UNCONDITIONALLY (Claude precedent, emitClaudeHooks): every generated Codex
// event appears exactly once, carrying either its typed withholding reason or —
// for the two authentically-proven, activation-bound events (SessionStart,
// PreToolUse) — the event-bound capture and production proofs. The withheld
// dispositions are the audit payload, not a side effect of enablement.
//
// The report derives solely from the pinned Codex registration manifest
// (registration.Codex0_146_0()) and activation catalog
// (activation.Codex0_146_0()) — no filesystem access and no live Codex — reusing
// the shared activationSupportReport shape so the Claude and Codex audit reports
// can never diverge. The exhaustiveness checks reject any disagreement between
// the generated catalog and the activation decisions (invalid/duplicate/missing/
// non-manifest entry), so a drifted catalog fails generation rather than
// silently shipping a partial audit.
// RenderCodexActivationReport renders the committed Codex activation audit
// report exactly as generation writes it, so a test can hold the committed
// artifact to the product's own emitter instead of to a second copy of the
// report shape.
func RenderCodexActivationReport() (string, error) {
	return renderCodexActivationReport()
}

func renderCodexActivationReport() (string, error) {
	manifest := registration.Codex0_146_0()
	states, err := activation.Codex0_146_0()
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexActivationReport: build activation manifest: %w", err)
	}
	report, err := buildActivationSupportReport("codegen.renderCodexActivationReport", manifest, states)
	if err != nil {
		return "", err
	}
	wire, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("codegen.renderCodexActivationReport: marshal activation report: %w", err)
	}
	return string(wire) + "\n", nil
}

// renderCodexEventRunner emits the exec-only sh runner for one lifecycle event.
// The runner resolves the built Pasture CLI (honoring the PASTURE_BIN override)
// and execs `pasture hook lifecycle --harness codex --event <Event>
// --host-version <version>`. Exec replaces the shell process, so the exact
// native event JSON on stdin passes straight through by inheritance. The runner
// performs zero JSON handling and zero branching; the harness, event, and
// host-version tokens are derived from pinned sources so they can never drift
// from the contract the CLI records against.
func renderCodexEventRunner(event string) string {
	return "#!/usr/bin/env sh\n" +
		"# Code generated by Pasture. DO NOT EDIT.\n" +
		"exec \"${" + adapterBinaryEnv + ":-pasture}\" hook lifecycle" +
		" --harness " + string(HarnessCodex) +
		" --event " + event +
		" --host-version " + codexHostVersionLabel() + "\n"
}

// renderCodexManifest builds the deterministic `.codex/codex.toml` content. The
// package roots are written in slash form so the manifest is portable across
// host path separators.
func renderCodexManifest() string {
	var b strings.Builder
	b.WriteString("# Code-generated by Pasture; DO NOT EDIT.\n")
	b.WriteString("# Codex plugin manifest: three independently selectable packages.\n")
	fmt.Fprintf(&b, "schema = %s\n", tomlString(codexManifestSchema))
	fmt.Fprintf(&b, "name = %s\n", tomlString("pasture-codex"))
	fmt.Fprintf(&b, "runtime_contract = %s\n", tomlString(CodexRuntimeContractID().String()))
	b.WriteString("\n")

	b.WriteString("[packages.skills]\n")
	fmt.Fprintf(&b, "id = %s\n", tomlString(codexSkillsComponent.String()))
	fmt.Fprintf(&b, "path = %s\n", tomlString(codexSkillRoot))
	b.WriteString("enabled = true\n")
	b.WriteString("\n")

	b.WriteString("[packages.agents]\n")
	fmt.Fprintf(&b, "id = %s\n", tomlString(codexAgentsComponent.String()))
	fmt.Fprintf(&b, "path = %s\n", tomlString(codexAgentsRoot))
	b.WriteString("enabled = true\n")
	b.WriteString("\n")

	b.WriteString("[packages.hooks]\n")
	fmt.Fprintf(&b, "id = %s\n", tomlString(codexHooksComponent.String()))
	fmt.Fprintf(&b, "path = %s\n", tomlString(codexHooksRoot))
	b.WriteString("enabled = false\n")
	return b.String()
}

// codexHostVersionLabel returns the pinned Codex host version spelling used in
// generated prose, derived from the pinned RuntimeContractID so the label can
// never drift from the contract the target lowers against.
func codexHostVersionLabel() string {
	id := CodexRuntimeContractID().String()
	if at := strings.LastIndexByte(id, '@'); at >= 0 {
		return id[at+1:]
	}
	return id
}

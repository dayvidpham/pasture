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
// Transport shape (Codex 0.146.0, Python-free per #65 and Phase 8 decision 2):
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

	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexManifestSchema tags the Codex plugin manifest so a consumer can reject
// an incompatible manifest shape.
const codexManifestSchema = "pasture.codex.manifest.v1"

// codexManifestEmitter implements ManifestEmitter for the Codex target.
type codexManifestEmitter struct{}

// Emit writes the Codex package manifest, host hook configuration, and one
// exec-only sh runner per pinned lifecycle event. It generates no Python adapter
// and no caller-selected PASTURE_ADAPTER_* env: the transport is mechanical and
// carries no activation or operation-selection logic.
//
// The event set is taken directly from the pinned runtime lifecycle catalog
// (runtime.CodexLifecycleEvents), never from the hidden-adapter operation
// metadata, so the transport can never smuggle a semantic operation vocabulary.
func (codexManifestEmitter) Emit(root string, opts GenerateOptions) ([]GeneratedFile, error) {
	events := runtime.CodexLifecycleEvents()
	eventNames := make([]string, len(events))
	for i, event := range events {
		eventNames[i] = event.NativeName()
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
// entry uses to invoke an event runner. Codex 0.146.0 authentic capture proves
// only plain-command-string invocation; host-side ${VAR} expansion inside
// hooks.json command strings is unproven and must not be relied on, so the path
// is spelled literally relative to the project root.
func codexRunnerRelPath(event string) string {
	return codexHooksRoot + "/events/" + event + ".sh"
}

func renderCodexHooksConfig(eventNames []string) (string, error) {
	config := codexHooksConfig{Hooks: make(map[string][]codexHookGroup, len(eventNames))}
	for _, name := range eventNames {
		var matcher *string
		switch name {
		case "SessionStart", "PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop":
			value := ""
			matcher = &value
		}
		config.Hooks[name] = []codexHookGroup{{
			Matcher: matcher,
			Hooks: []codexHookCommand{{
				Type:          "command",
				Command:       "sh " + codexRunnerRelPath(name),
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

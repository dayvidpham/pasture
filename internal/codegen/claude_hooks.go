package codegen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// ClaudeHooksConfigPath and ClaudeActivationReportPath are the two files the
// Claude Code lifecycle emitter writes, relative to the repository root. They
// live beside hand-authored hook payload files, so a guard that reads the
// emitted set finds them here rather than by listing the directory.
const (
	ClaudeHooksConfigPath      = "hooks/hooks.json"
	ClaudeActivationReportPath = "hooks/pasture-activation.json"
)

// adapterBinaryEnv is the environment variable a generated lifecycle transport
// reads to override the Pasture executable it invokes (default: PATH lookup of
// `pasture`). It is consumed by the Codex exec-only runner and the OpenCode
// hooks module.
const adapterBinaryEnv = "PASTURE_BIN"

type lifecycleIdentityMetadata struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

type lifecycleEventMetadata struct {
	Name           string                      `json:"name"`
	Semantic       string                      `json:"semantic"`
	Surface        string                      `json:"surface"`
	Blocking       string                      `json:"blocking"`
	Mutation       string                      `json:"mutation"`
	Order          string                      `json:"order"`
	Reconciliation string                      `json:"reconciliation"`
	Failure        string                      `json:"failure"`
	StopLoop       string                      `json:"stopLoop"`
	Identities     []lifecycleIdentityMetadata `json:"identities"`
	AllowedFields  []string                    `json:"allowedFields"`
}

type lifecycleTransportMetadata struct {
	Harness  string                   `json:"harness"`
	Version  string                   `json:"version"`
	Contract string                   `json:"contract"`
	Events   []lifecycleEventMetadata `json:"events"`
}

func lifecycleMetadata[E comparable](
	contract runtime.LifecycleContract[E],
	version string,
	allowedFields func(string) []string,
) (lifecycleTransportMetadata, error) {
	metadata := lifecycleTransportMetadata{
		Harness:  string(contract.Harness()),
		Version:  version,
		Contract: contract.ID().String(),
	}
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return lifecycleTransportMetadata{}, fmt.Errorf("map lifecycle event for %s: %w", contract.ID(), err)
		}
		identities := make([]lifecycleIdentityMetadata, 0, len(mapping.Identities()))
		fields := append([]string(nil), allowedFields(mapping.NativeName())...)
		for _, identity := range mapping.Identities() {
			identities = append(identities, lifecycleIdentityMetadata{
				Name:     identity.NativeName(),
				Kind:     identity.Kind().String(),
				Required: identity.Required(),
			})
			fields = append(fields, identity.NativeName())
		}
		fields = sortedUniqueStrings(fields)
		metadata.Events = append(metadata.Events, lifecycleEventMetadata{
			Name:           mapping.NativeName(),
			Semantic:       mapping.Semantic().String(),
			Surface:        mapping.Surface().String(),
			Blocking:       mapping.Blocking().String(),
			Mutation:       mapping.Mutation().String(),
			Order:          mapping.Order().String(),
			Reconciliation: mapping.Reconciliation().String(),
			Failure:        mapping.Failure().String(),
			StopLoop:       mapping.StopLoop().String(),
			Identities:     identities,
			AllowedFields:  fields,
		})
	}
	return metadata, nil
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func claudeNativeFields(event string) []string {
	manifest := registration.ClaudeCode2_1_261()
	fieldNames := registration.ClaudeCode2_1_261NativeFieldNames()
	for _, candidate := range manifest.Events {
		if candidate.NativeName != event {
			continue
		}
		fields := make([]string, 0, len(candidate.AllowedFields))
		for _, field := range candidate.AllowedFields {
			fields = append(fields, fieldNames[field])
		}
		return fields
	}
	return nil
}

type claudeHooksEmitter struct{}

type claudeHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type claudeHookGroup struct {
	Matcher string              `json:"matcher"`
	Hooks   []claudeHookCommand `json:"hooks"`
}

type claudeHooksConfig struct {
	Description string                       `json:"description"`
	Hooks       map[string][]claudeHookGroup `json:"hooks"`
}

func (claudeHooksEmitter) Emit(root string, opts GenerateOptions) ([]GeneratedFile, error) {
	manifest := registration.ClaudeCode2_1_261()
	states, err := activation.ClaudeCode2_1_261()
	if err != nil {
		return nil, fmt.Errorf("codegen.claudeHooksEmitter.Emit: build activation manifest: %w", err)
	}
	return emitClaudeHooks(root, opts, manifest, states)
}

func emitClaudeHooks(root string, opts GenerateOptions, manifest registration.Manifest, states []activation.Entry) ([]GeneratedFile, error) {
	stateByKind := make(map[model.ContractEventKind]activation.Entry, len(states))
	for _, state := range states {
		if !state.IsValid() {
			return nil, fmt.Errorf("codegen.emitClaudeHooks: activation entry for event %d is invalid; construct it with activation.NewEnabled or activation.NewWithheld", state.Event)
		}
		if _, duplicate := stateByKind[state.Event]; duplicate {
			return nil, fmt.Errorf("codegen.emitClaudeHooks: duplicate activation entry for event %d; provide exactly one decision per generated event", state.Event)
		}
		stateByKind[state.Event] = state
	}

	config := claudeHooksConfig{
		Description: "Pasture lifecycle adapters and shared-worktree git discipline for the pinned Claude Code contract.",
		Hooks:       make(map[string][]claudeHookGroup, len(manifest.Events)),
	}
	support, err := buildActivationSupportReport("codegen.emitClaudeHooks", manifest, states)
	if err != nil {
		return nil, err
	}
	for _, event := range manifest.Events {
		state, present := stateByKind[event.Kind]
		if !present {
			return nil, fmt.Errorf("codegen.emitClaudeHooks: generated event %q has no activation entry; add one exhaustive typed decision", event.NativeName)
		}
		if state.State == activation.Enabled {
			command := claudeHookCommand{Type: "command", Command: fmt.Sprintf(`${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event %s --host-version "${CLAUDE_CODE_VERSION:-unknown}"`, event.NativeName), Timeout: 10}
			config.Hooks[event.NativeName] = []claudeHookGroup{{Matcher: "", Hooks: []claudeHookCommand{command}}}
		}
	}
	if len(stateByKind) != len(manifest.Events) {
		return nil, fmt.Errorf("codegen.emitClaudeHooks: activation has %d entries for %d generated events; remove non-manifest entries and provide one exact decision per event", len(stateByKind), len(manifest.Events))
	}
	if groups := config.Hooks["SessionStart"]; len(groups) > 0 {
		groups[0].Hooks = append([]claudeHookCommand{{Type: "command", Command: "cat ${CLAUDE_PLUGIN_ROOT}/hooks/bd-prime.md 2>&1"}}, groups[0].Hooks...)
		config.Hooks["SessionStart"] = groups
	}
	if groups := config.Hooks["PreCompact"]; len(groups) > 0 {
		groups[0].Hooks = append([]claudeHookCommand{{Type: "command", Command: "cat ${CLAUDE_PLUGIN_ROOT}/hooks/bd-prime.md 2>&1"}}, groups[0].Hooks...)
		config.Hooks["PreCompact"] = groups
	}
	config.Hooks["PreToolUse"] = append([]claudeHookGroup{{
		Matcher: "Bash",
		Hooks: []claudeHookCommand{{
			Type: "command", Command: "bash ${CLAUDE_PLUGIN_ROOT}/hooks/scripts/git-discipline.sh", Timeout: 10,
		}},
	}}, config.Hooks["PreToolUse"]...)

	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("codegen.claudeHooksEmitter.Emit: marshal hooks config: %w", err)
	}
	configJSON = append(configJSON, '\n')
	supportJSON, err := json.MarshalIndent(support, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("codegen.claudeHooksEmitter.Emit: marshal activation support: %w", err)
	}
	supportJSON = append(supportJSON, '\n')

	outputs := []struct {
		path    string
		content string
	}{
		{path: filepath.Join(root, filepath.FromSlash(ClaudeHooksConfigPath)), content: string(configJSON)},
		{path: filepath.Join(root, filepath.FromSlash(ClaudeActivationReportPath)), content: string(supportJSON)},
	}
	files := make([]GeneratedFile, 0, len(outputs))
	for _, output := range outputs {
		generated, err := writeFullGeneratedFile(output.path, output.content, opts)
		if err != nil {
			return nil, fmt.Errorf("codegen.claudeHooksEmitter.Emit: write %q: %w", output.path, err)
		}
		files = append(files, generated)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

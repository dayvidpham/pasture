package codegen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const (
	claudeLifecycleScriptPath = "hooks/scripts/pasture-lifecycle.py"
	adapterEventEnv           = "PASTURE_ADAPTER_EVENT"
	adapterOperationEnv       = "PASTURE_ADAPTER_OPERATION"
	adapterInputEnv           = "PASTURE_ADAPTER_INPUT"
	adapterBinaryEnv          = "PASTURE_BIN"
)

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

type lifecycleAdapterMetadata struct {
	Harness    string                                 `json:"harness"`
	Version    string                                 `json:"version"`
	Contract   string                                 `json:"contract"`
	Events     []lifecycleEventMetadata               `json:"events"`
	Operations map[string][]handlers.AdapterOperation `json:"operations"`
}

// adapterOperationsBySemantic is a complete, disjoint partition of the hidden
// adapter's operation vocabulary. Native observations may record only
// assignment-controlled producer facts, gate consultations may perform only the
// read-only interaction-mode query, and only an explicit human-response event
// may submit one of the five human-gated decisions.
func adapterOperationsBySemantic() map[string][]handlers.AdapterOperation {
	return map[string][]handlers.AdapterOperation{
		runtime.SemanticObservation.String(): {
			handlers.AdapterOperationStartReview,
			handlers.AdapterOperationSubmitPlanReview,
			handlers.AdapterOperationSubmitImplementationReview,
			handlers.AdapterOperationFinalizeReview,
			handlers.AdapterOperationCreateSlice,
			handlers.AdapterOperationSetSliceCandidate,
			handlers.AdapterOperationCloseSlice,
			handlers.AdapterOperationCreateIntegrationCandidate,
			handlers.AdapterOperationPublishRepository,
		},
		runtime.SemanticGateConsultation.String(): {
			handlers.AdapterOperationShowInteractionMode,
		},
		runtime.SemanticExplicitHumanResponse.String(): {
			handlers.AdapterOperationSetInteractionMode,
			handlers.AdapterOperationRecordPlanUAT,
			handlers.AdapterOperationRatifyPlan,
			handlers.AdapterOperationRecordImplementationUAT,
			handlers.AdapterOperationLand,
		},
	}
}

func lifecycleMetadata[E comparable](
	contract runtime.LifecycleContract[E],
	version string,
	allowedFields func(string) []string,
) (lifecycleAdapterMetadata, error) {
	metadata := lifecycleAdapterMetadata{
		Harness:  string(contract.Harness()),
		Version:  version,
		Contract: contract.ID().String(),
	}
	seenSemantics := make(map[string]struct{})
	for _, event := range contract.Events() {
		mapping, err := contract.Mapping(event)
		if err != nil {
			return lifecycleAdapterMetadata{}, fmt.Errorf("map lifecycle event for %s: %w", contract.ID(), err)
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
		seenSemantics[mapping.Semantic().String()] = struct{}{}
	}
	metadata.Operations = make(map[string][]handlers.AdapterOperation, len(seenSemantics))
	for semantic, operations := range adapterOperationsBySemantic() {
		if _, present := seenSemantics[semantic]; present {
			metadata.Operations[semantic] = operations
		}
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
	manifest := registration.ClaudeCode2_1_210()
	fieldNames := registration.ClaudeCode2_1_210NativeFieldNames()
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

func codexNativeFields(event string) []string {
	fields := []string{
		"session_id", "cwd", "hook_event_name", "model", "permission_mode", "transcript_path",
	}
	extras := map[string][]string{
		"SessionStart":      {"source"},
		"UserPromptSubmit":  {"turn_id", "prompt"},
		"PreToolUse":        {"turn_id", "tool_name", "tool_use_id", "tool_input"},
		"PermissionRequest": {"turn_id", "tool_name", "tool_input"},
		"PostToolUse":       {"turn_id", "tool_name", "tool_use_id", "tool_input", "tool_response"},
		"PreCompact":        {"turn_id", "trigger"},
		"PostCompact":       {"turn_id", "trigger"},
		"SubagentStart":     {"turn_id", "agent_id", "agent_type"},
		"SubagentStop":      {"turn_id", "agent_id", "agent_type", "agent_transcript_path", "last_assistant_message", "stop_hook_active"},
		"Stop":              {"turn_id", "last_assistant_message", "stop_hook_active"},
	}
	return append(fields, extras[event]...)
}

func renderPythonLifecycleAdapter(metadata lifecycleAdapterMetadata) (string, error) {
	wire, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal lifecycle adapter metadata: %w", err)
	}

	const program = `#!/usr/bin/env python3
# Code generated by Pasture. DO NOT EDIT.
import argparse
import base64
import json
import os
import subprocess
import sys

METADATA = json.loads(r'''%s''')
EVENTS = {event["name"]: event for event in METADATA["events"]}
MAX_NATIVE_INPUT = 1 << 20
MAX_NATIVE_INVOCATION = 4096

class DuplicateKeyError(ValueError):
    pass

def strict_object(raw, where):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise DuplicateKeyError(f"duplicate field {key!r}")
            result[key] = value
        return result
    try:
        value = json.loads(raw, object_pairs_hook=pairs)
    except (UnicodeDecodeError, json.JSONDecodeError, DuplicateKeyError) as exc:
        raise ValueError(f"{where} is not one strict UTF-8 JSON object: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{where} must be one JSON object")
    return value

def emit_noop():
    sys.stdout.write("{}\n")

def fail(mapping, message):
    failure = mapping["failure"] if mapping else "strict-hook-failure"
    sys.stderr.write(f"Pasture lifecycle adapter failed: {message}\n")
    if failure in ("report-and-continue", "observe-only"):
        emit_noop()
        return 0
    if failure in ("exit-2-blocks", "strict-output-exit-2-blocks"):
        return 2
    return 1

def invocation_identity(mapping, payload):
    identities = []
    for identity in mapping["identities"]:
        name = identity["name"]
        if name not in payload:
            if identity["required"]:
                raise ValueError(f"event {mapping['name']!r} is missing required native identity {name!r}")
            continue
        value = payload[name]
        if not isinstance(value, str) or not value:
            raise ValueError(f"native identity {name!r} must be a non-empty string")
        identities.append([name, value])
    canonical = json.dumps(
        {"event": mapping["name"], "identities": identities},
        ensure_ascii=False,
        separators=(",", ":"),
    ).encode("utf-8")
    encoded = base64.urlsafe_b64encode(canonical).decode("ascii").rstrip("=")
    result = f"{METADATA['harness']}.{encoded}"
    if len(result.encode("utf-8")) > MAX_NATIVE_INVOCATION:
        raise ValueError("the correlated native invocation exceeds the hidden adapter's 4096-byte identity limit")
    return result

def build_envelope(mapping, payload, operation, raw_input):
    allowed = METADATA["operations"].get(mapping["semantic"], [])
    if operation not in allowed:
        raise ValueError(
            f"operation {operation!r} is not allowed for {mapping['semantic']!r} event {mapping['name']!r}; "
            f"allowed operations: {allowed}"
        )
    header = {
        "schema": "%s",
        "harness": METADATA["harness"],
        "harnessVersion": METADATA["version"],
        "harnessContract": METADATA["contract"],
        "nativeInvocation": invocation_identity(mapping, payload),
        "operation": operation,
    }
    # Keep operation input byte-for-byte until the production hidden handler
    # performs its one authoritative strict decode.
    prefix = json.dumps(header, ensure_ascii=False, separators=(",", ":"))[:-1]
    return (prefix + ",\"input\":" + raw_input + "}\n").encode("utf-8")

def invoke(mapping, payload):
    bound_event = os.environ.get("%s", "")
    operation = os.environ.get("%s", "")
    raw_input = os.environ.get("%s", "")
    if not bound_event and not operation and not raw_input:
        emit_noop()
        return 0
    if not bound_event or not operation or not raw_input:
        return fail(mapping, "%s, %s, and %s must either all be set or all be absent")
    if bound_event != mapping["name"]:
        emit_noop()
        return 0
    try:
        envelope = build_envelope(mapping, payload, operation, raw_input)
    except ValueError as exc:
        return fail(mapping, str(exc))
    if len(envelope) > MAX_NATIVE_INPUT:
        return fail(mapping, "the generated pasture.adapter-invocation/v1 envelope exceeds 1 MiB; remove native or transcript data from semantic input")

    binary = os.environ.get("%s", "pasture")
    try:
        completed = subprocess.run(
            [binary, "__adapter", "invoke"],
            input=envelope,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except OSError as exc:
        return fail(mapping, f"could not execute production binary {binary!r}: {exc}; set %s to the built pasture binary")
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", "replace").strip()
        return fail(mapping, f"pasture __adapter invoke exited {completed.returncode}: {detail or 'no diagnostic was returned'}")
    try:
        result = strict_object(completed.stdout, "pasture adapter result")
    except ValueError as exc:
        return fail(mapping, str(exc))
    if set(result) != {"schema", "operation", "result"}:
        return fail(mapping, "pasture adapter result must contain exactly schema, operation, and result")
    if result["schema"] != "%s" or result["operation"] != operation:
        return fail(mapping, "pasture adapter result schema or operation does not match the invocation")
    emit_noop()
    return 0

def main():
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--event", default="")
    args = parser.parse_args()
    raw = sys.stdin.buffer.read(MAX_NATIVE_INPUT + 1)
    if len(raw) > MAX_NATIVE_INPUT:
        sys.stderr.write("Pasture lifecycle adapter failed: native hook input exceeds 1 MiB\n")
        return 1
    try:
        payload = strict_object(raw, "native hook input")
        event_name = args.event or payload.get("hook_event_name", "")
        mapping = EVENTS.get(event_name)
        if mapping is None:
            raise ValueError(f"native event {event_name!r} is not in pinned contract {METADATA['contract']!r}")
        if payload.get("hook_event_name") != event_name:
            raise ValueError(f"hook_event_name must equal configured event {event_name!r}")
        unknown = sorted(set(payload) - set(mapping["allowedFields"]))
        if unknown:
            raise ValueError(f"event {event_name!r} has unknown fields {unknown}")
        if mapping["stopLoop"] == "consult-when-inactive":
            active = payload.get("stop_hook_active")
            if not isinstance(active, bool):
                raise ValueError(f"event {event_name!r} requires boolean stop_hook_active")
            if active:
                emit_noop()
                return 0
        if mapping["blocking"] == "conditionally-blocking" and payload.get("config_source") == "policy_settings":
            mapping = dict(mapping)
            mapping["failure"] = "report-and-continue"
        return invoke(mapping, payload)
    except ValueError as exc:
        mapping = EVENTS.get(args.event or payload.get("hook_event_name", "")) if 'payload' in locals() else None
        return fail(mapping, str(exc))

if __name__ == "__main__":
    raise SystemExit(main())
`

	return fmt.Sprintf(
		program,
		string(wire),
		handlers.AdapterInvocationSchema,
		adapterEventEnv,
		adapterOperationEnv,
		adapterInputEnv,
		adapterEventEnv,
		adapterOperationEnv,
		adapterInputEnv,
		adapterBinaryEnv,
		adapterBinaryEnv,
		handlers.AdapterResultSchema,
	), nil
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
	manifest := registration.ClaudeCode2_1_210()
	states, err := activation.ClaudeCode2_1_210()
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
	type supportEntry struct {
		Event           string `json:"event"`
		State           string `json:"state"`
		Reason          string `json:"reason,omitempty"`
		CaptureProof    string `json:"captureProof,omitempty"`
		ProductionProof string `json:"productionProof,omitempty"`
	}
	support := struct {
		Harness  string         `json:"harness"`
		Contract string         `json:"contract"`
		Events   []supportEntry `json:"events"`
	}{Harness: string(manifest.Harness), Contract: manifest.Contract.String()}
	for _, event := range manifest.Events {
		state, present := stateByKind[event.Kind]
		if !present {
			return nil, fmt.Errorf("codegen.emitClaudeHooks: generated event %q has no activation entry; add one exhaustive typed decision", event.NativeName)
		}
		entry := supportEntry{Event: event.NativeName, State: state.State.String(), Reason: state.Reason.String()}
		if state.State == activation.Enabled {
			entry.CaptureProof = state.CaptureProof.Name()
			entry.ProductionProof = state.ProductionProof.Name()
			command := claudeHookCommand{Type: "command", Command: fmt.Sprintf(`${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event %s --host-version "${CLAUDE_CODE_VERSION:-unknown}"`, event.NativeName), Timeout: 10}
			config.Hooks[event.NativeName] = []claudeHookGroup{{Matcher: "", Hooks: []claudeHookCommand{command}}}
		}
		support.Events = append(support.Events, entry)
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
		{path: filepath.Join(root, "hooks", "hooks.json"), content: string(configJSON)},
		{path: filepath.Join(root, "hooks", "pasture-activation.json"), content: string(supportJSON)},
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

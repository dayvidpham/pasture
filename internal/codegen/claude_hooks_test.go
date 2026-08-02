package codegen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestClaudeHooksFailClosedOnActivationMismatch(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_210()
	states, err := activation.ClaudeCode2_1_210()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]activation.Entry) []activation.Entry{
		"missing":   func(in []activation.Entry) []activation.Entry { return in[1:] },
		"duplicate": func(in []activation.Entry) []activation.Entry { return append(in, in[0]) },
		"invalid":   func(in []activation.Entry) []activation.Entry { in[0] = activation.Entry{}; return in },
	} {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := emitClaudeHooks(t.TempDir(), GenerateOptions{}, manifest, mutate(append([]activation.Entry(nil), states...)))
			if err == nil {
				t.Fatal("expected fail-closed activation error")
			}
		})
	}
}

func TestClaudeHooksStableProofNamesAndIndependentPreToolUse(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_210()
	states, err := activation.ClaudeCode2_1_210()
	if err != nil {
		t.Fatal(err)
	}
	files, err := emitClaudeHooks(t.TempDir(), GenerateOptions{}, manifest, states)
	if err != nil {
		t.Fatal(err)
	}
	var hooks, support string
	for _, file := range files {
		if strings.HasSuffix(file.Path, "hooks.json") {
			hooks = file.Content
		}
		if strings.HasSuffix(file.Path, "pasture-activation.json") {
			support = file.Content
		}
	}
	if !strings.Contains(support, `"reason": "missing-fixture"`) || !strings.Contains(support, `"captureProof":`) || strings.Contains(support, "reason-") {
		t.Fatalf("support report lacks stable named fields: %s", support)
	}
	if !strings.Contains(hooks, "git-discipline.sh") || !strings.Contains(hooks, "hook lifecycle") {
		t.Fatalf("hooks do not preserve independent and lifecycle PreToolUse disciplines: %s", hooks)
	}
}

func TestClaudeHooksAbsentSessionStartDoesNotPanic(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_210()
	states, err := activation.ClaudeCode2_1_210()
	if err != nil {
		t.Fatal(err)
	}
	for i := range states {
		if states[i].Event == registration.EventSessionStart {
			states[i], err = activation.NewWithheld(states[i].Event, activation.WithheldMissingFixture)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := emitClaudeHooks(t.TempDir(), GenerateOptions{}, manifest, states); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterOperationsBySemanticPartitionsHiddenContract(t *testing.T) {
	t.Parallel()

	groups := adapterOperationsBySemantic()
	seen := make(map[handlers.AdapterOperation]string)
	for semantic, operations := range groups {
		for _, operation := range operations {
			if previous, duplicate := seen[operation]; duplicate {
				t.Fatalf("operation %q appears in both %s and %s", operation, previous, semantic)
			}
			seen[operation] = semantic
		}
	}
	want := handlers.SupportedAdapterOperations()
	if len(seen) != len(want) {
		t.Fatalf("partition has %d operations, hidden contract has %d", len(seen), len(want))
	}
	for _, operation := range want {
		if _, ok := seen[operation]; !ok {
			t.Errorf("hidden operation %q is not classified", operation)
		}
	}

	human := groups[runtime.SemanticExplicitHumanResponse.String()]
	for _, operation := range human {
		if seen[operation] != runtime.SemanticExplicitHumanResponse.String() {
			t.Errorf("human operation %q escaped the explicit-response group", operation)
		}
	}
	if !slices.Contains(groups[runtime.SemanticGateConsultation.String()], handlers.AdapterOperationShowInteractionMode) {
		t.Error("gate consultations do not expose the read-only interaction-mode query")
	}
}

func TestLifecycleAdapterTargetsUnsupportedAndAbsentHarnesses(t *testing.T) {
	t.Parallel()

	_, err := ResolveHarness([]string{string(HarnessAntigravity)})
	if !errors.Is(err, runtime.ErrLifecycleAdapterUnsupported) {
		t.Fatalf("Antigravity error = %v, want ErrLifecycleAdapterUnsupported", err)
	}
	_, err = ResolveHarness([]string{"pi"})
	if err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("Pi resolution = %v, want no registered adapter", err)
	}
}

func TestLifecycleIdentityFieldsBelongToPinnedPayloadShapes(t *testing.T) {
	t.Parallel()

	t.Run("Claude", func(t *testing.T) {
		assertIdentityFieldsInPayloadShape(
			t,
			runtime.ClaudeCode2_1_210Lifecycle(),
			runtime.ClaudeLifecycleEvents(),
			"claudeNativeFields",
			claudeNativeFields,
		)
	})
	t.Run("Codex", func(t *testing.T) {
		assertIdentityFieldsInPayloadShape(
			t,
			runtime.Codex0_144_1Lifecycle(),
			runtime.CodexLifecycleEvents(),
			"codexNativeFields",
			codexNativeFields,
		)
	})
}

func assertIdentityFieldsInPayloadShape[E comparable](
	t *testing.T,
	contract runtime.LifecycleContract[E],
	events []E,
	payloadTable string,
	payloadFields func(string) []string,
) {
	t.Helper()
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		if err != nil {
			t.Fatalf("read lifecycle identity table for event %v: %v", event, err)
		}
		declaredPayloadFields := payloadFields(mapping.NativeName())
		for _, identity := range mapping.Identities() {
			if !slices.Contains(declaredPayloadFields, identity.NativeName()) {
				t.Errorf(
					"event %q identity field %q is declared by the runtime lifecycle identity table but absent from the pinned payload shape; edit %s in internal/codegen/claude_hooks.go",
					mapping.NativeName(), identity.NativeName(), payloadTable,
				)
			}
		}
	}
}

func TestClaudeLifecycleAdapterInvokesStrictHiddenEnvelope(t *testing.T) {
	t.Parallel()

	metadata, err := lifecycleMetadata(runtime.ClaudeCode2_1_210Lifecycle(), "2.1.210", claudeNativeFields)
	if err != nil {
		t.Fatalf("lifecycleMetadata: %v", err)
	}
	script, err := renderPythonLifecycleAdapter(metadata)
	if err != nil {
		t.Fatalf("renderPythonLifecycleAdapter: %v", err)
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "pasture lifecycle.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatalf("write generated adapter: %v", err)
	}
	capture := filepath.Join(dir, "captured envelope.json")
	binary := writeFakePasture(t, dir, handlers.AdapterOperationSetInteractionMode)
	native := readNativeFixture(t, "claude", "elicitation_result.json")
	rawInput := `{"epoch":"epoch--00000000-0000-0000-0000-000000000001","mode":"normal","actor":"human--00000000-0000-0000-0000-000000000002"}`

	stdout, stderr, exitCode := runPythonAdapter(t, scriptPath, nil, native, map[string]string{
		adapterEventEnv:        "ElicitationResult",
		adapterOperationEnv:    string(handlers.AdapterOperationSetInteractionMode),
		adapterInputEnv:        rawInput,
		adapterBinaryEnv:       binary,
		"PASTURE_TEST_CAPTURE": capture,
	})
	if exitCode != 0 {
		t.Fatalf("generated adapter exited %d\nstderr: %s", exitCode, stderr)
	}
	if stdout != "{}\n" {
		t.Fatalf("native stdout = %q, want no-op object", stdout)
	}

	wire, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read captured envelope: %v", err)
	}
	var envelope handlers.AdapterInvocationEnvelope
	if err := json.Unmarshal(wire, &envelope); err != nil {
		t.Fatalf("decode captured envelope: %v\n%s", err, wire)
	}
	if envelope.Schema != handlers.AdapterInvocationSchema ||
		envelope.Harness != runtime.ClaudeCode2_1_210Lifecycle().Harness() ||
		envelope.HarnessVersion != "2.1.210" ||
		envelope.HarnessContract != runtime.ClaudeCode2_1_210Lifecycle().ID() ||
		envelope.Operation != handlers.AdapterOperationSetInteractionMode {
		t.Fatalf("captured envelope metadata = %+v", envelope)
	}
	if string(envelope.Input) != rawInput {
		t.Fatalf("operation input changed before hidden decoding:\ngot  %s\nwant %s", envelope.Input, rawInput)
	}

	prefix, encoded, ok := strings.Cut(envelope.NativeInvocation, ".")
	if !ok || prefix != "claude-code" {
		t.Fatalf("native invocation %q has wrong harness prefix", envelope.NativeInvocation)
	}
	identityJSON, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode native invocation: %v", err)
	}
	var identity struct {
		Event      string     `json:"event"`
		Identities [][]string `json:"identities"`
	}
	if err := json.Unmarshal(identityJSON, &identity); err != nil {
		t.Fatalf("decode identity JSON: %v", err)
	}
	if identity.Event != "ElicitationResult" || len(identity.Identities) != 2 ||
		identity.Identities[0][0] != "session_id" || identity.Identities[0][1] != "session-alpha" ||
		identity.Identities[1][0] != "request_id" || identity.Identities[1][1] != "request-exact-001" {
		t.Fatalf("native correlation did not preserve exact declared identities: %+v", identity)
	}
}

func TestClaudeGenericEventCannotManufactureHumanDecision(t *testing.T) {
	t.Parallel()

	scriptPath := writeClaudeAdapter(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "must-not-exist")
	binary := writeFakePasture(t, dir, handlers.AdapterOperationRecordPlanUAT)
	stdout, stderr, exitCode := runPythonAdapter(
		t,
		scriptPath,
		nil,
		readNativeFixture(t, "claude", "permission_request.json"),
		map[string]string{
			adapterEventEnv:        "PermissionRequest",
			adapterOperationEnv:    string(handlers.AdapterOperationRecordPlanUAT),
			adapterInputEnv:        `{}`,
			adapterBinaryEnv:       binary,
			"PASTURE_TEST_CAPTURE": capture,
		},
	)
	if exitCode != 2 || !strings.Contains(stderr, "not allowed") {
		t.Fatalf("permission event human operation: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generic permission event reached hidden adapter; capture stat = %v", err)
	}
}

func TestClaudeStopLoopActiveSuppressesAdapter(t *testing.T) {
	t.Parallel()

	scriptPath := writeClaudeAdapter(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "must-not-exist")
	binary := writeFakePasture(t, dir, handlers.AdapterOperationShowInteractionMode)
	stdout, stderr, exitCode := runPythonAdapter(
		t,
		scriptPath,
		nil,
		readNativeFixture(t, "claude", "stop_active.json"),
		map[string]string{
			adapterEventEnv:        "Stop",
			adapterOperationEnv:    string(handlers.AdapterOperationShowInteractionMode),
			adapterInputEnv:        `{}`,
			adapterBinaryEnv:       binary,
			"PASTURE_TEST_CAPTURE": capture,
		},
	)
	if exitCode != 0 || stdout != "{}\n" || stderr != "" {
		t.Fatalf("active stop loop: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active stop loop reached hidden adapter; capture stat = %v", err)
	}
}

func TestClaudePolicyConfigChangeCannotBlock(t *testing.T) {
	t.Parallel()

	scriptPath := writeClaudeAdapter(t)
	stdout, stderr, exitCode := runPythonAdapter(
		t,
		scriptPath,
		nil,
		readNativeFixture(t, "claude", "config_change_policy.json"),
		map[string]string{
			adapterEventEnv:     "ConfigChange",
			adapterOperationEnv: string(handlers.AdapterOperationShowInteractionMode),
			adapterInputEnv:     `{"epoch":"epoch--00000000-0000-0000-0000-000000000001"}`,
			adapterBinaryEnv:    filepath.Join(t.TempDir(), "missing-pasture"),
		},
	)
	if exitCode != 0 || stdout != "{}\n" || !strings.Contains(stderr, "could not execute production binary") {
		t.Fatalf("policy config change failure blocked: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestLifecycleBindingTargetsExactlyOneNativeEvent(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runPythonAdapter(
		t,
		writeClaudeAdapter(t),
		nil,
		readNativeFixture(t, "claude", "permission_request.json"),
		map[string]string{
			adapterEventEnv:     "ConfigChange",
			adapterOperationEnv: string(handlers.AdapterOperationShowInteractionMode),
			adapterInputEnv:     `{"epoch":"epoch--00000000-0000-0000-0000-000000000001"}`,
			adapterBinaryEnv:    filepath.Join(t.TempDir(), "must-not-run"),
		},
	)
	if exitCode != 0 || stdout != "{}\n" || stderr != "" {
		t.Fatalf("nonmatching concurrent event consumed binding: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestCodexLifecycleAdapterStrictInputAndNoTranscriptRead(t *testing.T) {
	t.Parallel()

	metadata, err := lifecycleMetadata(runtime.Codex0_144_1Lifecycle(), "0.144.1", codexNativeFields)
	if err != nil {
		t.Fatalf("lifecycleMetadata: %v", err)
	}
	script, err := renderPythonLifecycleAdapter(metadata)
	if err != nil {
		t.Fatalf("renderPythonLifecycleAdapter: %v", err)
	}
	scriptPath := filepath.Join(t.TempDir(), "codex.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatalf("write generated adapter: %v", err)
	}
	native := readNativeFixture(t, "codex", "pre_tool_use.json")
	stdout, stderr, exitCode := runPythonAdapter(t, scriptPath, []string{"--event", "PreToolUse"}, native, nil)
	if exitCode != 0 || stdout != "{}\n" || stderr != "" {
		t.Fatalf("unreadable transcript sentinel was accessed: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	unknown := bytes.Replace(native, []byte("{"), []byte(`{"unknown":true,`), 1)
	stdout, stderr, exitCode = runPythonAdapter(t, scriptPath, []string{"--event", "PreToolUse"}, unknown, nil)
	if exitCode != 2 || !strings.Contains(stderr, "unknown fields") {
		t.Fatalf("unknown Codex field: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestGeneratedLifecycleAdaptersContainNoAuthorityOrStorageFields(t *testing.T) {
	t.Parallel()

	claude, err := lifecycleMetadata(runtime.ClaudeCode2_1_210Lifecycle(), "2.1.210", claudeNativeFields)
	if err != nil {
		t.Fatalf("Claude metadata: %v", err)
	}
	python, err := renderPythonLifecycleAdapter(claude)
	if err != nil {
		t.Fatalf("Python adapter: %v", err)
	}
	opencode, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatalf("OpenCode adapter: %v", err)
	}
	for name, artifact := range map[string]string{"python": python, "opencode": opencode} {
		for _, forbidden := range []string{"JournalID", "journalId", "expectedRevision", "EvidenceID", "evidenceIds", "reported-user-result", "transcript_path)", "open(transcript"} {
			if strings.Contains(artifact, forbidden) {
				t.Errorf("generated %s adapter contains forbidden authority/storage/transcript token %q", name, forbidden)
			}
		}
	}
}

func writeClaudeAdapter(t *testing.T) string {
	t.Helper()
	metadata, err := lifecycleMetadata(runtime.ClaudeCode2_1_210Lifecycle(), "2.1.210", claudeNativeFields)
	if err != nil {
		t.Fatalf("lifecycleMetadata: %v", err)
	}
	script, err := renderPythonLifecycleAdapter(metadata)
	if err != nil {
		t.Fatalf("renderPythonLifecycleAdapter: %v", err)
	}
	path := filepath.Join(t.TempDir(), "claude.py")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write generated adapter: %v", err)
	}
	return path
}

func writeFakePasture(t *testing.T, dir string, operation handlers.AdapterOperation) string {
	t.Helper()
	path := filepath.Join(dir, "fake pasture")
	script := `#!/usr/bin/env sh
set -eu
: "${PASTURE_TEST_CAPTURE:?missing capture path}"
cat > "${PASTURE_TEST_CAPTURE}"
printf '%s\n' '{"schema":"` + handlers.AdapterResultSchema + `","operation":"` + string(operation) + `","result":{"replayed":false}}'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pasture: %v", err)
	}
	return path
}

func readNativeFixture(t *testing.T, harness, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "native", harness, name))
	if err != nil {
		t.Fatalf("read native fixture: %v", err)
	}
	return data
}

func runPythonAdapter(t *testing.T, script string, args []string, stdin []byte, env map[string]string) (string, string, int) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is required to execute generated command adapters: %v", err)
	}
	cmd := exec.Command(python, append([]string{script}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("execute generated adapter: %v", err)
		}
		exitCode = exitError.ExitCode()
	}
	return stdout.String(), stderr.String(), exitCode
}

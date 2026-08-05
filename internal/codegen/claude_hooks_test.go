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
	for name, tc := range map[string]struct {
		mutate func([]activation.Entry) []activation.Entry
		want   string
	}{
		"missing":   {func(in []activation.Entry) []activation.Entry { return in[1:] }, "has no activation entry"},
		"duplicate": {func(in []activation.Entry) []activation.Entry { return append(in, in[0]) }, "duplicate activation entry"},
		"extra": {func(in []activation.Entry) []activation.Entry {
			extra, _ := activation.NewWithheld(999, activation.WithheldOutsideTargetSet)
			return append(in, extra)
		}, "remove non-manifest entries"},
		"invalid-state":  {func(in []activation.Entry) []activation.Entry { in[0].State = 0; return in }, "is invalid"},
		"invalid-reason": {func(in []activation.Entry) []activation.Entry { in[1].Reason = 0; return in }, "is invalid"},
		"invalid-proof":  {func(in []activation.Entry) []activation.Entry { in[0].CaptureProof = 0; return in }, "is invalid"},
		"enabled-with-reason": {func(in []activation.Entry) []activation.Entry {
			in[0].Reason = activation.WithheldMissingFixture
			return in
		}, "is invalid"},
		"enabled-with-invalid-production-proof": {func(in []activation.Entry) []activation.Entry { in[0].ProductionProof = 99; return in }, "is invalid"},
		"withheld-with-capture-proof": {func(in []activation.Entry) []activation.Entry {
			in[1].CaptureProof = activation.CaptureProofSessionStart
			return in
		}, "is invalid"},
		"withheld-with-production-proof": {func(in []activation.Entry) []activation.Entry {
			in[1].ProductionProof = activation.ProductionProofSessionStart
			return in
		}, "is invalid"},
	} {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := emitClaudeHooks(t.TempDir(), GenerateOptions{}, manifest, tc.mutate(append([]activation.Entry(nil), states...)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want actionable substring %q", err, tc.want)
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
	var report struct {
		Events []struct{ Event, State, Reason, CaptureProof, ProductionProof string }
	}
	if err := json.Unmarshal([]byte(support), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 30 {
		t.Fatalf("support entries=%d, want 30", len(report.Events))
	}
	expected := []struct{ event, state, reason string }{
		{"SessionStart", "enabled", ""}, {"Setup", "withheld", "outside-target-set"}, {"SessionEnd", "enabled", ""}, {"UserPromptSubmit", "withheld", "outside-target-set"}, {"UserPromptExpansion", "withheld", "outside-target-set"}, {"Stop", "withheld", "outside-target-set"}, {"StopFailure", "withheld", "outside-target-set"}, {"PreToolUse", "enabled", ""}, {"PermissionRequest", "withheld", "outside-target-set"}, {"PermissionDenied", "withheld", "outside-target-set"}, {"PostToolUse", "enabled", ""}, {"PostToolUseFailure", "enabled", ""}, {"PostToolBatch", "enabled", ""}, {"FileChanged", "withheld", "outside-target-set"}, {"CwdChanged", "withheld", "outside-target-set"}, {"ConfigChange", "withheld", "outside-target-set"}, {"InstructionsLoaded", "withheld", "outside-target-set"}, {"WorktreeCreate", "withheld", "outside-target-set"}, {"WorktreeRemove", "withheld", "outside-target-set"}, {"SubagentStart", "withheld", "outside-target-set"}, {"SubagentStop", "withheld", "outside-target-set"}, {"TeammateIdle", "withheld", "outside-target-set"}, {"TaskCreated", "withheld", "outside-target-set"}, {"TaskCompleted", "withheld", "outside-target-set"}, {"PreCompact", "enabled", ""}, {"PostCompact", "enabled", ""}, {"Notification", "withheld", "outside-target-set"}, {"MessageDisplay", "withheld", "outside-target-set"}, {"Elicitation", "withheld", "missing-request-correlation"}, {"ElicitationResult", "withheld", "missing-request-correlation"},
	}
	enabledProofs := map[string]struct{ capture, production string }{
		"SessionStart":       {"internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionStart"},
		"SessionEnd":         {"internal/lifecycle/ingress/claude/testdata/fixtures/session_end_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionEnd"},
		"PreToolUse":         {"internal/lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreToolUse"},
		"PostToolUse":        {"internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUse"},
		"PostToolUseFailure": {"internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_failure_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUseFailure"},
		"PostToolBatch":      {"internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_batch_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolBatch"},
		"PreCompact":         {"internal/lifecycle/ingress/claude/testdata/fixtures/pre_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreCompact"},
		"PostCompact":        {"internal/lifecycle/ingress/claude/testdata/fixtures/post_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)", "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostCompact"},
	}
	seen := make(map[string]struct{}, len(expected))
	for index, want := range expected {
		entry := report.Events[index]
		if _, duplicate := seen[entry.Event]; duplicate {
			t.Errorf("duplicate support event %q", entry.Event)
		}
		seen[entry.Event] = struct{}{}
		if entry.Event != want.event || entry.State != want.state || entry.Reason != want.reason {
			t.Errorf("support[%d]=%+v, want event=%q state=%q reason=%q", index, entry, want.event, want.state, want.reason)
		}
		if want.state == "enabled" {
			proof, present := enabledProofs[entry.Event]
			if !present || entry.CaptureProof != proof.capture || entry.ProductionProof != proof.production {
				t.Errorf("%s exact proofs changed: %+v", entry.Event, entry)
			}
		} else if entry.CaptureProof != "" || entry.ProductionProof != "" {
			t.Errorf("withheld event carries proofs: %+v", entry)
		}
		if strings.HasPrefix(entry.Reason, "reason-") {
			t.Errorf("numeric reason leaked: %+v", entry)
		}
	}
	if len(seen) != 30 {
		t.Fatalf("unique support events=%d, want 30", len(seen))
	}
	manifestEntries := manifest.Entries()
	for i, want := range expected {
		if manifestEntries[i].NativeName != want.event {
			t.Errorf("expected report order diverges from generated manifest at %d: %q != %q", i, want.event, manifestEntries[i].NativeName)
		}
	}
	var config claudeHooksConfig
	if err := json.Unmarshal([]byte(hooks), &config); err != nil {
		t.Fatal(err)
	}
	lifecycle := 0
	lifecycleByEvent := make(map[string]int, len(enabledProofs))
	for event, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if strings.Contains(hook.Command, "hook lifecycle") {
					lifecycle++
					if _, enabled := enabledProofs[event]; !enabled {
						t.Errorf("withheld lifecycle event emitted: %s", event)
					}
					lifecycleByEvent[event]++
					want := `${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event ` + event + ` --host-version "${CLAUDE_CODE_VERSION:-unknown}"`
					if hook.Command != want || hook.Type != "command" || hook.Timeout != 10 {
						t.Errorf("%s lifecycle command=%+v, want %q", event, hook, want)
					}
				}
			}
		}
	}
	if lifecycle != len(enabledProofs) {
		t.Fatalf("lifecycle hooks=%d, want %d enabled events", lifecycle, len(enabledProofs))
	}
	for event := range enabledProofs {
		if lifecycleByEvent[event] != 1 {
			t.Errorf("%s lifecycle hook count=%d, want exactly one", event, lifecycleByEvent[event])
		}
	}
	session := config.Hooks["SessionStart"]
	if len(session) != 1 || session[0].Matcher != "" || len(session[0].Hooks) != 2 {
		t.Fatalf("SessionStart hook group changed: %+v", session)
	}
	if session[0].Hooks[0].Command != "cat ${CLAUDE_PLUGIN_ROOT}/hooks/bd-prime.md 2>&1" {
		t.Errorf("SessionStart bd-prime command=%q", session[0].Hooks[0].Command)
	}
	wantLifecycle := `${PASTURE_BIN:-pasture} hook lifecycle --harness claude-code --event SessionStart --host-version "${CLAUDE_CODE_VERSION:-unknown}"`
	if session[0].Hooks[1].Command != wantLifecycle || session[0].Hooks[1].Type != "command" || session[0].Hooks[1].Timeout != 10 {
		t.Errorf("SessionStart lifecycle command=%+v, want %q", session[0].Hooks[1], wantLifecycle)
	}
	compact := config.Hooks["PreCompact"]
	if len(compact) != 1 || compact[0].Matcher != "" || len(compact[0].Hooks) != 2 ||
		compact[0].Hooks[0].Command != "cat ${CLAUDE_PLUGIN_ROOT}/hooks/bd-prime.md 2>&1" ||
		!strings.Contains(compact[0].Hooks[1].Command, "--event PreCompact") {
		t.Fatalf("PreCompact hook group changed: %+v", compact)
	}
	pre := config.Hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Matcher != "Bash" || len(pre[0].Hooks) != 1 ||
		pre[0].Hooks[0].Type != "command" ||
		pre[0].Hooks[0].Command != "bash ${CLAUDE_PLUGIN_ROOT}/hooks/scripts/git-discipline.sh" ||
		pre[0].Hooks[0].Timeout != 10 || pre[1].Matcher != "" || len(pre[1].Hooks) != 1 ||
		!strings.Contains(pre[1].Hooks[0].Command, "--event PreToolUse") {
		t.Fatalf("independent PreToolUse discipline missing: %+v", pre)
	}
	for _, name := range []string{"hooks.json", "pasture-activation.json"} {
		root, err := os.ReadFile(filepath.Join("..", "..", "hooks", name))
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := os.ReadFile(filepath.Join("..", "target", "claudecode", "assets", "pasture-hooks", "hooks", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(root, embedded) {
			t.Fatalf("root and embedded generated %s differ byte-for-byte", name)
		}
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
	files, err := emitClaudeHooks(t.TempDir(), GenerateOptions{}, manifest, states)
	if err != nil {
		t.Fatal(err)
	}
	var hooks string
	for _, file := range files {
		if strings.HasSuffix(file.Path, "hooks.json") {
			hooks = file.Content
		}
	}
	var config claudeHooksConfig
	if err := json.Unmarshal([]byte(hooks), &config); err != nil {
		t.Fatal(err)
	}
	if _, present := config.Hooks["SessionStart"]; present {
		t.Fatalf("withheld SessionStart emitted augmentation: %+v", config.Hooks["SessionStart"])
	}
	pre := config.Hooks["PreToolUse"]
	if len(pre) != 2 || !strings.Contains(pre[0].Hooks[0].Command, "git-discipline.sh") ||
		!strings.Contains(pre[1].Hooks[0].Command, "--event PreToolUse") {
		t.Fatalf("independent PreToolUse missing without SessionStart: %+v", pre)
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
			"generated Claude registration",
			claudeNativeFields,
		)
	})
	t.Run("Codex", func(t *testing.T) {
		assertIdentityFieldsInPayloadShape(
			t,
			runtime.Codex0_144_1Lifecycle(),
			runtime.CodexLifecycleEvents(),
			"Codex payload field table",
			codexNativeFields,
		)
	})
}

func TestClaudeLifecycleAdapterAllowsAuthentic2_1_222FieldShapes(t *testing.T) {
	t.Parallel()
	metadata, err := lifecycleMetadata(runtime.ClaudeCode2_1_210Lifecycle(), "2.1.210", claudeNativeFields)
	if err != nil {
		t.Fatalf("build Claude lifecycle adapter metadata: %v", err)
	}
	events := make(map[string]lifecycleEventMetadata, len(metadata.Events))
	for _, event := range metadata.Events {
		events[event.Name] = event
	}
	fixtureRoot := filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures")
	fixtures, err := filepath.Glob(filepath.Join(fixtureRoot, "*_2_1_222.json"))
	if err != nil {
		t.Fatalf("list authentic Claude fixtures: %v", err)
	}
	if len(fixtures) != 10 {
		t.Fatalf("authentic Claude fixture count = %d, want 10", len(fixtures))
	}
	for _, fixture := range fixtures {
		raw, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read authentic Claude fixture %q: %v", fixture, err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode authentic Claude fixture %q: %v", fixture, err)
		}
		var eventName string
		if err := json.Unmarshal(payload["hook_event_name"], &eventName); err != nil {
			t.Fatalf("decode hook_event_name in %q: %v", fixture, err)
		}
		event, present := events[eventName]
		if !present {
			t.Errorf("authentic Claude fixture %q names unknown adapter event %q", fixture, eventName)
			continue
		}
		for field := range payload {
			if !slices.Contains(event.AllowedFields, field) {
				t.Errorf("authentic Claude fixture %q field %q is absent from generated adapter metadata for %s", fixture, field, eventName)
			}
		}
	}
}

func TestClaudeLifecycleAdapterFieldsMatchGeneratedRegistration(t *testing.T) {
	t.Parallel()
	fieldNames := registration.ClaudeCode2_1_210NativeFieldNames()
	for _, event := range registration.ClaudeCode2_1_210().Entries() {
		expected := make([]string, 0, len(event.AllowedFields))
		for _, field := range event.AllowedFields {
			name, present := fieldNames[field]
			if !present || name == "" {
				t.Fatalf("generated Claude event %q field %d has no native name", event.NativeName, field)
			}
			expected = append(expected, name)
		}
		if actual := claudeNativeFields(event.NativeName); !slices.Equal(actual, expected) {
			t.Errorf("Claude adapter fields for %q = %v, want generated registration fields %v", event.NativeName, actual, expected)
		}
	}
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
					"event %q identity field %q is declared by the runtime lifecycle identity table but absent from %s",
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
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("sh is required to execute the fake pasture adapter: %v", err)
	}
	script := `#!` + shell + `
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

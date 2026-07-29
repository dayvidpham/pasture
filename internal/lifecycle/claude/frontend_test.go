package claude_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/lifecycle/claude"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// sessionStartPayload is a realistic Claude SessionStart payload. It carries
// the fields Claude actually sends — including transcript_path, which must be
// tolerated and then ignored.
func sessionStartPayload(t *testing.T, overrides map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"session_id":      "sess-01",
		"transcript_path": "/home/dev/.claude/projects/demo/sess-01.jsonl",
		"cwd":             "/home/dev/demo",
		"permission_mode": "default",
		"hook_event_name": "SessionStart",
		"source":          "startup",
		"model":           "claude-sonnet",
		"session_title":   "demo",
	}
	for key, value := range overrides {
		if value == nil {
			delete(payload, key)
			continue
		}
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	return encoded
}

func requireStructured(t *testing.T, err error) *pasterrors.StructuredError {
	t.Helper()
	require.Error(t, err)
	var se *pasterrors.StructuredError
	require.ErrorAs(t, err, &se, "frontend errors must be actionable")
	assert.NotEmpty(t, se.What)
	assert.NotEmpty(t, se.Why)
	assert.NotEmpty(t, se.Where)
	assert.NotEmpty(t, se.Impact)
	assert.NotEmpty(t, se.Fix)
	return se
}

func TestFrontendServesClaudeOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ir.HarnessClaudeCode, claude.New().Harness())
}

// TestZeroFrontendRefusesToParse covers the one way a caller can hold a
// frontend that resolves nothing: building the struct directly instead of
// calling the constructor. Without this branch the failure surfaces as a
// confusing "empty runtime contract" from the waist, or — worse, if the
// catalogue index is consulted first — as "this event is not pinned" for an
// event that plainly is.
func TestZeroFrontendRefusesToParse(t *testing.T) {
	t.Parallel()

	_, err := claude.Frontend{}.Parse(sessionStartPayload(t, nil), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.Fix, "claude.New()")
}

// goldenIR pins the IR one realistic native payload must produce. The expected
// values are written out literally rather than read back from the pinned
// contract: an expectation derived from the same table the code reads would
// agree with the code however wrong both were.
type goldenIR struct {
	name       string
	payload    string
	nativeName lifecycle.NativeEventName
	semantic   runtime.EventSemantic
	blocking   runtime.BlockingMode
	identities []lifecycle.SemanticIdentity
}

// TestParseProducesGoldenIR is the IR-level table: no host, no database, no
// clock. Between them the cases exercise every identity kind the Claude
// profile uses (session, request, tool-call, agent), every semantic it
// carries, and every blocking mode — so a frontend that resolved the wrong
// catalogue entry, or attached a value under the wrong kind, fails here rather
// than at the first real hook invocation.
func TestParseProducesGoldenIR(t *testing.T) {
	t.Parallel()

	cases := []goldenIR{
		{
			name:       "SessionStart is a non-blocking observation correlated by session",
			payload:    `{"session_id":"sess-01","transcript_path":"/home/dev/.claude/sess-01.jsonl","cwd":"/home/dev/demo","permission_mode":"default","hook_event_name":"SessionStart","source":"startup","model":"claude-sonnet","session_title":"demo"}`,
			nativeName: "SessionStart",
			semantic:   runtime.SemanticObservation,
			blocking:   runtime.NonBlocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
			},
		},
		{
			name:       "PostToolUse adds a tool-call correlation",
			payload:    `{"session_id":"sess-01","cwd":"/home/dev/demo","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_019","tool_input":{"command":"ls -la"},"tool_output":"total 0"}`,
			nativeName: "PostToolUse",
			semantic:   runtime.SemanticObservation,
			blocking:   runtime.NonBlocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
				{Kind: runtime.IdentityToolCall, Value: "toolu_019"},
			},
		},
		{
			name:       "PermissionRequest is a blocking gate correlated by request",
			payload:    `{"session_id":"sess-01","hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{"file_path":"/etc/hosts"},"request_id":"req_42"}`,
			nativeName: "PermissionRequest",
			semantic:   runtime.SemanticGateConsultation,
			blocking:   runtime.Blocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
				{Kind: runtime.IdentityRequest, Value: "req_42"},
			},
		},
		{
			name:       "SubagentStart adds an agent correlation",
			payload:    `{"session_id":"sess-01","hook_event_name":"SubagentStart","agent_id":"agent_7","agent_type":"general-purpose"}`,
			nativeName: "SubagentStart",
			semantic:   runtime.SemanticObservation,
			blocking:   runtime.NonBlocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
				{Kind: runtime.IdentityAgent, Value: "agent_7"},
			},
		},
		{
			name:       "ElicitationResult is an explicit human response",
			payload:    `{"session_id":"sess-01","hook_event_name":"ElicitationResult","request_id":"req_42","mcp_server_name":"docs","response":{"answer":"yes"}}`,
			nativeName: "ElicitationResult",
			semantic:   runtime.SemanticExplicitHumanResponse,
			blocking:   runtime.Blocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
				{Kind: runtime.IdentityRequest, Value: "req_42"},
			},
		},
		{
			name:       "ConfigChange is a conditionally blocking gate",
			payload:    `{"session_id":"sess-01","hook_event_name":"ConfigChange","config_source":"project_settings"}`,
			nativeName: "ConfigChange",
			semantic:   runtime.SemanticGateConsultation,
			blocking:   runtime.ConditionallyBlocking,
			identities: []lifecycle.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "sess-01"},
			},
		},
	}

	frontend := claude.New()
	contractID := runtime.ClaudeCode2_1_210Lifecycle().ID()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event, err := frontend.Parse([]byte(tc.payload), "")
			require.NoError(t, err)
			require.True(t, event.IsValid())

			origin := event.Origin()
			assert.Equal(t, tc.nativeName, origin.NativeEventName())
			assert.Equal(t, ir.HarnessClaudeCode, origin.Harness())
			assert.Equal(t, contractID, origin.Contract())
			assert.False(t, origin.PayloadDigest().IsZero(), "the digest must be computed, not left zero")

			semantics := event.Semantics()
			assert.Equal(t, tc.semantic, semantics.Semantic())
			assert.Equal(t, tc.blocking, semantics.Blocking())
			assert.Equal(t, tc.identities, semantics.Identities())
		})
	}
}

// TestParseExtractsOnlyDeclaredCorrelation is the guarantee that keeps the
// waist narrow at the source. The payload is full of content — a transcript
// path, a working directory, a model name, a title — and exactly one field
// crosses, because exactly one is declared.
//
// transcript_path is the one that matters: it is a path to the whole
// conversation. It is tolerated because Claude always sends it, and it is
// never read, never stored, and cannot become an identity.
func TestParseExtractsOnlyDeclaredCorrelation(t *testing.T) {
	t.Parallel()

	event, err := claude.New().Parse(sessionStartPayload(t, nil), "")
	require.NoError(t, err)

	identities := event.Semantics().Identities()
	require.Len(t, identities, 1, "SessionStart declares exactly one correlation field")
	assert.Equal(t, runtime.IdentitySession, identities[0].Kind)
	assert.Equal(t, "sess-01", identities[0].Value)

	for _, identity := range identities {
		assert.NotContains(t, identity.Value, ".jsonl", "a transcript path must never become an identity")
		assert.NotContains(t, identity.Value, "/home/dev/demo", "a working directory must never become an identity")
		assert.NotContains(t, identity.Value, "claude-sonnet", "a model name must never become an identity")
	}
}

// TestParseDigestsTheExactBytesReceived is the replay guarantee at its source.
//
// The hazard is one session doing the same thing twice: same event, same
// declared correlation, same waist semantics, different payload content. Only
// a digest over the raw bytes separates those, so each case here varies
// exactly ONE thing — a frontend that digested the extracted correlation, or a
// re-marshalled canonical form, collapses the pair and fails.
func TestParseDigestsTheExactBytesReceived(t *testing.T) {
	t.Parallel()

	frontend := claude.New()
	digestOf := func(t *testing.T, payload []byte) lifecycle.Digest {
		t.Helper()
		event, err := frontend.Parse(payload, "")
		require.NoError(t, err)
		return event.Origin().PayloadDigest()
	}

	t.Run("identical bytes digest identically", func(t *testing.T) {
		t.Parallel()
		payload := sessionStartPayload(t, nil)
		assert.Equal(t, digestOf(t, payload), digestOf(t, payload))
	})

	t.Run("content outside the declared correlation still changes the digest", func(t *testing.T) {
		t.Parallel()
		// Same session, same event, same identities: the waist values are
		// indistinguishable, so the digest is the only thing separating them.
		first := sessionStartPayload(t, map[string]any{"session_title": "first task"})
		second := sessionStartPayload(t, map[string]any{"session_title": "second task"})
		assert.NotEqual(t, digestOf(t, first), digestOf(t, second))
	})

	t.Run("member order changes the digest", func(t *testing.T) {
		t.Parallel()
		// Byte-for-byte is the stated contract, and it is what makes the
		// digest well-defined for payloads that never parse. A frontend that
		// re-marshalled the decoded members would normalise key order and
		// collapse these two.
		first := []byte(`{"hook_event_name":"SessionStart","session_id":"sess-01"}`)
		second := []byte(`{"session_id":"sess-01","hook_event_name":"SessionStart"}`)
		assert.NotEqual(t, digestOf(t, first), digestOf(t, second))
	})

	t.Run("the digest is over the payload as received", func(t *testing.T) {
		t.Parallel()
		payload := sessionStartPayload(t, nil)
		assert.Equal(t, lifecycle.NewDigest(payload), digestOf(t, payload))
	})
}

func TestParseAcceptsAgreeingRequestedEvent(t *testing.T) {
	t.Parallel()

	event, err := claude.New().Parse(sessionStartPayload(t, nil), "SessionStart")
	require.NoError(t, err)
	assert.Equal(t, lifecycle.NativeEventName("SessionStart"), event.Origin().NativeEventName())
}

// TestParseRejectsStaleHookRegistration covers the failure a person will
// actually hit: the hook is registered under one event and the payload reports
// another, because the registration was not updated.
func TestParseRejectsStaleHookRegistration(t *testing.T) {
	t.Parallel()

	_, err := claude.New().Parse(sessionStartPayload(t, nil), "PreToolUse")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "PreToolUse")
	assert.Contains(t, se.What, "SessionStart")
	assert.Contains(t, se.Fix, "SessionStart")
}

func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	payload := sessionStartPayload(t, map[string]any{"tool_name": "Bash", "surprise": 1})

	_, err := claude.New().Parse(payload, "")

	se := requireStructured(t, err)
	// Both are reported, sorted, so a version drift shows its full shape in
	// one run rather than one field per attempt.
	assert.Contains(t, se.What, "surprise")
	assert.Contains(t, se.What, "tool_name")
	assert.Less(t, strings.Index(se.What, "surprise"), strings.Index(se.What, "tool_name"))
	assert.Contains(t, se.Fix, "session_id")
}

// TestParseRejectsFieldsBelongingToAnotherEvent proves admissibility is per
// event, not per harness: tool_input is a perfectly good Claude field, just
// not on SessionStart. This is the property a single wide decode struct with
// DisallowUnknownFields could not express.
func TestParseRejectsFieldsBelongingToAnotherEvent(t *testing.T) {
	t.Parallel()

	frontend := claude.New()

	// The same field is admissible on the event that declares it...
	_, err := frontend.Parse([]byte(`{"hook_event_name":"PreToolUse","session_id":"sess-01","tool_use_id":"toolu_1","tool_input":{"command":"ls"}}`), "")
	require.NoError(t, err)

	// ...and refused on the one that does not.
	_, err = frontend.Parse(sessionStartPayload(t, map[string]any{"tool_input": map[string]any{"command": "ls"}}), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "tool_input")
	assert.Contains(t, se.What, "SessionStart")
}

// TestParseToleratesEveryFieldTheHostLegitimatelySends is the false-rejection
// guard, and it is the failure mode with the worst blast radius: a field the
// host really sends but the allowed set omits rejects EVERY invocation of that
// event in production, while looking like a strictness win in review.
//
// Each case is a full payload as Claude sends it for that event, including the
// extras beyond the declared correlation.
func TestParseToleratesEveryFieldTheHostLegitimatelySends(t *testing.T) {
	t.Parallel()

	frontend := claude.New()
	cases := map[string]string{
		"SessionStart carries source, model and title": `{"session_id":"s","transcript_path":"/t.jsonl","cwd":"/w","permission_mode":"default","hook_event_name":"SessionStart","source":"resume","model":"claude-opus","session_title":"demo","effort":"high","agent_id":"a","agent_type":"main"}`,
		"Stop carries stop_hook_active":                `{"session_id":"s","hook_event_name":"Stop","stop_hook_active":true,"cwd":"/w"}`,
		"SubagentStop carries its own transcript":      `{"session_id":"s","hook_event_name":"SubagentStop","agent_id":"a","agent_type":"general-purpose","agent_transcript_path":"/a.jsonl","stop_hook_active":false}`,
		"InstructionsLoaded carries memory detail":     `{"session_id":"s","hook_event_name":"InstructionsLoaded","file_path":"/AGENTS.md","memory_type":"project","load_reason":"startup","globs":["**/*.go"],"trigger_file_path":"/main.go","parent_file_path":"/CLAUDE.md"}`,
		"PostToolUseFailure carries the error":         `{"session_id":"s","hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_use_id":"t","tool_input":{"command":"false"},"error":"exit status 1"}`,
		"Notification carries title and type":          `{"session_id":"s","hook_event_name":"Notification","message":"waiting","notification_type":"idle","title":"Claude"}`,
		"Elicitation carries the MCP request":          `{"session_id":"s","hook_event_name":"Elicitation","request_id":"r","mcp_server_name":"docs","fields":[{"name":"answer"}]}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := frontend.Parse([]byte(payload), "")
			require.NoError(t, err, "a payload the host legitimately sends must not be rejected")
		})
	}
}

func TestParseRejectsDuplicateFields(t *testing.T) {
	t.Parallel()

	// json.Marshal cannot produce a duplicate member, so the payload is
	// written literally — which is also how a broken host would send one.
	// DisallowUnknownFields would not catch this: encoding/json silently
	// keeps the last occurrence.
	payload := []byte(`{"hook_event_name":"SessionStart","session_id":"a","session_id":"b"}`)

	_, err := claude.New().Parse(payload, "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "strict JSON object")
	assert.Contains(t, se.Why, "repeats a field")
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"hook_event_name":"SessionStart","session_id":"a"}{"hook_event_name":"SessionEnd"}`)

	_, err := claude.New().Parse(payload, "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "strict JSON object")
}

func TestParseRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload []byte
		wantIn  string
	}{
		{"empty", nil, "empty payload"},
		{"not an object", []byte(`["SessionStart"]`), "strict JSON object"},
		{"json null", []byte(`null`), "strict JSON object"},
		{"bare string", []byte(`"SessionStart"`), "strict JSON object"},
		{"invalid utf-8", []byte("{\"hook_event_name\":\"Session\xff\xfeStart\"}"), "not valid UTF-8"},
		{"missing event name", []byte(`{"session_id":"a"}`), "strict JSON object"},
		{"empty event name", []byte(`{"hook_event_name":""}`), "empty"},
		{"non-string event name", []byte(`{"hook_event_name":7}`), "not a JSON string"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := claude.New().Parse(tc.payload, "")
			se := requireStructured(t, err)
			assert.Contains(t, se.What, tc.wantIn)
		})
	}
}

func TestParseRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	// A valid object padded past the bound with a legal field's content.
	filler := strings.Repeat("x", lifecycle.MaxNativePayloadBytes)
	payload := sessionStartPayload(t, map[string]any{"session_title": filler})
	require.Greater(t, len(payload), lifecycle.MaxNativePayloadBytes)

	_, err := claude.New().Parse(payload, "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "over the")
}

func TestParseRejectsUnpinnedEvent(t *testing.T) {
	t.Parallel()

	_, err := claude.New().Parse([]byte(`{"hook_event_name":"SomethingNew","session_id":"a"}`), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "SomethingNew")
	// The remedy is a version check, not a menu. Enumerating thirty event
	// names is text an operator reads past to reach the actual fix, and
	// "pick another event" is never available to them — the host chose it.
	assert.Contains(t, se.Fix, "lifecycle_profiles.go")
	assert.NotContains(t, se.Fix, "PostToolUseFailure", "the catalogue must not be dumped into the remedy")
}

func TestParseRejectsMissingRequiredCorrelation(t *testing.T) {
	t.Parallel()

	_, err := claude.New().Parse(sessionStartPayload(t, map[string]any{"session_id": nil}), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "session_id")
	assert.Contains(t, se.What, "missing")
}

func TestParseRejectsNonStringCorrelation(t *testing.T) {
	t.Parallel()

	_, err := claude.New().Parse(sessionStartPayload(t, map[string]any{"session_id": 42}), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "session_id")
	assert.Contains(t, se.What, "not a JSON string")
}

func TestParseRejectsEmptyCorrelation(t *testing.T) {
	t.Parallel()

	_, err := claude.New().Parse(sessionStartPayload(t, map[string]any{"session_id": ""}), "")

	se := requireStructured(t, err)
	assert.Contains(t, se.What, "session_id")
	assert.Contains(t, se.What, "empty value")
}

// TestParseOmitsAbsentOptionalCorrelation guards the other half of the
// absent-field rule. The Claude profile happens to declare every
// correlation field required, so this asserts the shape of the rule rather
// than a case that exists today: an optional declared field that is absent
// must parse, and its identity must simply not appear.
func TestParseOmitsAbsentOptionalCorrelation(t *testing.T) {
	t.Parallel()

	contract := runtime.ClaudeCode2_1_210Lifecycle()
	mapping, err := contract.Mapping(runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	for _, field := range mapping.Identities() {
		require.True(t, field.Required(),
			"this profile declares no optional Claude correlation; if that changes, extend this test with the optional case")
	}

	// Every declared field is required today, so the observable rule is that
	// an absent one is refused — by the verifier, which owns the declared set.
	_, err = claude.New().Parse(sessionStartPayload(t, map[string]any{"session_id": nil}), "")
	se := requireStructured(t, err)
	assert.Contains(t, se.What, "required correlation field")
}

// TestParseCoversTheWholePinnedCatalogue proves every event the pinned
// contract declares can be parsed — including the gate and human-response
// events the lowering pass currently refuses to act on. Deciding what an event
// MEANS, and whether Pasture will act on it, is the lowering's job; refusing to
// PARSE it here would move that decision into the frontend and hide the very
// refusal that must be loud.
func TestParseCoversTheWholePinnedCatalogue(t *testing.T) {
	t.Parallel()

	contract := runtime.ClaudeCode2_1_210Lifecycle()
	frontend := claude.New()
	require.NotEmpty(t, contract.Events())

	for _, native := range contract.Events() {
		mapping, err := contract.Mapping(native)
		require.NoError(t, err)

		t.Run(mapping.NativeName(), func(t *testing.T) {
			t.Parallel()
			payload := map[string]any{"hook_event_name": mapping.NativeName()}
			for _, field := range mapping.Identities() {
				payload[field.NativeName()] = "native-" + field.NativeName()
			}
			encoded, err := json.Marshal(payload)
			require.NoError(t, err)

			event, err := frontend.Parse(encoded, lifecycle.NativeEventName(mapping.NativeName()))
			require.NoError(t, err, "every pinned event must parse")

			assert.Equal(t, lifecycle.NativeEventName(mapping.NativeName()), event.Origin().NativeEventName())
			assert.Equal(t, mapping.Semantic(), event.Semantics().Semantic())
			assert.Equal(t, mapping.Blocking(), event.Semantics().Blocking())
			assert.Len(t, event.Semantics().Identities(), len(mapping.Identities()))
		})
	}
}

// TestParseIgnoresPayloadContentWhenDecidingSemantics is the regression guard
// for the specific drift being retired. The previous adapter downgraded
// ConfigChange's failure mode when config_source looked like a policy setting,
// and suppressed Stop when stop_hook_active was set — payload content deciding
// semantics inside a frontend, re-implemented per harness.
//
// Here the same payload content is present and nothing moves, on either side
// of the waist: the target detail is asserted too, because that is what the
// retired adapter actually altered.
func TestParseIgnoresPayloadContentWhenDecidingSemantics(t *testing.T) {
	t.Parallel()

	frontend := claude.New()
	contract := runtime.ClaudeCode2_1_210Lifecycle()

	t.Run("config_source does not move ConfigChange", func(t *testing.T) {
		t.Parallel()
		mapping, err := contract.Mapping(runtime.ClaudeEventConfigChange)
		require.NoError(t, err)

		for _, source := range []string{"policy_settings", "project_settings", "user_settings"} {
			event, err := frontend.Parse(
				[]byte(`{"hook_event_name":"ConfigChange","session_id":"a","config_source":"`+source+`"}`), "")
			require.NoError(t, err)

			assert.Equal(t, runtime.ConditionallyBlocking, event.Semantics().Blocking())
			assert.Equal(t, mapping.Failure(), lifecycle.BackendView(event).TargetBehaviour().Failure(),
				"payload content must not change the failure mode")
		}
	})

	t.Run("stop_hook_active does not move Stop", func(t *testing.T) {
		t.Parallel()
		mapping, err := contract.Mapping(runtime.ClaudeEventStop)
		require.NoError(t, err)

		for _, active := range []string{"true", "false"} {
			event, err := frontend.Parse(
				[]byte(`{"hook_event_name":"Stop","session_id":"a","stop_hook_active":`+active+`}`), "")
			require.NoError(t, err)

			assert.Equal(t, mapping.Semantic(), event.Semantics().Semantic(),
				"stop-loop state must not change what the event means")
			assert.Equal(t, runtime.StopLoopConsultWhenInactive,
				lifecycle.BackendView(event).TargetBehaviour().StopLoop(),
				"stop-loop policy is reported to the middle-end, not applied in the frontend")
		}
	})
}

// TestParseIsPureUnderConcurrency exercises the two claims the doc comment
// makes about state: one Frontend serves many occurrences concurrently, and
// the same bytes always yield the same IR. The frontend holds a catalogue
// index, so "read-only after construction" is a property worth proving under
// -race rather than asserting in a comment.
func TestParseIsPureUnderConcurrency(t *testing.T) {
	t.Parallel()

	frontend := claude.New()
	payloads := [][]byte{
		sessionStartPayload(t, nil),
		[]byte(`{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"t","tool_input":{},"tool_output":""}`),
		[]byte(`{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"Write","tool_input":{},"request_id":"r"}`),
	}

	want := make([]lifecycle.Event, len(payloads))
	for index, payload := range payloads {
		event, err := frontend.Parse(payload, "")
		require.NoError(t, err)
		want[index] = event
	}

	var group sync.WaitGroup
	for round := 0; round < 8; round++ {
		for index, payload := range payloads {
			group.Add(1)
			go func() {
				defer group.Done()
				event, err := frontend.Parse(payload, "")
				assert.NoError(t, err)
				assert.Equal(t, want[index].Semantics(), event.Semantics())
				assert.Equal(t, want[index].Origin(), event.Origin())
			}()
		}
	}
	group.Wait()
}

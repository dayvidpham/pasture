package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// These are deliberate malformed-input probes, never authored host evidence.
// Valid controls use the cleared native fixtures. Mutations at the parser or
// dispatch that lose a cause, change its field/kind, or bypass validation must
// fail the console AND durable assertions here.
func TestLifecycleTypedCaptureDiagnostics(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	check := func(t *testing.T, harness, event, version string, raw []byte, disposition model.CaptureDisposition, reason, advice string) {
		t.Helper()
		database := filepath.Join(t.TempDir(), "pasture.db")
		initializeLifecycleTestDatabase(t, database)
		run := runLifecycleHookOn(t, binary, database, harness, event, version, raw)
		require.Equal(t, 0, run.ExitCode, "refusal is a fail-open fault, not a policy deny")
		if harness == "claude-code" {
			require.Empty(t, run.Stdout)
		} else {
			require.JSONEq(t, `{"decision":"proceed"}`, run.Stdout)
		}
		if disposition == model.CaptureValid {
			require.Empty(t, run.Stderr, "native control must not carry a refusal")
		} else {
			require.Contains(t, run.Stderr, "WAS NOT EVALUATED: "+reason+".", "exact cause must reach the built CLI")
			require.Contains(t, run.Stderr, advice, "cause-specific repair must reach the built CLI")
			require.NotContains(t, run.Stderr, "private-host-value")
			require.NotContains(t, run.Stderr, "either an identity")
			require.NotContains(t, run.Stderr, "absent, unreadable, or")
			require.NotContains(t, run.Stderr, "the payload is not a JSON object")
			require.Equal(t, 1, strings.Count(run.Stderr, "WAS NOT EVALUATED"))
			faults := readFaultRecords(t, filepath.Dir(database))
			require.Len(t, faults, 1, "one invocation leaves one fault line")
		}
		assertDiagnosticCaptureRecord(t, database, raw, disposition)
	}
	claude := func(t *testing.T, raw []byte, disposition model.CaptureDisposition, reason, advice string) {
		check(t, "claude-code", "SessionStart", "2.1.261", raw, disposition, reason, advice)
	}
	openCode := func(t *testing.T, raw string, disposition model.CaptureDisposition, reason, advice string) {
		check(t, "opencode", "tool.execute.before", "1.18.29", []byte(raw), disposition, reason, advice)
	}
	fixture := claudeFixture(t, "session_start_2_1_261.json")
	changed := func(t *testing.T, field string, value []byte) []byte {
		t.Helper()
		var members map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fixture, &members))
		if value == nil {
			delete(members, field)
		} else {
			members[field] = value
		}
		raw, err := json.Marshal(members)
		require.NoError(t, err)
		return raw
	}
	t.Run("claude-native-control", func(t *testing.T) {
		claude(t, fixture, model.CaptureValid, "", "")
	})
	t.Run("opencode-native-control", func(t *testing.T) {
		openCode(t, string(openCodeToolExecuteBeforeWire(t)), model.CaptureValid, "", "")
	})
	t.Run("claude-undeclared", func(t *testing.T) {
		claude(t, changed(t, "added_member", []byte(`"private-host-value"`)), model.CaptureUnsupportedSchema,
			`member "added_member" is not declared by this event's registration`,
			"Compare that added member with the matching host contract and update the pinned registration before admitting it; do not rename or remove valid identities.")
	})
	t.Run("claude-missing", func(t *testing.T) {
		claude(t, changed(t, "session_id", nil), model.CaptureUnsupportedSchema,
			`required member "session_id" is absent`,
			`Supply "session_id" as a JSON string at that path; check for a dropped or renamed member in the host contract.`)
	})
	t.Run("claude-wrong-kind", func(t *testing.T) {
		claude(t, changed(t, "session_id", []byte(`42`)), model.CaptureUnsupportedSchema,
			`member "session_id" has the wrong JSON kind; required kind is string`,
			`Send "session_id" as a JSON string at that path; correct the wrapper or serializer, not the top-level object.`)
	})
	t.Run("claude-empty", func(t *testing.T) {
		claude(t, changed(t, "session_id", []byte(`""`)), model.CaptureUnsupportedSchema,
			`identity "session_id" is an empty string`,
			`Supply the nonempty host identity at "session_id"; do not synthesize a replacement identity.`)
	})
	t.Run("claude-overlength", func(t *testing.T) {
		value, err := json.Marshal(strings.Repeat("x", 513))
		require.NoError(t, err)
		claude(t, changed(t, "session_id", value), model.CaptureUnsupportedSchema,
			`identity "session_id" exceeds the 512-byte identity bound`,
			`Check why "session_id" exceeds 512 bytes and align the host contract with the identity bound; do not truncate an identity.`)
	})
	t.Run("claude-unusable", func(t *testing.T) {
		claude(t, changed(t, "session_id", []byte(`"private-host-value\n"`)), model.CaptureUnsupportedSchema,
			`identity "session_id" contains padding or a control character`,
			`Supply "session_id" without leading or trailing whitespace, NUL or control characters; fix serialization without changing the host identity.`)
	})
	t.Run("claude-event-mismatch", func(t *testing.T) {
		claude(t, changed(t, "hook_event_name", []byte(`"private-host-value"`)), model.CaptureEventMismatch,
			`member "hook_event_name" does not name the invoked event`,
			"Send the payload for the command's event, or invoke the command for the event that payload describes; do not reinterpret it as another event.")
	})
	t.Run("opencode-malformed", func(t *testing.T) {
		openCode(t, `{`, model.CaptureMalformed,
			"the payload is not one complete, well-formed JSON value",
			"Send one complete JSON object with no trailing value or bytes; check the hook's stdin serialization.")
	})
	t.Run("opencode-nonobject", func(t *testing.T) {
		openCode(t, `[]`, model.CaptureMalformed,
			"the payload is valid JSON but its top level is not an object",
			"Send a JSON object at the top level, not an array, scalar or null.")
	})
	t.Run("opencode-retired-wrapper", func(t *testing.T) {
		openCode(t, `{"harness":"opencode","event":"private-host-value","payload":{}}`, model.CaptureMalformed,
			`member "event" has the wrong JSON kind; required kind is object`,
			`Send "event" as a JSON object at that path; correct the wrapper or serializer, not the top-level object.`)
	})
	t.Run("opencode-wrong-leaf-kind", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":42,"callID":"private-host-value"}}`, model.CaptureMalformed,
			`member "input.sessionID" has the wrong JSON kind; required kind is string`,
			`Send "input.sessionID" as a JSON string at that path; correct the wrapper or serializer, not the top-level object.`)
	})
	t.Run("opencode-missing-container", func(t *testing.T) {
		openCode(t, `{}`, model.CaptureUnsupportedSchema,
			`required member "input" is absent`,
			`Supply "input" as a JSON object at that path; check for a dropped or renamed member in the host contract.`)
	})
	t.Run("opencode-missing-leaf", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":"private-host-value"}}`, model.CaptureUnsupportedSchema,
			`required member "input.callID" is absent`,
			`Supply "input.callID" as a JSON string at that path; check for a dropped or renamed member in the host contract.`)
	})
	t.Run("opencode-null-leaf", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":null,"callID":"private-host-value"}}`, model.CaptureUnsupportedSchema,
			`member "input.sessionID" has the wrong JSON kind; required kind is string`,
			`Send "input.sessionID" as a JSON string at that path; correct the wrapper or serializer, not the top-level object.`)
	})
	t.Run("opencode-empty", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":"","callID":"private-host-value"}}`, model.CaptureUnsupportedSchema,
			`identity "input.sessionID" is an empty string`,
			`Supply the nonempty host identity at "input.sessionID"; do not synthesize a replacement identity.`)
	})
	t.Run("opencode-overlength", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":"`+strings.Repeat("x", 513)+`","callID":"call"}}`, model.CaptureUnsupportedSchema,
			`identity "input.sessionID" exceeds the 512-byte identity bound`,
			`Check why "input.sessionID" exceeds 512 bytes and align the host contract with the identity bound; do not truncate an identity.`)
	})
	t.Run("opencode-unusable", func(t *testing.T) {
		openCode(t, `{"input":{"sessionID":"private-host-value\n","callID":"call"}}`, model.CaptureUnsupportedSchema,
			`identity "input.sessionID" contains padding or a control character`,
			`Supply "input.sessionID" without leading or trailing whitespace, NUL or control characters; fix serialization without changing the host identity.`)
	})
}

func assertDiagnosticCaptureRecord(t *testing.T, database string, raw []byte, disposition model.CaptureDisposition) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(database)
	require.NoError(t, err)
	defer tracker.Close()
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1, "exactly one durable occurrence per invocation")
	payload := decodeOccurrencePayload(t, occurrences[0].Payload)
	require.Equal(t, disposition, payload.Capture, "durable disposition must match the cause rendered to the operator")
	require.Equal(t, digest.FromBytes(raw).String(), payload.Body)
	if disposition != model.CaptureValid {
		require.Empty(t, payload.Bindings)
		require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
		require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
	}
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
	require.NoError(t, err)
	require.Equal(t, raw, body, "diagnosis must not rewrite the raw host payload")
}

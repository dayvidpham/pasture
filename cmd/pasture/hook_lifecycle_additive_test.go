package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/stretchr/testify/require"
)

// In-memory edits are compatibility probes, not authored host fixtures. The
// same binary and reviewed event must tolerate added evidence without turning
// that evidence into a declared binding or new interpreted content.
func TestClaudeAdditivePayloadPreservesClosedProjection(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	control := claudeFixture(t, "session_start_2_1_261.json")
	expanded := withTopLevelMember(t, control, "future_host_metadata", `{"identity":"private-added-value","content":[1,true,null]}`)
	type projection struct {
		bindings    []lifecycleBindingPayload
		interpreted []byte
	}
	drive := func(raw []byte) projection {
		database := filepath.Join(t.TempDir(), "pasture.db")
		initializeLifecycleTestDatabase(t, database)
		run := runLifecycleHookOn(t, binary, database, "claude-code", "SessionStart", "2.1.300", raw)
		require.Equal(t, 0, run.ExitCode)
		require.Empty(t, run.Stdout)
		require.Empty(t, run.Stderr, "an unrelated added member must not refuse a compatible host payload")
		require.Empty(t, readFaultRecords(t, filepath.Dir(database)))
		assertDiagnosticCaptureRecord(t, database, raw, model.CaptureValid)
		tracker, err := tasks.OpenTaskTracker(database)
		require.NoError(t, err)
		defer tracker.Close()
		occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
		require.Len(t, occurrences, 1)
		payload := decodeOccurrencePayload(t, occurrences[0].Payload)
		require.Equal(t, "2.1.300", payload.Envelope.HostVersion, "a newer observation is metadata, not a release equality gate")
		interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
		require.Len(t, interpreted, 1)
		require.NotContains(t, string(interpreted[0].Payload), "private-added-value")
		require.NotContains(t, string(interpreted[0].Payload), "future_host_metadata")
		return projection{bindings: payload.Bindings, interpreted: interpreted[0].Payload}
	}
	before := drive(control)
	after := drive(expanded)
	require.Equal(t, before.bindings, after.bindings, "raw extras must not become bindings")
	require.True(t, bytes.Equal(before.interpreted, after.interpreted), "raw extras must not change the closed interpreted payload")
}

func TestClaudeAddedMembersDoNotRescueRequiredContractDefects(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	fixture := claudeFixture(t, "session_start_2_1_261.json")
	check := func(t *testing.T, field string, replacement json.RawMessage, disposition model.CaptureDisposition, reason string) {
		t.Helper()
		var members map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fixture, &members))
		members["future_host_metadata"] = json.RawMessage(`{"session_id":"not-a-replacement"}`)
		if replacement == nil {
			delete(members, field)
		} else {
			members[field] = replacement
		}
		raw, err := json.Marshal(members)
		require.NoError(t, err)
		database := filepath.Join(t.TempDir(), "pasture.db")
		initializeLifecycleTestDatabase(t, database)
		run := runLifecycleHookOn(t, binary, database, "claude-code", "SessionStart", "2.1.300", raw)
		require.Equal(t, 0, run.ExitCode)
		require.Empty(t, run.Stdout)
		require.Contains(t, run.Stderr, reason)
		require.NotContains(t, run.Stderr, "not-a-replacement")
		assertDiagnosticCaptureRecord(t, database, raw, disposition)
	}
	t.Run("missing", func(t *testing.T) {
		check(t, "session_id", nil, model.CaptureUnsupportedSchema, `required member "session_id" is absent`)
	})
	t.Run("wrong-kind", func(t *testing.T) {
		check(t, "session_id", json.RawMessage(`42`), model.CaptureUnsupportedSchema, `member "session_id" has the wrong JSON kind; required kind is string`)
	})
	t.Run("wrong-event", func(t *testing.T) {
		check(t, "hook_event_name", json.RawMessage(`"SessionEnd"`), model.CaptureEventMismatch, `member "hook_event_name" does not name the invoked event`)
	})
}

func TestClaudeAddedMembersCannotActivateWithheldEvent(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	raw := claudeFixture(t, "file_changed_2_1_263.json")
	expanded := withTopLevelMember(t, raw, "future_host_metadata", `{"enable":true}`)
	database := filepath.Join(t.TempDir(), "pasture.db")
	run := runLifecycleHookOn(t, binary, database, "claude-code", "FileChanged", "2.1.300", expanded)
	require.Equal(t, 0, run.ExitCode)
	require.Empty(t, run.Stdout)
	require.Contains(t, run.Stderr, `event "FileChanged" is withheld`)
	_, err := os.Stat(database)
	require.ErrorIs(t, err, os.ErrNotExist, "added evidence must not bypass the event activation gate")
}

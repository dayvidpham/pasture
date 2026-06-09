package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	stderrors "errors"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Test helpers ────────────────────────────────────────────────────────────

// sampleQueryStateResult builds a QueryStateResult suitable for formatter tests.
func sampleQueryStateResult() protocol.QueryStateResult {
	lastErr := "constraint check failed"
	ts := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	return protocol.QueryStateResult{
		CurrentPhase: protocol.PhaseWorkerSlices,
		CurrentRole:  protocol.RoleWorker,
		TransitionHistory: []protocol.TransitionRecord{
			{
				FromPhase:    protocol.PhaseImplPlan,
				ToPhase:      protocol.PhaseWorkerSlices,
				Timestamp:    ts,
				TriggeredBy:  "supervisor",
				ConditionMet: "plan ratified",
				Success:      true,
			},
		},
		Votes: map[protocol.ReviewAxis]protocol.VoteType{
			protocol.AxisCorrectness: protocol.VoteAccept,
			protocol.AxisTestQuality: protocol.VoteRevise,
		},
		LastError:            &lastErr,
		AvailableTransitions: []protocol.PhaseId{protocol.PhaseCodeReview},
		ActiveSessionCount:   3,
	}
}

// ─── FormatEpochState ────────────────────────────────────────────────────────

func TestFormatEpochState_JSON(t *testing.T) {
	result := sampleQueryStateResult()
	got, err := formatters.FormatEpochState(result, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatEpochState JSON: unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("FormatEpochState JSON: output is not valid JSON: %v\nOutput:\n%s", err, got)
	}

	// Verify required camelCase top-level keys.
	keys := []string{"currentPhase", "currentRole", "transitionHistory", "votes", "availableTransitions", "activeSessionCount"}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("FormatEpochState JSON: missing key %q", k)
		}
	}

	// currentPhase and currentRole values.
	if got := m["currentPhase"].(string); got != "worker-slices" {
		t.Errorf("currentPhase: want %q, got %q", "worker-slices", got)
	}
	if got := m["currentRole"].(string); got != "worker" {
		t.Errorf("currentRole: want %q, got %q", "worker", got)
	}

	// lastError must be present when non-nil.
	if _, ok := m["lastError"]; !ok {
		t.Error("FormatEpochState JSON: expected lastError key when LastError is set")
	}

	// transitionHistory must have one entry.
	hist, ok := m["transitionHistory"].([]any)
	if !ok || len(hist) != 1 {
		t.Errorf("FormatEpochState JSON: want 1 transitionHistory entry, got %v", m["transitionHistory"])
	}
	if len(hist) == 1 {
		entry := hist[0].(map[string]any)
		if entry["fromPhase"] != "impl-plan" {
			t.Errorf("transitionHistory[0].fromPhase: want %q, got %q", "impl-plan", entry["fromPhase"])
		}
		if entry["toPhase"] != "worker-slices" {
			t.Errorf("transitionHistory[0].toPhase: want %q, got %q", "worker-slices", entry["toPhase"])
		}
		if entry["success"] != true {
			t.Errorf("transitionHistory[0].success: want true, got %v", entry["success"])
		}
	}

	// votes must have string keys/values.
	votes, ok := m["votes"].(map[string]any)
	if !ok {
		t.Fatalf("FormatEpochState JSON: votes is not a map, got %T", m["votes"])
	}
	if votes["correctness"] != "ACCEPT" {
		t.Errorf("votes[correctness]: want %q, got %v", "ACCEPT", votes["correctness"])
	}
	if votes["test_quality"] != "REVISE" {
		t.Errorf("votes[test_quality]: want %q, got %v", "REVISE", votes["test_quality"])
	}

	// availableTransitions.
	avail, ok := m["availableTransitions"].([]any)
	if !ok || len(avail) != 1 {
		t.Errorf("availableTransitions: want [code-review], got %v", m["availableTransitions"])
	}
	if len(avail) == 1 && avail[0] != "code-review" {
		t.Errorf("availableTransitions[0]: want %q, got %v", "code-review", avail[0])
	}
}

func TestFormatEpochState_Text(t *testing.T) {
	result := sampleQueryStateResult()
	got, err := formatters.FormatEpochState(result, types.OutputText)
	if err != nil {
		t.Fatalf("FormatEpochState Text: unexpected error: %v", err)
	}

	checks := []struct {
		label    string
		contains string
	}{
		{"Phase line", "Phase: worker-slices"},
		{"Role line", "Role:  worker"},
		{"Votes header", "Votes:"},
		{"Correctness vote", "correctness"},
		{"ACCEPT vote", "ACCEPT"},
		{"test_quality vote", "test_quality"},
		{"REVISE vote", "REVISE"},
		{"LastError line", "Last Error: constraint check failed"},
		{"Available transitions header", "Available Transitions:"},
		{"Available transition code-review", "-> code-review"},
		{"Transition count", "Transitions: 1"},
		{"Active sessions", "Active Sessions: 3"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.contains) {
			t.Errorf("FormatEpochState Text [%s]: output does not contain %q\nGot:\n%s", c.label, c.contains, got)
		}
	}
}

func TestFormatEpochState_Text_NoVotes(t *testing.T) {
	result := sampleQueryStateResult()
	result.Votes = nil
	got, err := formatters.FormatEpochState(result, types.OutputText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Votes: (none)") {
		t.Errorf("expected 'Votes: (none)' for empty votes, got:\n%s", got)
	}
}

func TestFormatEpochState_Text_NoLastError(t *testing.T) {
	result := sampleQueryStateResult()
	result.LastError = nil
	got, err := formatters.FormatEpochState(result, types.OutputText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "Last Error:") {
		t.Errorf("expected no 'Last Error:' line when LastError is nil, got:\n%s", got)
	}
}

func TestFormatEpochState_JSON_LastErrorOmittedWhenNil(t *testing.T) {
	result := sampleQueryStateResult()
	result.LastError = nil
	got, err := formatters.FormatEpochState(result, types.OutputJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "lastError") {
		t.Errorf("lastError must be omitted from JSON when nil, got:\n%s", got)
	}
}

func TestFormatEpochState_InvalidFormat(t *testing.T) {
	result := sampleQueryStateResult()
	_, err := formatters.FormatEpochState(result, types.OutputFormat("xml"))
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	var se *errors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != errors.CategoryValidation {
		t.Errorf("expected CategoryValidation, got %q", se.Category)
	}
}

// ─── FormatStartResult ───────────────────────────────────────────────────────

func TestFormatStartResult_JSON(t *testing.T) {
	got, err := formatters.FormatStartResult("epoch-123", "run-abc", types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatStartResult JSON: unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("FormatStartResult JSON: output is not valid JSON: %v\nOutput:\n%s", err, got)
	}
	if m["workflowId"] != "epoch-123" {
		t.Errorf("workflowId: want %q, got %v", "epoch-123", m["workflowId"])
	}
	if m["runId"] != "run-abc" {
		t.Errorf("runId: want %q, got %v", "run-abc", m["runId"])
	}
}

func TestFormatStartResult_Text(t *testing.T) {
	got, err := formatters.FormatStartResult("epoch-123", "run-abc", types.OutputText)
	if err != nil {
		t.Fatalf("FormatStartResult Text: unexpected error: %v", err)
	}
	want := "Started epoch: workflow_id=epoch-123, run_id=run-abc"
	if got != want {
		t.Errorf("FormatStartResult Text:\n  want: %q\n  got:  %q", want, got)
	}
}

func TestFormatStartResult_InvalidFormat(t *testing.T) {
	_, err := formatters.FormatStartResult("id", "run", types.OutputFormat("yaml"))
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	var se *errors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != errors.CategoryValidation {
		t.Errorf("expected CategoryValidation, got %q", se.Category)
	}
}

// ─── FormatSignalResult ──────────────────────────────────────────────────────

func TestFormatSignalResult_JSON_Success(t *testing.T) {
	got, err := formatters.FormatSignalResult(true, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatSignalResult JSON (success): unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("FormatSignalResult JSON: output is not valid JSON: %v", err)
	}
	if m["success"] != true {
		t.Errorf("success: want true, got %v", m["success"])
	}
}

func TestFormatSignalResult_JSON_Failure(t *testing.T) {
	got, err := formatters.FormatSignalResult(false, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatSignalResult JSON (failure): unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("FormatSignalResult JSON: output is not valid JSON: %v", err)
	}
	if m["success"] != false {
		t.Errorf("success: want false, got %v", m["success"])
	}
}

func TestFormatSignalResult_Text_Success(t *testing.T) {
	got, err := formatters.FormatSignalResult(true, types.OutputText)
	if err != nil {
		t.Fatalf("FormatSignalResult Text (success): unexpected error: %v", err)
	}
	want := "Signal delivered successfully"
	if got != want {
		t.Errorf("FormatSignalResult Text (success):\n  want: %q\n  got:  %q", want, got)
	}
}

func TestFormatSignalResult_Text_Failure(t *testing.T) {
	got, err := formatters.FormatSignalResult(false, types.OutputText)
	if err != nil {
		t.Fatalf("FormatSignalResult Text (failure): unexpected error: %v", err)
	}
	want := "Signal delivery failed"
	if got != want {
		t.Errorf("FormatSignalResult Text (failure):\n  want: %q\n  got:  %q", want, got)
	}
}

func TestFormatSignalResult_InvalidFormat(t *testing.T) {
	_, err := formatters.FormatSignalResult(true, types.OutputFormat("csv"))
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	var se *errors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
}

// ─── FormatError ─────────────────────────────────────────────────────────────

func TestFormatError_StructuredError_JSON(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryConnection,
		What:     "cannot reach Temporal",
		Why:      "network unreachable",
		Impact:   "epoch workflows cannot start",
		Fix:      "start the Temporal server and retry",
	}
	got := formatters.FormatError(se, types.OutputJSON)
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("FormatError JSON: output is not valid JSON: %v\nOutput:\n%s", err, got)
	}
	if m["category"] != "connection error" {
		t.Errorf("category: want %q, got %v", "connection error", m["category"])
	}
	if m["what"] != "cannot reach Temporal" {
		t.Errorf("what: want %q, got %v", "cannot reach Temporal", m["what"])
	}
	if m["fix"] != "start the Temporal server and retry" {
		t.Errorf("fix: want %q, got %v", "start the Temporal server and retry", m["fix"])
	}
}

func TestFormatError_StructuredError_Text(t *testing.T) {
	se := &errors.StructuredError{
		Category: errors.CategoryWorkflow,
		What:     "The workflow ran past its timeout.",
		Why:      "An activity didn't finish within the configured deadline.",
		Impact:   "The slice can't complete until the activity is rerun.",
		Fix: "1. Raise the activity timeout in your pastured config:\n" +
			"     $EDITOR ~/.config/pasture/pastured.toml",
	}
	got := formatters.FormatError(se, types.OutputText)
	// Plain-language Stringer: the top "Error:" line is now derived from
	// Category (so it elaborates rather than duplicates Problem). The
	// specific What value appears in the Problem: line. The category enum
	// literal must NOT appear in user-visible output.
	checks := []string{
		"Error: A workflow step failed.",
		"Problem:    The workflow ran past its timeout.",
		"Reason:",
		"Impact:",
		"How to fix:",
		"An activity didn't finish within the configured deadline.",
		"The slice can't complete until the activity is rerun.",
		"$EDITOR ~/.config/pasture/pastured.toml",
	}
	for _, s := range checks {
		if !strings.Contains(got, s) {
			t.Errorf("FormatError Text: output does not contain %q\nGot:\n%s", s, got)
		}
	}
	if strings.Contains(got, "workflow error") {
		t.Errorf("FormatError Text: output leaked category literal:\n%s", got)
	}
}

func TestFormatError_PlainError_ReturnsFallback(t *testing.T) {
	plain := stderrors.New("something went wrong")
	got := formatters.FormatError(plain, types.OutputJSON)
	if got != "something went wrong" {
		t.Errorf("FormatError plain: want %q, got %q", "something went wrong", got)
	}
}

func TestFormatError_Nil_ReturnsEmpty(t *testing.T) {
	got := formatters.FormatError(nil, types.OutputJSON)
	if got != "" {
		t.Errorf("FormatError nil: want empty string, got %q", got)
	}
}

// ─── FormatHookRecord ─────────────────────────────────────────────────────────

// TestFormatHookRecord_JSON_AllMetadataPresent asserts the JSON output includes
// all nine camelCase keys when every metadata field (including repo+remotes) is
// supplied.
func TestFormatHookRecord_JSON_AllMetadataPresent(t *testing.T) {
	remotes := map[string]string{"origin": "git@github.com:dayvidpham/pasture.git"}
	got, err := formatters.FormatHookRecord(
		"git-commit", "abc123", 42,
		"fix: handle nil config", "Jane Dev <jane@example.com>", "main", "2026-06-07T12:00:00Z",
		"dayvidpham/pasture", remotes,
		types.OutputJSON,
	)
	if err != nil {
		t.Fatalf("FormatHookRecord JSON: unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("FormatHookRecord JSON: output is not valid JSON: %v\n%s", err, got)
	}
	// 9 keys: eventType, sha, eventId, message, author, branch, timestamp, repo, remotes
	if len(decoded) != 9 {
		t.Errorf("JSON has %d keys, want exactly 9; got %v", len(decoded), decoded)
	}
	if decoded["eventType"] != "git-commit" {
		t.Errorf("eventType = %v, want %q", decoded["eventType"], "git-commit")
	}
	if decoded["sha"] != "abc123" {
		t.Errorf("sha = %v, want %q", decoded["sha"], "abc123")
	}
	if id, _ := decoded["eventId"].(float64); id != 42 {
		t.Errorf("eventId = %v, want 42", decoded["eventId"])
	}
	if decoded["message"] != "fix: handle nil config" {
		t.Errorf("message = %v, want %q", decoded["message"], "fix: handle nil config")
	}
	if decoded["author"] != "Jane Dev <jane@example.com>" {
		t.Errorf("author = %v, want %q", decoded["author"], "Jane Dev <jane@example.com>")
	}
	if decoded["branch"] != "main" {
		t.Errorf("branch = %v, want %q", decoded["branch"], "main")
	}
	if decoded["timestamp"] != "2026-06-07T12:00:00Z" {
		t.Errorf("timestamp = %v, want %q", decoded["timestamp"], "2026-06-07T12:00:00Z")
	}
	if decoded["repo"] != "dayvidpham/pasture" {
		t.Errorf("repo = %v, want %q", decoded["repo"], "dayvidpham/pasture")
	}
	// remotes decodes as map[string]any
	gotRemotes, _ := decoded["remotes"].(map[string]any)
	if gotRemotes["origin"] != "git@github.com:dayvidpham/pasture.git" {
		t.Errorf("remotes[origin] = %v, want %q", gotRemotes["origin"], "git@github.com:dayvidpham/pasture.git")
	}
}

// TestFormatHookRecord_JSON_AbsentFieldOmitted asserts that metadata fields
// absent from the recording (empty string / nil map) are omitted from the JSON
// output via omitempty. Covers branch (empty), repo (empty), and remotes (nil).
func TestFormatHookRecord_JSON_AbsentFieldOmitted(t *testing.T) {
	// branch is empty (e.g. detached HEAD); repo and remotes also absent.
	got, err := formatters.FormatHookRecord(
		"git-commit", "abc123", 42,
		"fix: detached HEAD commit", "Jane Dev <jane@example.com>", "", "2026-06-07T12:00:00Z",
		"", nil,
		types.OutputJSON,
	)
	if err != nil {
		t.Fatalf("FormatHookRecord JSON: unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("FormatHookRecord JSON: output is not valid JSON: %v\n%s", err, got)
	}
	// branch, repo, remotes absent → 6 keys (not 9)
	if len(decoded) != 6 {
		t.Errorf("JSON has %d keys, want 6 (branch/repo/remotes absent via omitempty); got %v", len(decoded), decoded)
	}
	for _, absent := range []string{"branch", "repo", "remotes"} {
		if _, present := decoded[absent]; present {
			t.Errorf("%q key should be absent when empty (omitempty); got: %v", absent, decoded)
		}
	}
	// The other keys must still be present.
	for _, key := range []string{"eventType", "sha", "eventId", "message", "author", "timestamp"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("expected key %q to be present; got: %v", key, decoded)
		}
	}
}

func TestFormatHookRecord_Text_MatchesContract(t *testing.T) {
	got, err := formatters.FormatHookRecord("git-commit", "abc123", 42, "msg", "auth", "main", "ts", "owner/repo", nil, types.OutputText)
	if err != nil {
		t.Fatalf("FormatHookRecord Text: unexpected error: %v", err)
	}
	want := "recorded git-commit event for sha abc123 (event #42)"
	if got != want {
		t.Errorf("FormatHookRecord Text:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatHookRecord_UnknownFormat_ActionableError(t *testing.T) {
	_, err := formatters.FormatHookRecord("git-commit", "abc123", 42, "", "", "", "", "", nil, types.OutputFormat("xml"))
	if err == nil {
		t.Fatal("FormatHookRecord with unknown format: want error, got nil")
	}
	var se *errors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != errors.CategoryValidation {
		t.Errorf("category = %v, want CategoryValidation", se.Category)
	}
}

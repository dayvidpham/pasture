package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── PhaseId.IsValid ──────────────────────────────────────────────────────────

func TestPhaseId_IsValid(t *testing.T) {
	t.Parallel()

	validCases := []protocol.PhaseId{
		protocol.PhaseRequest,
		protocol.PhaseElicit,
		protocol.PhasePropose,
		protocol.PhaseReview,
		protocol.PhasePlanReview,
		protocol.PhaseRatify,
		protocol.PhaseHandoff,
		protocol.PhaseImplPlan,
		protocol.PhaseWorkerSlices,
		protocol.PhaseCodeReview,
		protocol.PhaseImplUAT,
		protocol.PhaseLanding,
		protocol.PhaseComplete,
	}
	for _, p := range validCases {
		p := p // capture range var
		t.Run("valid_"+string(p), func(t *testing.T) {
			t.Parallel()
			if !p.IsValid() {
				t.Errorf("expected PhaseId(%q).IsValid() == true", p)
			}
		})
	}

	invalidCases := []protocol.PhaseId{
		"",
		"p1",
		"p13",
		"unknown",
		"COMPLETE",
		"p 1",
	}
	for _, p := range invalidCases {
		p := p
		t.Run("invalid_"+string(p), func(t *testing.T) {
			t.Parallel()
			if p.IsValid() {
				t.Errorf("expected PhaseId(%q).IsValid() == false", p)
			}
		})
	}
}

func TestAllPhaseIds_Completeness(t *testing.T) {
	t.Parallel()

	// AllPhaseIds must contain exactly 13 entries (12 pipeline phases + complete).
	if got := len(protocol.AllPhaseIds); got != 13 {
		t.Errorf("len(AllPhaseIds) = %d, want 13", got)
	}

	// Every entry in AllPhaseIds must be valid.
	for _, p := range protocol.AllPhaseIds {
		if !p.IsValid() {
			t.Errorf("AllPhaseIds contains invalid PhaseId %q", p)
		}
	}
}

// ─── PhaseId.String ───────────────────────────────────────────────────────────

func TestPhaseId_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		phase protocol.PhaseId
		want  string
	}{
		{protocol.PhaseRequest, "request"},
		{protocol.PhaseComplete, "complete"},
		{protocol.PhaseCodeReview, "code-review"},
		{protocol.PhaseWorkerSlices, "worker-slices"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.phase.String(); got != tc.want {
				t.Errorf("PhaseId(%q).String() = %q, want %q", tc.phase, got, tc.want)
			}
		})
	}
}

// ─── ParsePhaseId ─────────────────────────────────────────────────────────────

func TestParsePhaseId(t *testing.T) {
	t.Parallel()

	type tc struct {
		input string
		want  protocol.PhaseId
	}

	validCases := []tc{
		// Name-only format (canonical)
		{"request", protocol.PhaseRequest},
		{"elicit", protocol.PhaseElicit},
		{"propose", protocol.PhasePropose},
		{"review", protocol.PhaseReview},
		{"plan-review", protocol.PhasePlanReview},
		{"ratify", protocol.PhaseRatify},
		{"handoff", protocol.PhaseHandoff},
		{"impl-plan", protocol.PhaseImplPlan},
		{"worker-slices", protocol.PhaseWorkerSlices},
		{"code-review", protocol.PhaseCodeReview},
		{"impl-uat", protocol.PhaseImplUAT},
		{"landing", protocol.PhaseLanding},
		{"complete", protocol.PhaseComplete},
		// Alias name formats
		{"planreview", protocol.PhasePlanReview},
		{"plan_review", protocol.PhasePlanReview},
		{"implplan", protocol.PhaseImplPlan},
		{"impl_plan", protocol.PhaseImplPlan},
		{"workerslices", protocol.PhaseWorkerSlices},
		{"worker_slices", protocol.PhaseWorkerSlices},
		{"slice", protocol.PhaseWorkerSlices},
		{"slices", protocol.PhaseWorkerSlices},
		{"codereview", protocol.PhaseCodeReview},
		{"code_review", protocol.PhaseCodeReview},
		{"impluat", protocol.PhaseImplUAT},
		{"impl_uat", protocol.PhaseImplUAT},
		{"uat", protocol.PhaseImplUAT},
		// pX format resolved via DefaultPipeline
		{"p1", protocol.PhaseRequest},
		{"p2", protocol.PhaseElicit},
		{"p3", protocol.PhasePropose},
		{"p4", protocol.PhaseReview},
		{"p5", protocol.PhasePlanReview},
		{"p6", protocol.PhaseRatify},
		{"p7", protocol.PhaseHandoff},
		{"p8", protocol.PhaseImplPlan},
		{"p9", protocol.PhaseWorkerSlices},
		{"p10", protocol.PhaseCodeReview},
		{"p11", protocol.PhaseImplUAT},
		{"p12", protocol.PhaseLanding},
		// pX-name legacy format
		{"p1-request", protocol.PhaseRequest},
		{"p2-elicit", protocol.PhaseElicit},
		{"p9-worker-slices", protocol.PhaseWorkerSlices},
		{"p10-code-review", protocol.PhaseCodeReview},
		// pX_name legacy format
		{"p1_request", protocol.PhaseRequest},
		{"p4_review", protocol.PhaseReview},
		// Number only
		{"1", protocol.PhaseRequest},
		{"2", protocol.PhaseElicit},
		{"3", protocol.PhasePropose},
		{"10", protocol.PhaseCodeReview},
		{"11", protocol.PhaseImplUAT},
		{"12", protocol.PhaseLanding},
		// Case-insensitive
		{"REQUEST", protocol.PhaseRequest},
		{"COMPLETE", protocol.PhaseComplete},
		{"P1", protocol.PhaseRequest},
		{"P10", protocol.PhaseCodeReview},
		// Whitespace trimming
		{"  p1  ", protocol.PhaseRequest},
		{"  request  ", protocol.PhaseRequest},
	}

	for _, tc := range validCases {
		tc := tc
		t.Run("valid_"+tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := protocol.ParsePhaseId(tc.input)
			if err != nil {
				t.Errorf("ParsePhaseId(%q) unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("ParsePhaseId(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	errorCases := []string{
		"",
		"p0",
		"p13",
		"unknown",
		"P1_Unknown",
		"garbage",
		"13",
		"0",
	}
	for _, input := range errorCases {
		input := input
		t.Run("error_"+input, func(t *testing.T) {
			t.Parallel()
			got, err := protocol.ParsePhaseId(input)
			if err == nil {
				t.Errorf("ParsePhaseId(%q) expected error, got %q", input, got)
			}
		})
	}
}

// ─── Pipeline ────────────────────────────────────────────────────────────────

func TestDefaultPipeline_Length(t *testing.T) {
	t.Parallel()
	if got := len(protocol.DefaultPipeline); got != 12 {
		t.Errorf("DefaultPipeline length = %d, want 12", got)
	}
}

func TestDefaultPipeline_PhaseComplete_NotIncluded(t *testing.T) {
	t.Parallel()
	if protocol.DefaultPipeline.Contains(protocol.PhaseComplete) {
		t.Error("DefaultPipeline should NOT contain PhaseComplete (terminal state)")
	}
}

func TestPipeline_PhaseNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		phase protocol.PhaseId
		want  int
	}{
		{protocol.PhaseRequest, 1},
		{protocol.PhaseElicit, 2},
		{protocol.PhasePropose, 3},
		{protocol.PhaseReview, 4},
		{protocol.PhasePlanReview, 5},
		{protocol.PhaseRatify, 6},
		{protocol.PhaseHandoff, 7},
		{protocol.PhaseImplPlan, 8},
		{protocol.PhaseWorkerSlices, 9},
		{protocol.PhaseCodeReview, 10},
		{protocol.PhaseImplUAT, 11},
		{protocol.PhaseLanding, 12},
		{protocol.PhaseComplete, -1}, // terminal, not in pipeline
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.phase), func(t *testing.T) {
			t.Parallel()
			got := protocol.DefaultPipeline.PhaseNumber(tc.phase)
			if got != tc.want {
				t.Errorf("DefaultPipeline.PhaseNumber(%q) = %d, want %d", tc.phase, got, tc.want)
			}
		})
	}
}

func TestPipeline_PhaseAt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		number int
		want   protocol.PhaseId
		ok     bool
	}{
		{1, protocol.PhaseRequest, true},
		{5, protocol.PhasePlanReview, true},
		{9, protocol.PhaseWorkerSlices, true},
		{10, protocol.PhaseCodeReview, true},
		{12, protocol.PhaseLanding, true},
		{0, "", false},
		{13, "", false},
		{-1, "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got, ok := protocol.DefaultPipeline.PhaseAt(tc.number)
			if ok != tc.ok {
				t.Errorf("DefaultPipeline.PhaseAt(%d) ok = %v, want %v", tc.number, ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("DefaultPipeline.PhaseAt(%d) = %q, want %q", tc.number, got, tc.want)
			}
		})
	}
}

func TestPipeline_Contains(t *testing.T) {
	t.Parallel()
	if !protocol.DefaultPipeline.Contains(protocol.PhaseRequest) {
		t.Error("Contains(PhaseRequest) = false, want true")
	}
	if !protocol.DefaultPipeline.Contains(protocol.PhaseLanding) {
		t.Error("Contains(PhaseLanding) = false, want true")
	}
	if protocol.DefaultPipeline.Contains(protocol.PhaseComplete) {
		t.Error("Contains(PhaseComplete) = true, want false (terminal state)")
	}
	if protocol.DefaultPipeline.Contains("") {
		t.Error("Contains(\"\") = true, want false")
	}
}

func TestPipeline_Next(t *testing.T) {
	t.Parallel()
	cases := []struct {
		current protocol.PhaseId
		want    protocol.PhaseId
	}{
		{protocol.PhaseRequest, protocol.PhaseElicit},
		{protocol.PhaseElicit, protocol.PhasePropose},
		{protocol.PhaseWorkerSlices, protocol.PhaseCodeReview},
		{protocol.PhaseLanding, protocol.PhaseComplete},  // last in pipeline → Complete
		{protocol.PhaseComplete, protocol.PhaseComplete}, // not in pipeline → Complete
		{"unknown", protocol.PhaseComplete},              // not in pipeline → Complete
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.current), func(t *testing.T) {
			t.Parallel()
			got := protocol.DefaultPipeline.Next(tc.current)
			if got != tc.want {
				t.Errorf("DefaultPipeline.Next(%q) = %q, want %q", tc.current, got, tc.want)
			}
		})
	}
}

func TestPipeline_Index(t *testing.T) {
	t.Parallel()
	if got := protocol.DefaultPipeline.Index(protocol.PhaseRequest); got != 0 {
		t.Errorf("Index(PhaseRequest) = %d, want 0", got)
	}
	if got := protocol.DefaultPipeline.Index(protocol.PhaseLanding); got != 11 {
		t.Errorf("Index(PhaseLanding) = %d, want 11", got)
	}
	if got := protocol.DefaultPipeline.Index(protocol.PhaseComplete); got != -1 {
		t.Errorf("Index(PhaseComplete) = %d, want -1", got)
	}
}

// ─── EventType.IsValid ────────────────────────────────────────────────────────

func TestEventType_IsValid(t *testing.T) {
	t.Parallel()

	validCases := []protocol.EventType{
		protocol.EventPhaseTransition,
		protocol.EventPhaseAdvance,
		protocol.EventVoteRecorded,
		protocol.EventConstraintChecked,
		protocol.EventSliceStarted,
		protocol.EventSliceCompleted,
		protocol.EventSessionRegistered,
		protocol.EventReviewCycleStarted,
	}
	for _, e := range validCases {
		e := e
		t.Run("valid_"+string(e), func(t *testing.T) {
			t.Parallel()
			if !e.IsValid() {
				t.Errorf("expected EventType(%q).IsValid() == true", e)
			}
		})
	}

	invalidCases := []protocol.EventType{
		"",
		"unknown",
		"phaseTransition",  // camelCase not valid (EventType values use PascalCase)
		"phase-transition", // hyphen not valid
	}
	for _, e := range invalidCases {
		e := e
		t.Run("invalid_"+string(e), func(t *testing.T) {
			t.Parallel()
			if e.IsValid() {
				t.Errorf("expected EventType(%q).IsValid() == false", e)
			}
		})
	}
}

func TestAllEventTypes_Completeness(t *testing.T) {
	t.Parallel()

	if got := len(protocol.AllEventTypes); got != 8 {
		t.Errorf("len(AllEventTypes) = %d, want 8", got)
	}
	for _, e := range protocol.AllEventTypes {
		if !e.IsValid() {
			t.Errorf("AllEventTypes contains invalid EventType %q", e)
		}
	}
}

// ─── AuditEvent JSON round-trip ───────────────────────────────────────────────

func TestAuditEvent_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	event := protocol.AuditEvent{
		EpochID:   "epoch-abc-123",
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "worker",
		EventType: protocol.EventSliceCompleted,
		Payload: map[string]any{
			"sliceId": "slice-2",
			"success": true,
		},
		Timestamp: ts,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(AuditEvent) error: %v", err)
	}

	var decoded protocol.AuditEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(AuditEvent) error: %v", err)
	}

	if decoded.EpochID != event.EpochID {
		t.Errorf("EpochID round-trip mismatch: got %q, want %q", decoded.EpochID, event.EpochID)
	}
	if decoded.Phase != event.Phase {
		t.Errorf("Phase round-trip mismatch: got %q, want %q", decoded.Phase, event.Phase)
	}
	if decoded.Role != event.Role {
		t.Errorf("Role round-trip mismatch: got %q, want %q", decoded.Role, event.Role)
	}
	if decoded.EventType != event.EventType {
		t.Errorf("EventType round-trip mismatch: got %q, want %q", decoded.EventType, event.EventType)
	}
	if !decoded.Timestamp.Equal(event.Timestamp) {
		t.Errorf("Timestamp round-trip mismatch: got %v, want %v", decoded.Timestamp, event.Timestamp)
	}
}

func TestAuditEvent_JSONKeys(t *testing.T) {
	t.Parallel()

	event := protocol.AuditEvent{
		EpochID:   "epoch-1",
		Phase:     protocol.PhaseRequest,
		Role:      "epoch",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal to map error: %v", err)
	}

	requiredKeys := []string{"epochId", "phase", "role", "eventType", "payload", "timestamp"}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected JSON key %q in marshaled AuditEvent, got keys: %v", key, mapKeys(raw))
		}
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

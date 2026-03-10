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
		protocol.P1_Request,
		protocol.P2_Elicit,
		protocol.P3_Propose,
		protocol.P4_Review,
		protocol.P5_Uat,
		protocol.P6_Ratify,
		protocol.P7_Handoff,
		protocol.P8_ImplPlan,
		protocol.P9_Slice,
		protocol.P10_CodeReview,
		protocol.P11_ImplUat,
		protocol.P12_Landing,
		protocol.Complete,
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
		"P1",
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

	// AllPhaseIds must contain exactly 13 entries (p1..p12 + complete).
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
		{protocol.P1_Request, "p1"},
		{protocol.Complete, "complete"},
		{protocol.P10_CodeReview, "p10"},
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
		// Wire format
		{"p1", protocol.P1_Request},
		{"p2", protocol.P2_Elicit},
		{"p3", protocol.P3_Propose},
		{"p4", protocol.P4_Review},
		{"p5", protocol.P5_Uat},
		{"p6", protocol.P6_Ratify},
		{"p7", protocol.P7_Handoff},
		{"p8", protocol.P8_ImplPlan},
		{"p9", protocol.P9_Slice},
		{"p10", protocol.P10_CodeReview},
		{"p11", protocol.P11_ImplUat},
		{"p12", protocol.P12_Landing},
		{"complete", protocol.Complete},
		// Number only
		{"1", protocol.P1_Request},
		{"2", protocol.P2_Elicit},
		{"3", protocol.P3_Propose},
		{"10", protocol.P10_CodeReview},
		{"11", protocol.P11_ImplUat},
		{"12", protocol.P12_Landing},
		// Name only
		{"request", protocol.P1_Request},
		{"elicit", protocol.P2_Elicit},
		{"propose", protocol.P3_Propose},
		{"review", protocol.P4_Review},
		{"uat", protocol.P5_Uat},
		{"ratify", protocol.P6_Ratify},
		{"handoff", protocol.P7_Handoff},
		{"implplan", protocol.P8_ImplPlan},
		{"slice", protocol.P9_Slice},
		{"codereview", protocol.P10_CodeReview},
		{"impluat", protocol.P11_ImplUat},
		{"landing", protocol.P12_Landing},
		// Case-insensitive wire format
		{"P1", protocol.P1_Request},
		{"P10", protocol.P10_CodeReview},
		{"COMPLETE", protocol.Complete},
		// Full underscore format
		{"p1_request", protocol.P1_Request},
		{"p2_elicit", protocol.P2_Elicit},
		{"p10_codereview", protocol.P10_CodeReview},
		// PascalCase-style (lowered = same as underscore)
		{"P1_Request", protocol.P1_Request},
		{"P4_Review", protocol.P4_Review},
		// Whitespace trimming
		{"  p1  ", protocol.P1_Request},
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
		"phase_transition", // old snake_case not valid
		"phase-transition",  // hyphen not valid
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
		Phase:     protocol.P9_Slice,
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
		Phase:     protocol.P1_Request,
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

package protocol_test

import (
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TestSignalTopic_WireValues pins the on-the-wire topic strings. These names are
// the durable contract between a sender (CLI handler) and the workflow receive
// loop; a change here silently breaks delivery, so they are asserted explicitly.
func TestSignalTopic_WireValues(t *testing.T) {
	cases := map[protocol.SignalTopic]string{
		protocol.SignalAdvancePhase:    "advance_phase",
		protocol.SignalSubmitVote:      "submit_vote",
		protocol.SignalSliceProgress:   "slice_progress",
		protocol.SignalRegisterSession: "register_session",
		protocol.SignalStartSlice:      "start_slice",
		protocol.SignalCompleteSlice:   "complete_slice",
	}
	for topic, want := range cases {
		if got := topic.String(); got != want {
			t.Errorf("SignalTopic.String() = %q, want %q", got, want)
		}
		if !topic.IsValid() {
			t.Errorf("SignalTopic %q reported invalid", topic)
		}
	}
}

func TestSignalTopic_AllSignalTopicsComplete(t *testing.T) {
	if len(protocol.AllSignalTopics) != 6 {
		t.Fatalf("AllSignalTopics has %d entries, want 6", len(protocol.AllSignalTopics))
	}
	seen := map[protocol.SignalTopic]bool{}
	for _, topic := range protocol.AllSignalTopics {
		if !topic.IsValid() {
			t.Errorf("AllSignalTopics contains invalid topic %q", topic)
		}
		if seen[topic] {
			t.Errorf("AllSignalTopics contains duplicate topic %q", topic)
		}
		seen[topic] = true
	}
}

func TestSignalTopic_IsValidRejectsUnknown(t *testing.T) {
	if protocol.SignalTopic("not_a_topic").IsValid() {
		t.Error("unknown SignalTopic reported valid")
	}
	if protocol.SignalTopic("").IsValid() {
		t.Error("empty SignalTopic reported valid")
	}
}

// TestQueryName_WireValues pins the query name strings used by the SQL-over-
// projection read path and the folded CLI query verbs.
func TestQueryName_WireValues(t *testing.T) {
	cases := map[protocol.QueryName]string{
		protocol.QueryCurrentState:         "current_state",
		protocol.QueryAvailableTransitions: "available_transitions",
		protocol.QueryFullState:            "full_state",
		protocol.QuerySliceProgressState:   "slice_progress_state",
		protocol.QueryActiveSessions:       "active_sessions",
	}
	for q, want := range cases {
		if got := q.String(); got != want {
			t.Errorf("QueryName.String() = %q, want %q", got, want)
		}
		if !q.IsValid() {
			t.Errorf("QueryName %q reported invalid", q)
		}
	}
}

func TestQueryName_AllQueryNamesComplete(t *testing.T) {
	if len(protocol.AllQueryNames) != 5 {
		t.Fatalf("AllQueryNames has %d entries, want 5", len(protocol.AllQueryNames))
	}
	for _, q := range protocol.AllQueryNames {
		if !q.IsValid() {
			t.Errorf("AllQueryNames contains invalid query %q", q)
		}
	}
}

func TestParseQueryName(t *testing.T) {
	if q, ok := protocol.ParseQueryName("full_state"); !ok || q != protocol.QueryFullState {
		t.Errorf("ParseQueryName(full_state) = (%q, %v), want (full_state, true)", q, ok)
	}
	if _, ok := protocol.ParseQueryName("bogus"); ok {
		t.Error("ParseQueryName(bogus) reported ok")
	}
}

package receipt

import (
	stderrors "errors"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
)

// lineageWarrant builds a valid lineage-links warrant used to exercise the
// class-mismatch refusal at the delivery commit surface.
func lineageWarrant(t *testing.T) gate.Warrant {
	t.Helper()
	intent, refusal := gate.NewLineageIntent(ir.HarnessClaudeCode, 1)
	if refusal != nil {
		t.Fatalf("build lineage intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		t.Fatalf("legalize lineage intent: %v", refusal)
	}
	return warrant
}

// TestReceiveRefusesZeroWarrantBeforeAnyIO pins that the sole durable writer
// refuses an ungated (zero-value) warrant with a typed *gate.Refusal BEFORE any
// blob or journal I/O — no Put, no Append. FAILS against the L1 stub (Authorize
// permits, so the delivery commits) until L3.
func TestReceiveRefusesZeroWarrantBeforeAnyIO(t *testing.T) {
	t.Parallel()
	calls := []string{}
	clock := testClock{now: time.Unix(10, 0)}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls}, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "gate-zero-warrant"}}
	_, err := s.Receive(boundedContext(t), gate.Warrant{}, validDelivery())
	if err == nil {
		t.Fatal("Receive accepted a zero-value warrant; want a typed *gate.Refusal")
	}
	var refusal *gate.Refusal
	if !stderrors.As(err, &refusal) {
		t.Fatalf("Receive error = %v (%T), want *gate.Refusal", err, err)
	}
	if refusal.Reason() != gate.RefusalInvalidIntent {
		t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), gate.RefusalInvalidIntent)
	}
	if len(calls) != 0 {
		t.Fatalf("I/O occurred before the gate refused: %v; want no blob or journal writes", calls)
	}
}

// TestReceiveRefusesClassMismatchWarrantBeforeAnyIO pins that the delivery
// commit surface refuses a warrant issued for another class (here lineage-links)
// with a typed *gate.Refusal before any I/O. FAILS against the L1 stub until L3.
func TestReceiveRefusesClassMismatchWarrantBeforeAnyIO(t *testing.T) {
	t.Parallel()
	calls := []string{}
	clock := testClock{now: time.Unix(10, 0)}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls}, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "gate-class-mismatch"}}
	_, err := s.Receive(boundedContext(t), lineageWarrant(t), validDelivery())
	if err == nil {
		t.Fatal("Receive accepted a lineage-links warrant for a delivery-receipt write; want a typed *gate.Refusal")
	}
	var refusal *gate.Refusal
	if !stderrors.As(err, &refusal) {
		t.Fatalf("Receive error = %v (%T), want *gate.Refusal", err, err)
	}
	if refusal.Reason() != gate.RefusalClassMismatch {
		t.Fatalf("refusal reason = %s, want %s", refusal.Reason(), gate.RefusalClassMismatch)
	}
	if len(calls) != 0 {
		t.Fatalf("I/O occurred before the gate refused: %v; want no blob or journal writes", calls)
	}
}

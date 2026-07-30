package receipt

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testOperations struct{ id string }

func (o testOperations) NewOperationID() (string, error) { return o.id, nil }

type testIdentity struct{}

func (testIdentity) ResolveLifecycleIdentity(context.Context) (Identity, error) {
	return Identity{Actor: provenance.ActorID{Namespace: "pasture-system"}, Authority: 7}, nil
}

type failingIdentity struct{}

func (failingIdentity) ResolveLifecycleIdentity(context.Context) (Identity, error) {
	return Identity{}, stderrors.New("simulated crash after blob commit")
}

type orderedBlobs struct {
	calls     *[]string
	failAfter bool
}

func (b orderedBlobs) Put(context.Context, digest.Digest, []byte) error {
	*b.calls = append(*b.calls, "blob")
	if b.failAfter {
		return stderrors.New("simulated crash")
	}
	return nil
}

type contextJournal struct {
	calls  *[]string
	result provenance.CommittedResult
	err    error
}

func (j contextJournal) ApplyContext(context.Context, provenance.OperationInput) (provenance.CommittedResult, error) {
	*j.calls = append(*j.calls, "occurrence")
	return j.result, j.err
}
func (j contextJournal) Apply(in provenance.OperationInput) (provenance.CommittedResult, error) {
	return j.ApplyContext(context.Background(), in)
}
func (contextJournal) Facts() provenance.FactQueryAPI { return nil }
func (contextJournal) QueryTaskEvents(provenance.JournalQueryV1) (provenance.JournalTaskEventPageV1, error) {
	return provenance.JournalTaskEventPageV1{}, nil
}
func (contextJournal) TaskAttributions(provenance.TaskID) ([]provenance.TaskAttribution, error) {
	return nil, nil
}
func (contextJournal) VerifyIntegrity() error                                      { return nil }
func (contextJournal) RegisterNamespaceClaim(provenance.ActorNamespaceClaim) error { return nil }
func (contextJournal) RegisterFixedActorEntry(provenance.FixedActorEntry) error    { return nil }
func (contextJournal) NamespaceClaims() ([]provenance.ActorNamespaceClaim, error)  { return nil, nil }
func (contextJournal) LookupCommitted(provenance.OperationID) (provenance.CommittedResult, error) {
	return provenance.CommittedResult{}, nil
}
func (contextJournal) AuthorityGovernsTaskAt(provenance.JournalID, provenance.TaskID, provenance.JournalID) (bool, error) {
	return false, nil
}
func (contextJournal) PreflightSchema() error { return nil }
func (contextJournal) ReplayProjections() (provenance.ReplayResult, error) {
	return provenance.ReplayResult{}, nil
}
func (contextJournal) MigrateLegacyBaseline(provenance.MigrationInput) (provenance.MigrationResult, error) {
	return provenance.MigrationResult{}, nil
}

func TestReceiveWritesBlobBeforeOccurrence(t *testing.T) {
	t.Parallel()
	calls := []string{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: receiptSlot, ProducedJournalID: 41}}}}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-order"}}
	r, err := s.Receive(context.Background(), validDelivery())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if r.JournalID() != 41 {
		t.Fatalf("receipt journal id = %d, want 41", r.JournalID())
	}
	if len(calls) != 2 || calls[0] != "blob" || calls[1] != "occurrence" {
		t.Fatalf("write order = %v, want [blob occurrence]", calls)
	}
}

func TestReceiveCrashAfterBlobLeavesNoOccurrence(t *testing.T) {
	t.Parallel()
	calls := []string{}
	clock := testClock{now: time.Unix(10, 0)}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls}, Clock: clock, Deadline: time.Second}, Identity: failingIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-crash"}}
	if _, err := s.Receive(context.Background(), validDelivery()); err == nil {
		t.Fatal("Receive succeeded, want simulated crash")
	}
	if len(calls) != 1 || calls[0] != "blob" {
		t.Fatalf("calls = %v, occurrence must not be attempted after blob failure", calls)
	}
}

func TestAppenderRequiresContextJournal(t *testing.T) {
	t.Parallel()
	_, err := (JournalAppender{Journal: nonContextJournal{}, Clock: testClock{now: time.Unix(10, 0)}, Deadline: time.Second}).Append(context.Background(), provenance.OperationInput{})
	if err == nil {
		t.Fatal("Append succeeded without ContextJournal")
	}
}

type nonContextJournal struct{ provenance.Journal }

func validDelivery() Delivery {
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.210")
	if err != nil {
		panic(err)
	}
	return Delivery{Contract: contract, Event: 1, Envelope: model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: contract}}, Capture: model.CaptureValid, Body: []byte(`{"session_id":"s"}`)}
}

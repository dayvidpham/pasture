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
	calls      *[]string
	inputs     *[]provenance.OperationInput
	applyCount *int
	result     provenance.CommittedResult
	err        error
}

func (j contextJournal) ApplyContext(_ context.Context, in provenance.OperationInput) (provenance.CommittedResult, error) {
	*j.calls = append(*j.calls, "occurrence")
	if j.inputs != nil {
		*j.inputs = append(*j.inputs, in)
	}
	if j.applyCount != nil {
		(*j.applyCount)++
	}
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
	inputs := []provenance.OperationInput{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: receiptSlot, ProducedJournalID: 41}}}}
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

	if len(inputs) != 1 || len(inputs[0].Effects) != 1 {
		t.Fatalf("zero-extra effects = %#v, want one occurrence effect", inputs)
	}
}

func TestReceiveAppendsOccurrenceAndExtrasInOneOperation(t *testing.T) {
	t.Parallel()
	calls := []string{}
	inputs := []provenance.OperationInput{}
	applyCount := 0
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{
		calls:      &calls,
		inputs:     &inputs,
		applyCount: &applyCount,
		result:     provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: receiptSlot, ProducedJournalID: 42}}},
	}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-extras"}}
	extraOne := provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    provenance.ResultSlotID("interpreted"),
		EvidenceKind:  provenance.EvidenceKind("pasture.lifecycle.interpreted.v1"),
		ContentDigest: []byte{1, 2, 3},
		Payload:       []byte(`{"semantic":1}`),
	}
	extraTwo := provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    provenance.ResultSlotID("consultation"),
		EvidenceKind:  provenance.EvidenceKind("pasture.lifecycle.consultation.v1"),
		ContentDigest: []byte{4, 5, 6},
		Payload:       []byte(`{"answer":"proceed"}`),
	}

	receipt, err := s.Receive(context.Background(), validDelivery(), extraOne, extraTwo)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if receipt.JournalID() != 42 {
		t.Fatalf("receipt journal id = %d, want 42", receipt.JournalID())
	}
	if applyCount != 1 || len(inputs) != 1 {
		t.Fatalf("ApplyContext calls = %d, recorded inputs = %d, want one each", applyCount, len(inputs))
	}
	effects := inputs[0].Effects
	if len(effects) != 3 {
		t.Fatalf("effects = %d, want occurrence plus two extras", len(effects))
	}
	if effects[0].ResultSlot != receiptSlot || effects[0].EvidenceKind != receiptKind {
		t.Fatalf("first effect = %#v, want occurrence effect", effects[0])
	}
	if effects[1].ResultSlot != extraOne.ResultSlot || effects[1].EvidenceKind != extraOne.EvidenceKind || string(effects[1].Payload) != string(extraOne.Payload) {
		t.Fatalf("second effect = %#v, want first extra %#v", effects[1], extraOne)
	}
	if effects[2].ResultSlot != extraTwo.ResultSlot || effects[2].EvidenceKind != extraTwo.EvidenceKind || string(effects[2].Payload) != string(extraTwo.Payload) {
		t.Fatalf("third effect = %#v, want second extra %#v", effects[2], extraTwo)
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

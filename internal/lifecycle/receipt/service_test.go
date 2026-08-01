package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	stderrors "errors"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

const (
	expectedOccurrenceResultSlot   = provenance.ResultSlotID("occurrence")
	expectedOccurrenceEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
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
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 41}}}}
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
	effect := inputs[0].Effects[0]
	if effect.Sort != provenance.EffectEvidence || effect.ResultSlot != expectedOccurrenceResultSlot || effect.EvidenceKind != expectedOccurrenceEvidenceKind {
		t.Fatalf("zero-extra occurrence effect = %#v, want independently pinned occurrence metadata", effect)
	}
}

func TestReceiveAppendsOccurrenceAndOneExtraInOneOperation(t *testing.T) {
	calls := []string{}
	inputs := []provenance.OperationInput{}
	applyCount := 0
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{
		calls:      &calls,
		inputs:     &inputs,
		applyCount: &applyCount,
		result:     provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 43}}},
	}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-one-extra"}}
	extra := provenance.Effect{
		Sort:         provenance.EffectEvidence,
		ResultSlot:   provenance.ResultSlotID("interpreted"),
		EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.interpreted.v1"),
		Payload:      []byte(`{"semantic":1}`),
	}
	digest := sha256.Sum256(extra.Payload)
	extra.ContentDigest = digest[:]

	receipt, err := s.Receive(context.Background(), validDelivery(), extra)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if receipt.JournalID() != 43 {
		t.Fatalf("receipt journal id = %d, want 43", receipt.JournalID())
	}
	if applyCount != 1 || len(inputs) != 1 {
		t.Fatalf("ApplyContext calls = %d, recorded inputs = %d, want one each", applyCount, len(inputs))
	}
	effects := inputs[0].Effects
	if len(effects) != 2 {
		t.Fatalf("effects = %d, want occurrence plus one extra", len(effects))
	}
	occurrence := effects[0]
	if occurrence.Sort != provenance.EffectEvidence || occurrence.ResultSlot != expectedOccurrenceResultSlot || occurrence.EvidenceKind != expectedOccurrenceEvidenceKind {
		t.Fatalf("first effect = %#v, want independently pinned occurrence metadata", occurrence)
	}
	if effects[1].Sort != extra.Sort || effects[1].ResultSlot != extra.ResultSlot || effects[1].EvidenceKind != extra.EvidenceKind || !bytes.Equal(effects[1].ContentDigest, extra.ContentDigest) || !bytes.Equal(effects[1].Payload, extra.Payload) {
		t.Fatalf("second effect = %#v, want byte-identical extra %#v", effects[1], extra)
	}
}

func TestReceiveRejectsForgedLifecycleExtraBeforeAppend(t *testing.T) {
	t.Parallel()
	calls := []string{}
	inputs := []provenance.OperationInput{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{}}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-forged-extra"}}
	forged := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: interpretedSlot, EvidenceKind: interpretedKind, ContentDigest: []byte{1}, Payload: []byte(`{"semantic":1}`)}
	if _, err := s.Receive(context.Background(), validDelivery(), forged); err == nil {
		t.Fatal("Receive accepted a forged interpreted effect; want validation before Append")
	}
	if len(inputs) != 0 {
		t.Fatalf("Append inputs = %d, want zero for forged pair", len(inputs))
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
		result:     provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 42}}},
	}
	s := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-extras"}}
	extraOne := provenance.Effect{
		Sort:         provenance.EffectEvidence,
		ResultSlot:   provenance.ResultSlotID("interpreted"),
		EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.interpreted.v1"),
		Payload:      []byte(`{"semantic":1}`),
	}
	firstDigest := sha256.Sum256(extraOne.Payload)
	extraOne.ContentDigest = firstDigest[:]
	firstRef := digest.FromBytes(extraOne.Payload)
	extraTwo := provenance.Effect{
		Sort:         provenance.EffectEvidence,
		ResultSlot:   provenance.ResultSlotID("consultation"),
		EvidenceKind: provenance.EvidenceKind("pasture.lifecycle.consultation.v1"),
		Payload:      []byte(`{"interpreted":{"result_slot":"interpreted","content_digest":"` + firstRef.String() + `"}}`),
	}
	secondDigest := sha256.Sum256(extraTwo.Payload)
	extraTwo.ContentDigest = secondDigest[:]

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
	if effects[0].Sort != provenance.EffectEvidence || effects[0].ResultSlot != expectedOccurrenceResultSlot || effects[0].EvidenceKind != expectedOccurrenceEvidenceKind {
		t.Fatalf("first effect = %#v, want occurrence effect", effects[0])
	}
	if effects[1].Sort != extraOne.Sort || effects[1].ResultSlot != extraOne.ResultSlot || effects[1].EvidenceKind != extraOne.EvidenceKind || !bytes.Equal(effects[1].ContentDigest, extraOne.ContentDigest) || !bytes.Equal(effects[1].Payload, extraOne.Payload) {
		t.Fatalf("second effect = %#v, want first extra %#v", effects[1], extraOne)
	}
	if effects[2].Sort != extraTwo.Sort || effects[2].ResultSlot != extraTwo.ResultSlot || effects[2].EvidenceKind != extraTwo.EvidenceKind || !bytes.Equal(effects[2].ContentDigest, extraTwo.ContentDigest) || !bytes.Equal(effects[2].Payload, extraTwo.Payload) {
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

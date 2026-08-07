package backend_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

func TestBuildConsultationDelegatesCanonicalRecord(t *testing.T) {
	t.Parallel()
	l2 := realL2(t, runtime.ClaudeEventPostToolBatch)
	if facts := l2.Semantics().UnresolvedFacts(); len(facts) != 1 || facts[0].Reason != waist.UnresolvedToolCall {
		t.Fatalf("PostToolBatch unresolved facts = %#v, want tool-call-unresolved", facts)
	}
	interpreted := interpretedRecord(t, l2)
	result, err := legalize.Event(l2)
	if err != nil {
		t.Fatal(err)
	}
	legalized, ok := result.Legalized()
	if !ok {
		t.Fatal("gate result has no Legalized terminal")
	}
	record, response, err := backend.BuildConsultation(interpreted, legalized)
	if err != nil {
		t.Fatalf("BuildConsultation() error = %v", err)
	}
	if !record.IsValid() || !response.IsValid() || response.Decision() != backend.DecisionProceed {
		t.Fatalf("BuildConsultation() = %#v, %#v", record, response)
	}
	raw, err := response.MarshalJSON()
	if err != nil || !bytes.Equal(raw, []byte(`{"decision":"proceed"}`)) {
		t.Fatalf("HostResponse JSON = %s, %v", raw, err)
	}
	direct, err := receipt.NewConsultation(interpreted, legalized, response)
	if err != nil {
		t.Fatalf("direct NewConsultation() error = %v", err)
	}
	assertEffectEqual(t, record.Effect(), direct.Effect())
}

func TestBuildConsultationRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	gateL2 := realL2(t, runtime.ClaudeEventPreToolUse)
	gateResult, _ := legalize.Event(gateL2)
	legalized, _ := gateResult.Legalized()
	observation := interpretedRecord(t, realL2(t, runtime.ClaudeEventSessionStart))
	tests := []struct {
		name        string
		interpreted receipt.Record
		legalized   legalize.Legalized
	}{
		{name: "zero interpreted", legalized: legalized},
		{name: "non-gate interpreted", interpreted: observation, legalized: legalized},
		{name: "zero legalized", interpreted: interpretedRecord(t, gateL2)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record, response, err := backend.BuildConsultation(test.interpreted, test.legalized)
			if err == nil || record.IsValid() || response.IsValid() {
				t.Fatalf("BuildConsultation invalid inputs = %#v, %#v, %v", record, response, err)
			}
		})
	}
}

func TestZeroDecisionAndHostResponse(t *testing.T) {
	t.Parallel()
	if backend.Decision(0).IsValid() || backend.Decision(0).String() != "" || backend.Decision(2).IsValid() {
		t.Fatal("invalid Decision accepted")
	}
	if !backend.DecisionProceed.IsValid() || backend.DecisionProceed.String() != "proceed" {
		t.Fatal("DecisionProceed invalid")
	}
	var response backend.HostResponse
	if response.IsValid() || response.Decision() != 0 {
		t.Fatal("zero HostResponse valid")
	}
	if raw, err := response.MarshalJSON(); err == nil || raw != nil {
		t.Fatalf("zero HostResponse MarshalJSON = %q, %v", raw, err)
	}
}

func TestConsultationEffectsAreAcceptedByReceiptServiceInOrder(t *testing.T) {
	t.Parallel()
	l2 := realL2(t, runtime.ClaudeEventElicitation)
	interpreted := interpretedRecord(t, l2)
	result, _ := legalize.Event(l2)
	legalized, _ := result.Legalized()
	consultation, _, err := backend.BuildConsultation(interpreted, legalized)
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	inputs := []provenance.OperationInput{}
	clock := fixedClock{now: time.Unix(10, 0)}
	service := receipt.Service{
		Blobs:      blobFake{calls: &calls},
		Appender:   receipt.JournalAppender{Journal: journalFake{calls: &calls, inputs: &inputs}, Clock: clock, Deadline: time.Second},
		Identity:   identityFake{},
		Clock:      clock,
		Operations: operationFake{},
	}
	delivery := receipt.Delivery{
		Contract: runtime.ClaudeCode2_1_210Lifecycle().ID(),
		Event:    model.ContractEventKind(runtime.ClaudeEventElicitation),
		Envelope: model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: runtime.ClaudeCode2_1_210Lifecycle().ID()}},
		Capture:  model.CaptureValid,
		Body:     []byte(`{"hook_event_name":"Elicitation"}`),
	}
	deliveryIntent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	if refusal != nil {
		t.Fatalf("build delivery intent: %v", refusal)
	}
	warrant, refusal := gate.Legalize(deliveryIntent)
	if refusal != nil {
		t.Fatalf("legalize delivery intent: %v", refusal)
	}
	if _, err := service.Receive(context.Background(), warrant, delivery, interpreted.Effect(), consultation.Effect()); err != nil {
		t.Fatalf("Receive() rejected production effects: %v", err)
	}
	if len(calls) != 2 || calls[0] != "blob" || calls[1] != "append" {
		t.Fatalf("calls = %v, want [blob append]", calls)
	}
	if len(inputs) != 1 || len(inputs[0].Effects) != 3 {
		t.Fatalf("operation inputs = %#v, want occurrence + interpreted + consultation", inputs)
	}
	if inputs[0].Effects[1].ResultSlot != interpreted.Effect().ResultSlot || inputs[0].Effects[2].ResultSlot != consultation.Effect().ResultSlot {
		t.Fatalf("effect order = %#v", inputs[0].Effects)
	}
}

func realL2(t *testing.T, event runtime.ClaudeLifecycleEvent) waist.L2 {
	t.Helper()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	binding, err := waist.BindEvent(contract, event)
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]waist.Identity, 0, len(binding.DeclaredIdentities()))
	for index, field := range binding.DeclaredIdentities() {
		identity, identityErr := waist.NewIdentity(field.Kind(), field.NativeName(), field.NativeName()+"-value-"+string(rune('a'+index)))
		if identityErr != nil {
			t.Fatal(identityErr)
		}
		identities = append(identities, identity)
	}
	l2, err := binding.NewEvent(identities)
	if err != nil {
		t.Fatal(err)
	}
	return l2
}

func interpretedRecord(t *testing.T, l2 waist.L2) receipt.Record {
	t.Helper()
	record, err := receipt.NewInterpreted(l2, l2.Origin().Contract(), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func assertEffectEqual(t *testing.T, got, want provenance.Effect) {
	t.Helper()
	if got.Sort != want.Sort || got.ResultSlot != want.ResultSlot || got.EvidenceKind != want.EvidenceKind || !bytes.Equal(got.ContentDigest, want.ContentDigest) || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("effect = %#v, want %#v", got, want)
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type operationFake struct{}

func (operationFake) NewOperationID() (string, error) { return "backend-integration", nil }

type identityFake struct{}

func (identityFake) ResolveLifecycleIdentity(context.Context) (receipt.Identity, error) {
	return receipt.Identity{Actor: provenance.ActorID{Namespace: "pasture-system"}, Authority: 7}, nil
}

type blobFake struct{ calls *[]string }

func (b blobFake) Put(context.Context, digest.Digest, []byte) error {
	*b.calls = append(*b.calls, "blob")
	return nil
}

type journalFake struct {
	calls  *[]string
	inputs *[]provenance.OperationInput
}

func (j journalFake) ApplyContext(_ context.Context, input provenance.OperationInput) (provenance.CommittedResult, error) {
	*j.calls = append(*j.calls, "append")
	*j.inputs = append(*j.inputs, input)
	return provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: "occurrence", ProducedJournalID: 1}}}, nil
}
func (j journalFake) Apply(input provenance.OperationInput) (provenance.CommittedResult, error) {
	return j.ApplyContext(context.Background(), input)
}
func (journalFake) Facts() provenance.FactQueryAPI { return nil }
func (journalFake) QueryTaskEvents(provenance.JournalQueryV1) (provenance.JournalTaskEventPageV1, error) {
	return provenance.JournalTaskEventPageV1{}, nil
}
func (journalFake) TaskAttributions(provenance.TaskID) ([]provenance.TaskAttribution, error) {
	return nil, nil
}
func (journalFake) VerifyIntegrity() error                                      { return nil }
func (journalFake) RegisterNamespaceClaim(provenance.ActorNamespaceClaim) error { return nil }
func (journalFake) RegisterFixedActorEntry(provenance.FixedActorEntry) error    { return nil }
func (journalFake) NamespaceClaims() ([]provenance.ActorNamespaceClaim, error)  { return nil, nil }
func (journalFake) LookupCommitted(provenance.OperationID) (provenance.CommittedResult, error) {
	return provenance.CommittedResult{}, nil
}
func (journalFake) AuthorityGovernsTaskAt(provenance.JournalID, provenance.TaskID, provenance.JournalID) (bool, error) {
	return false, nil
}
func (journalFake) PreflightSchema() error { return nil }
func (journalFake) ReplayProjections() (provenance.ReplayResult, error) {
	return provenance.ReplayResult{}, nil
}
func (journalFake) MigrateLegacyBaseline(provenance.MigrationInput) (provenance.MigrationResult, error) {
	return provenance.MigrationResult{}, nil
}

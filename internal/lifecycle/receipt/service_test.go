package receipt

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
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

type codedError struct {
	code int
}

func (e codedError) Error() string { return fmt.Sprintf("coded sqlite error %d", e.code) }
func (e codedError) Code() int     { return e.code }
func (contextJournal) MigrateLegacyBaseline(provenance.MigrationInput) (provenance.MigrationResult, error) {
	return provenance.MigrationResult{}, nil
}

func TestReceiveWritesBlobBeforeOccurrence(t *testing.T) {
	t.Parallel()
	calls := []string{}
	inputs := []provenance.OperationInput{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 41}}}}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-order"}}
	r, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery())
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
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-one-extra"}}
	interpreted, err := NewInterpreted(mustSessionStartL2(t, "session-1"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	extra := interpreted.Effect()

	receipt, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery(), extra)
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
	if effects[1].Sort != extra.Sort || effects[1].ResultSlot != extra.ResultSlot || effects[1].EvidenceKind != extra.EvidenceKind || !effectDigestValid(effects[1]) || validateInterpretedPayload(effects[1].Payload) != nil {
		t.Fatalf("second effect is not canonical interpreted evidence: %#v", effects[1])
	}
}

func TestReceiveRejectsForgedLifecycleExtraBeforeAppend(t *testing.T) {
	t.Parallel()
	calls := []string{}
	inputs := []provenance.OperationInput{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{}}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-forged-extra"}}
	forged := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: interpretedSlot, EvidenceKind: interpretedKind, ContentDigest: []byte{1}, Payload: []byte(`{"semantic":1}`)}
	if _, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery(), forged); err == nil {
		t.Fatal("Receive accepted a forged interpreted effect; want validation before Append")
	}
	if len(inputs) != 0 {
		t.Fatalf("Append inputs = %d, want zero for forged pair", len(inputs))
	}
}

func TestReceiveRejectsUnnormalizedBindingsBeforeWrites(t *testing.T) {
	t.Parallel()
	cases := []model.NativeBinding{{Kind: 0, NativeName: "session_id", Value: "x"}, {Kind: model.BindingSession, Value: "x"}, {Kind: model.BindingSession, NativeName: " session_id", Value: "x"}, {Kind: model.BindingSession, NativeName: "session_id", Value: " x"}, {Kind: model.BindingSession, NativeName: "session_id", Value: "x\x00"}, {Kind: model.BindingSession, NativeName: string([]byte{0xff}), Value: "x"}}
	for _, binding := range cases {
		binding := binding
		t.Run(fmt.Sprintf("%d-%q-%q", binding.Kind, binding.NativeName, binding.Value), func(t *testing.T) {
			t.Parallel()
			calls := []string{}
			inputs := []provenance.OperationInput{}
			clock := testClock{now: time.Unix(10, 0)}
			service := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls, inputs: &inputs}, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "invalid-binding"}}
			delivery := validDelivery()
			delivery.Bindings = []model.NativeBinding{binding}
			if _, err := service.Receive(context.Background(), mustDeliveryWarrant(), delivery); err == nil {
				t.Fatal("accepted invalid binding")
			}
			if len(calls) != 0 || len(inputs) != 0 {
				t.Fatalf("writes occurred: %v %#v", calls, inputs)
			}
		})
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
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-extras"}}
	interpreted, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	extraOne := interpreted.Effect()
	consultation, err := NewConsultation(interpreted, jsonPort{raw: []byte(`{"rule":"allow"}`), valid: true}, jsonPort{raw: []byte(`{"decision":"proceed"}`), valid: true})
	if err != nil {
		t.Fatal(err)
	}
	extraTwo := consultation.Effect()

	receipt, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery(), extraOne, extraTwo)
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
	if effects[1].Sort != extraOne.Sort || effects[1].ResultSlot != extraOne.ResultSlot || effects[1].EvidenceKind != extraOne.EvidenceKind || !effectDigestValid(effects[1]) || validateInterpretedPayload(effects[1].Payload) != nil {
		t.Fatalf("second effect is not canonical interpreted evidence: %#v", effects[1])
	}
	if effects[2].Sort != extraTwo.Sort || effects[2].ResultSlot != extraTwo.ResultSlot || effects[2].EvidenceKind != extraTwo.EvidenceKind || !effectDigestValid(effects[2]) || validateConsultationPayload(effects[2].Payload, effects[1].Payload) != nil {
		t.Fatalf("third effect is not canonical consultation evidence: %#v", effects[2])
	}
}

func TestReceiveCrashAfterBlobLeavesNoOccurrence(t *testing.T) {
	t.Parallel()
	calls := []string{}
	clock := testClock{now: time.Unix(10, 0)}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls}, Clock: clock, Deadline: time.Second}, Identity: failingIdentity{}, Clock: clock, Operations: testOperations{id: "receipt-crash"}}
	if _, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery()); err == nil {
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

func TestAppenderClassifiesContextStateBeforeUpstreamError(t *testing.T) {
	t.Parallel()
	contentionUpstream := stderrors.Join(context.DeadlineExceeded, codedError{code: 5})
	cases := []struct {
		name     string
		context  func() context.Context
		upstream error
		want     string
	}{
		{
			name:     "live code-compatible non-SQLite error remains storage",
			context:  context.Background,
			upstream: contentionUpstream,
			want:     "storage",
		},
		{
			name: "canceled parent preserves cancellation",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			upstream: codedError{code: 5},
			want:     "canceled",
		},
		{
			name: "canceled parent keeps upstream cancellation chain",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			upstream: stderrors.Join(codedError{code: 5}, context.Canceled),
			want:     "canceled-preserved",
		},
		{
			name: "already expired parent wins over base busy",
			context: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				cancel()
				return ctx
			},
			upstream: codedError{code: 5},
			want:     "deadline",
		},
		{
			name:     "live non busy deadline wrapper remains storage",
			context:  context.Background,
			upstream: fmt.Errorf("non-busy storage error: %w", context.DeadlineExceeded),
			want:     "storage",
		},
		{
			name:     "live extended busy remains storage",
			context:  context.Background,
			upstream: codedError{code: 517},
			want:     "storage",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := []string{}
			applyCount := 0
			journal := contextJournal{calls: &calls, applyCount: &applyCount, err: tc.upstream}
			appender := JournalAppender{Journal: journal, Clock: testClock{now: time.Unix(10, 0)}, Deadline: time.Second}
			got, err := appender.Append(tc.context(), provenance.OperationInput{})
			if got != 0 {
				t.Fatalf("journal id = %d, want zero on failure", got)
			}
			if applyCount != 1 {
				t.Fatalf("ApplyContext calls = %d, want one", applyCount)
			}

			var contention model.IngressContentionError
			var deadline model.IngressDeadlineError
			var storage *pasterrors.StructuredError
			switch tc.want {
			case "canceled", "canceled-preserved":
				if !stderrors.As(err, &storage) {
					t.Fatalf("error = %v, want structured storage error", err)
				}
				if tc.want == "canceled" && storage.Cause == tc.upstream {
					t.Fatal("canceled storage cause did not join bounded cancellation")
				}
				if tc.want == "canceled-preserved" && storage.Cause != tc.upstream {
					t.Fatalf("canceled storage cause = %v, want unchanged upstream error %v", storage.Cause, tc.upstream)
				}
				if !stderrors.Is(storage.Cause, tc.upstream) || !stderrors.Is(storage.Cause, context.Canceled) {
					t.Fatalf("storage cause = %v, want upstream and context.Canceled", storage.Cause)
				}
				if !stderrors.Is(err, context.Canceled) {
					t.Fatal("canceled storage error does not preserve context.Canceled")
				}
				if stderrors.As(err, &contention) || stderrors.As(err, &deadline) {
					t.Fatalf("canceled error received deadline/contention type: %v", err)
				}
			case "deadline":
				if !stderrors.As(err, &deadline) {
					t.Fatalf("error = %v, want IngressDeadlineError", err)
				}
				if stderrors.As(err, &contention) || stderrors.As(err, &storage) {
					t.Fatalf("deadline error also classified as another type: %v", err)
				}
			case "storage":
				if !stderrors.As(err, &storage) {
					t.Fatalf("error = %v, want structured storage error", err)
				}
				if storage.Cause != tc.upstream {
					t.Fatalf("storage cause = %v, want upstream error %v", storage.Cause, tc.upstream)
				}
				if stderrors.As(err, &contention) || stderrors.As(err, &deadline) {
					t.Fatalf("storage error received contention/deadline type: %v", err)
				}
			}
		})
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

// mustDeliveryWarrant builds a valid delivery-receipt warrant for the receipt
// tests, mirroring validDelivery's panic-on-error style. Receive requires a
// delivery-receipt warrant; the gate certifies only the write class, so one
// warrant covers every delivery in these tests.
func mustDeliveryWarrant() gate.Warrant {
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.210")
	if err != nil {
		panic(err)
	}
	intent, refusal := gate.NewDeliveryIntent(contract, 1)
	if refusal != nil {
		panic(refusal)
	}
	warrant, refusal := gate.Legalize(intent)
	if refusal != nil {
		panic(refusal)
	}
	return warrant
}

func windowService(calls *[]string) Service {
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: calls, inputs: &[]provenance.OperationInput{}, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 41}}}}
	return Service{Window: time.Second, Blobs: orderedBlobs{calls: calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "writer-window"}}
}

func requireWindowRefusal(t *testing.T, err error, calls []string, phrases ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Receive must refuse")
	}
	for _, phrase := range phrases {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("refusal %q must contain %q", err.Error(), phrase)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("the refusal must come before any I/O; calls = %v", calls)
	}
	var structuredErr *pasterrors.StructuredError
	if !stderrors.As(err, &structuredErr) || structuredErr.Category != pasterrors.CategoryValidation {
		t.Fatalf("the refusal must be a validation error, got %v", err)
	}
}

func requireCommitted(t *testing.T, r Receipt, err error, calls []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("the delivery must be accepted: %v", err)
	}
	if r.JournalID() != 41 || len(calls) != 2 {
		t.Fatalf("accepted delivery must commit blob then occurrence; id=%d calls=%v", r.JournalID(), calls)
	}
}

// TestReceiveAcceptsAContextWithNoDeadline: a context bounded by cancellation
// is bounded and reports no deadline; the bound on production writers is
// enforced where their contexts are made, not here.
func TestReceiveAcceptsAContextWithNoDeadline(t *testing.T) {
	t.Parallel()
	calls := []string{}
	r, err := windowService(&calls).Receive(context.Background(), mustDeliveryWarrant(), validDelivery())
	requireCommitted(t, r, err, calls)
}

// TestReceiveAcceptsADeadlineInsideTheWindow is the control for the refusal
// below: a deadline inside the window commits blob then occurrence.
func TestReceiveAcceptsADeadlineInsideTheWindow(t *testing.T) {
	t.Parallel()
	calls := []string{}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r, err := windowService(&calls).Receive(ctx, mustDeliveryWarrant(), validDelivery())
	requireCommitted(t, r, err, calls)
}

// TestReceiveRefusesADeadlineBeyondTheWriterWindow: a deadline that is
// present must lie inside the window, or the writer may outlive the bound
// the orphan reclaim ages blobs against. The refusal comes before any I/O
// and names the tier.
func TestReceiveRefusesADeadlineBeyondTheWriterWindow(t *testing.T) {
	t.Parallel()
	calls := []string{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := windowService(&calls).Receive(ctx, mustDeliveryWarrant(), validDelivery())
	requireWindowRefusal(t, err, calls, "beyond the 1s writer window", "WorkflowResult tier")
}

// TestReceiveRefusesAServiceWithoutAWriterWindow: a zero window is a service
// built outside the production constructor; it refuses rather than defaulting,
// because a silent zero would disable the bound.
func TestReceiveRefusesAServiceWithoutAWriterWindow(t *testing.T) {
	t.Parallel()
	calls := []string{}
	s := windowService(&calls)
	s.Window = 0
	_, err := s.Receive(context.Background(), mustDeliveryWarrant(), validDelivery())
	requireWindowRefusal(t, err, calls, "has no writer window", "WorkflowResult tier")
}

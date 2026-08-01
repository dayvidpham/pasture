package receipt

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

func TestNewInterpretedSessionStartPreservesTypedValues(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	wantValue := "session-é-<>&"
	l2 := mustSessionStartL2(t, wantValue)

	record, err := NewInterpreted(l2, contract)
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	if !record.IsValid() {
		t.Fatal("NewInterpreted() returned an invalid record")
	}
	if got := record.Semantic(); got != runtime.SemanticObservation {
		t.Fatalf("Semantic() = %v, want %v", got, runtime.SemanticObservation)
	}
	identities := record.Identities()
	if len(identities) != 1 {
		t.Fatalf("Identities() length = %d, want 1", len(identities))
	}
	if identities[0].Kind != runtime.IdentitySession {
		t.Fatalf("identity kind = %v, want %v", identities[0].Kind, runtime.IdentitySession)
	}
	if !bytes.Equal([]byte(identities[0].Value), []byte(wantValue)) {
		t.Fatalf("identity value bytes = %x, want %x", []byte(identities[0].Value), []byte(wantValue))
	}
	if got := record.UnresolvedFacts(); len(got) != 0 {
		t.Fatalf("UnresolvedFacts() = %#v, want empty", got)
	}
	if got := record.Contract(); got != contract {
		t.Fatalf("Contract() = %q, want %q", got, contract)
	}
}

func TestNewInterpretedPostToolBatchPreservesUnresolvedFact(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	l2 := mustPostToolBatchL2(t, "session-1")
	record, err := NewInterpreted(l2, contract)
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	if !record.IsValid() {
		t.Fatal("NewInterpreted() returned an invalid record")
	}
	if got := record.Semantic(); got != runtime.SemanticGateConsultation {
		t.Fatalf("Semantic() = %v, want %v", got, runtime.SemanticGateConsultation)
	}
	if got := record.Identities(); len(got) != 1 || got[0] != (waist.SemanticIdentity{Kind: runtime.IdentitySession, Value: "session-1"}) {
		t.Fatalf("Identities() = %#v, want the session identity only", got)
	}
	wantFacts := []waist.UnresolvedFact{{Reason: waist.UnresolvedToolCall}}
	if got := record.UnresolvedFacts(); !reflect.DeepEqual(got, wantFacts) {
		t.Fatalf("UnresolvedFacts() = %#v, want %#v", got, wantFacts)
	}
}

func TestNewInterpretedRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	l2 := mustSessionStartL2(t, "session-1")
	mismatched, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.220")
	if err != nil {
		t.Fatalf("NewRuntimeContractID() error = %v", err)
	}
	tests := []struct {
		name     string
		l2       waist.L2
		contract ir.RuntimeContractID
	}{
		{name: "zero L2", l2: waist.L2{}, contract: contract},
		{name: "invalid contract", l2: l2, contract: ir.RuntimeContractID{}},
		{name: "mismatched contract", l2: l2, contract: mismatched},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record, err := NewInterpreted(test.l2, test.contract)
			if err == nil {
				t.Fatal("NewInterpreted() succeeded, want validation error")
			}
			assertActionableValidationError(t, err)
			if !reflect.DeepEqual(record, Record{}) {
				t.Fatalf("failed construction returned %#v, want zero Record", record)
			}
			if got := record.Effect(); !reflect.DeepEqual(got, provenance.Effect{}) {
				t.Fatalf("failed construction effect = %#v, want zero effect", got)
			}
		})
	}
}

func TestInterpretedAccessorsReturnDefensiveCopies(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	record, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), contract)
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	if !record.IsValid() {
		t.Fatal("NewInterpreted() returned an invalid record")
	}
	identities := record.Identities()
	identities[0].Value = "changed"
	if got := record.Identities(); got[0].Value != "session-1" {
		t.Fatalf("identity accessor leaked mutable storage: %#v", got)
	}
	facts := record.UnresolvedFacts()
	facts[0].Reason = 0
	if got := record.UnresolvedFacts(); !reflect.DeepEqual(got, []waist.UnresolvedFact{{Reason: waist.UnresolvedToolCall}}) {
		t.Fatalf("unresolved accessor leaked mutable storage: %#v", got)
	}
}

func TestInterpretedEffectIsCanonicalAndOwnsBytes(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	record, err := NewInterpreted(mustSessionStartL2(t, "session-é-<>&"), contract)
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	effect := record.Effect()
	wantPayload := []byte(`{"semantic":1,"identities":[{"kind":1,"value":"session-é-<>&"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210"}`)
	if effect.Sort != provenance.EffectEvidence {
		t.Fatalf("effect sort = %v, want %v", effect.Sort, provenance.EffectEvidence)
	}
	if effect.ResultSlot != interpretedSlot {
		t.Fatalf("effect result slot = %q, want %q", effect.ResultSlot, interpretedSlot)
	}
	if effect.EvidenceKind != interpretedKind {
		t.Fatalf("effect evidence kind = %q, want %q", effect.EvidenceKind, interpretedKind)
	}
	if !bytes.Equal(effect.Payload, wantPayload) {
		t.Fatalf("effect payload = %s, want %s", effect.Payload, wantPayload)
	}
	wantDigest := sha256.Sum256(wantPayload)
	if !bytes.Equal(effect.ContentDigest, wantDigest[:]) {
		t.Fatalf("effect digest = %x, want %x", effect.ContentDigest, wantDigest)
	}

	effect.Payload[0] = 'X'
	effect.ContentDigest[0] ^= 0xff
	again := record.Effect()
	if !bytes.Equal(again.Payload, wantPayload) {
		t.Fatalf("record payload changed through returned effect bytes: %s", again.Payload)
	}
	if !bytes.Equal(again.ContentDigest, wantDigest[:]) {
		t.Fatalf("record digest changed through returned effect bytes: %x", again.ContentDigest)
	}
}

func TestZeroRecordEffectIsSafe(t *testing.T) {
	t.Parallel()

	if got := (Record{}).Effect(); !reflect.DeepEqual(got, provenance.Effect{}) {
		t.Fatalf("zero record effect = %#v, want zero effect", got)
	}
}

func mustClaudeLifecycleContract(t *testing.T) ir.RuntimeContractID {
	t.Helper()
	return runtime.ClaudeCode2_1_210Lifecycle().ID()
}

func mustSessionStartL2(t *testing.T, value string) waist.L2 {
	t.Helper()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	binding, err := waist.BindEvent(contract, runtime.ClaudeEventSessionStart)
	if err != nil {
		t.Fatalf("BindEvent(SessionStart) error = %v", err)
	}
	identity, err := waist.NewIdentity(runtime.IdentitySession, "session_id", value)
	if err != nil {
		t.Fatalf("NewIdentity(session_id) error = %v", err)
	}
	l2, err := binding.NewEvent([]waist.Identity{identity})
	if err != nil {
		t.Fatalf("NewEvent(SessionStart) error = %v", err)
	}
	return l2
}

func mustPostToolBatchL2(t *testing.T, value string) waist.L2 {
	t.Helper()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	binding, err := waist.BindEvent(contract, runtime.ClaudeEventPostToolBatch)
	if err != nil {
		t.Fatalf("BindEvent(PostToolBatch) error = %v", err)
	}
	identity, err := waist.NewIdentity(runtime.IdentitySession, "session_id", value)
	if err != nil {
		t.Fatalf("NewIdentity(session_id) error = %v", err)
	}
	l2, err := binding.NewEvent([]waist.Identity{identity})
	if err != nil {
		t.Fatalf("NewEvent(PostToolBatch) error = %v", err)
	}
	return l2
}

func assertActionableValidationError(t *testing.T, err error) {
	t.Helper()
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *errors.StructuredError", err)
	}
	if structured.Category != pasterrors.CategoryValidation {
		t.Fatalf("error category = %q, want %q", structured.Category, pasterrors.CategoryValidation)
	}
	if structured.What == "" || structured.Why == "" || structured.Where == "" || structured.Impact == "" || structured.Fix == "" {
		t.Fatalf("validation error is not actionable: %#v", structured)
	}
}

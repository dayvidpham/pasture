package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

const (
	expectedInterpretedResultSlot   = provenance.ResultSlotID("interpreted")
	expectedInterpretedEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.interpreted.v2")
	expectedInterpretedContract     = "claude-code/claude-code@2.1.210"
)

func TestNewInterpretedSessionStartPreservesTypedValues(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	wantValue := "session-é-<>&"
	l2 := mustSessionStartL2(t, wantValue)

	record, err := NewInterpreted(l2, contract, metamodel.Active())
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
	record, err := NewInterpreted(l2, contract, metamodel.Active())
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
		name                  string
		l2                    waist.L2
		contract              ir.RuntimeContractID
		wantWhat              string
		wantContractFragments []string
	}{
		{name: "zero L2", l2: waist.L2{}, contract: contract, wantWhat: "source is invalid"},
		{name: "invalid contract", l2: l2, contract: ir.RuntimeContractID{}, wantWhat: "invalid runtime contract"},
		{
			name:                  "mismatched contract",
			l2:                    l2,
			contract:              mismatched,
			wantWhat:              "does not match",
			wantContractFragments: []string{"claude-code/2.1.220", "claude-code/claude-code@2.1.210"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record, err := NewInterpreted(test.l2, test.contract, metamodel.Active())
			if err == nil {
				t.Fatal("NewInterpreted() succeeded, want validation error")
			}
			assertActionableValidationError(t, err, test.wantWhat, test.wantContractFragments...)
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
	record, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), contract, metamodel.Active())
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
	record, err := NewInterpreted(mustSessionStartL2(t, "session-é-<>&"), contract, metamodel.Active())
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	effect := record.Effect()
	activeContent := metamodel.Active().Content
	wantPayload := []byte(`{"semantic":1,"identities":[{"kind":1,"value":"session-é-<>&"}],"unresolved_facts":[],"contract":"claude-code/claude-code@2.1.210","manifest":{"id":"pasture.lifecycle.metamodel","version":1,"content":"` + hex.EncodeToString(activeContent[:]) + `"}}`)
	decoded := mustDecodeInterpretedEffectPayload(t, effect.Payload)
	if decoded.Semantic != uint8(runtime.SemanticObservation) {
		t.Fatalf("decoded semantic = %d, want %d", decoded.Semantic, runtime.SemanticObservation)
	}
	if len(decoded.Identities) != 1 || decoded.Identities[0].Kind != uint8(runtime.IdentitySession) || decoded.Identities[0].Value != "session-é-<>&" {
		t.Fatalf("decoded identities = %#v, want the exact session identity", decoded.Identities)
	}
	if len(decoded.UnresolvedFacts) != 0 {
		t.Fatalf("decoded unresolved facts = %#v, want empty", decoded.UnresolvedFacts)
	}
	if decoded.Contract != expectedInterpretedContract {
		t.Fatalf("decoded contract = %q, want %q", decoded.Contract, expectedInterpretedContract)
	}
	assertInterpretedEffectEnvelope(t, effect)
	if !bytes.Equal(effect.Payload, wantPayload) {
		t.Fatalf("effect payload = %s, want %s", effect.Payload, wantPayload)
	}
	wantDigest := sha256.Sum256(effect.Payload)
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

func TestInterpretedEffectPayloadRejectsDuplicateMembers(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	record, err := NewInterpreted(mustSessionStartL2(t, "session-1"), contract, metamodel.Active())
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	effect := record.Effect()
	needle := []byte(`"contract":"` + expectedInterpretedContract + `"`)
	duplicate := bytes.Replace(effect.Payload, needle, []byte(`"contract":"forged","contract":"`+expectedInterpretedContract+`"`), 1)
	if bytes.Equal(duplicate, effect.Payload) {
		t.Fatal("test setup did not insert a duplicate contract member")
	}
	if _, err := decodeInterpretedEffectPayload(duplicate); err == nil {
		t.Fatal("duplicate contract member was accepted")
	} else if !ir.IsDuplicateJSONMember(err) {
		t.Fatalf("duplicate contract member error = %v, want duplicate-member classification", err)
	}
}

func TestInterpretedPostToolBatchEffectPreservesUnresolvedFact(t *testing.T) {
	t.Parallel()

	contract := mustClaudeLifecycleContract(t)
	record, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), contract, metamodel.Active())
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	effect := record.Effect()
	assertInterpretedEffectEnvelope(t, effect)

	decoded := mustDecodeInterpretedEffectPayload(t, effect.Payload)
	if decoded.Semantic != uint8(runtime.SemanticGateConsultation) {
		t.Fatalf("decoded semantic = %d, want %d", decoded.Semantic, runtime.SemanticGateConsultation)
	}
	if len(decoded.Identities) != 1 || decoded.Identities[0].Kind != uint8(runtime.IdentitySession) || decoded.Identities[0].Value != "session-1" {
		t.Fatalf("decoded identities = %#v, want the exact session identity", decoded.Identities)
	}
	if len(decoded.UnresolvedFacts) != 1 {
		t.Fatalf("decoded unresolved facts = %#v, want exactly one fact", decoded.UnresolvedFacts)
	}
	if decoded.UnresolvedFacts[0].Reason != uint8(waist.UnresolvedToolCall) {
		t.Fatalf("decoded unresolved reason = %d, want %d", decoded.UnresolvedFacts[0].Reason, waist.UnresolvedToolCall)
	}
	if got := waist.UnresolvedReason(decoded.UnresolvedFacts[0].Reason).String(); got != "tool-call-unresolved" {
		t.Fatalf("decoded unresolved reason label = %q, want %q", got, "tool-call-unresolved")
	}
	if decoded.Contract != expectedInterpretedContract {
		t.Fatalf("decoded contract = %q, want %q", decoded.Contract, expectedInterpretedContract)
	}

	wantDigest := sha256.Sum256(effect.Payload)
	if !bytes.Equal(effect.ContentDigest, wantDigest[:]) {
		t.Fatalf("effect digest = %x, want SHA-256 of returned payload %x", effect.ContentDigest, wantDigest)
	}
}

func TestZeroRecordEffectIsSafe(t *testing.T) {
	t.Parallel()

	if got := (Record{}).Effect(); !reflect.DeepEqual(got, provenance.Effect{}) {
		t.Fatalf("zero record effect = %#v, want zero effect", got)
	}
}

func TestDecodeInterpretedStrictCanonicalEvidence(t *testing.T) {
	t.Parallel()
	contract := mustClaudeLifecycleContract(t)
	record, err := NewInterpreted(mustSessionStartL2(t, "session-1"), contract, metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	effect := record.Effect()
	decoded, err := DecodeInterpretedV2(model.InterpretationID(12), model.OccurrenceID(11), effect.Payload)
	if err != nil {
		t.Fatalf("DecodeInterpretedV2: %v", err)
	}
	if decoded.JournalID() != 12 || decoded.OccurrenceID.JournalID() != 11 || decoded.Semantic() != record.Semantic() || decoded.Contract() != record.Contract() {
		t.Fatalf("decoded=%#v", decoded)
	}
	manifest, ok := decoded.Metamodel()
	if !ok || manifest != metamodel.Active() {
		t.Fatalf("decoded codebook = %#v ok=%v, want the active coordinate %#v", manifest, ok, metamodel.Active())
	}
	// A committed interpreted.v1 record carries no codebook, so the v1 decoder
	// must NOT accept the v2 payload (its codebook member is an unknown field).
	if _, err := DecodeInterpreted(12, 11, effect.Payload); err == nil {
		t.Fatal("interpreted.v1 decoder accepted an interpreted.v2 payload")
	}
	needle := []byte(`"contract":"` + expectedInterpretedContract + `"`)
	duplicate := bytes.Replace(effect.Payload, needle, []byte(`"contract":"forged","contract":"`+expectedInterpretedContract+`"`), 1)
	for _, invalid := range [][]byte{append(append([]byte(nil), effect.Payload...), []byte(` {}`)...), duplicate} {
		if _, err := DecodeInterpretedV2(12, 11, invalid); err == nil {
			t.Fatalf("accepted noncanonical payload %s", invalid)
		}
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

type interpretedEffectPayloadOracle struct {
	Semantic        uint8                             `json:"semantic"`
	Identities      []interpretedIdentityOracle       `json:"identities"`
	UnresolvedFacts []interpretedUnresolvedFactOracle `json:"unresolved_facts"`
	Contract        string                            `json:"contract"`
	Metamodel       interpretedMetamodelOracle        `json:"manifest"`
}

type interpretedMetamodelOracle struct {
	ID      string `json:"id"`
	Version uint32 `json:"version"`
	Content string `json:"content"`
}

type interpretedIdentityOracle struct {
	Kind  uint8  `json:"kind"`
	Value string `json:"value"`
}

type interpretedUnresolvedFactOracle struct {
	Reason uint8 `json:"reason"`
}

func decodeInterpretedEffectPayload(payload []byte) (interpretedEffectPayloadOracle, error) {
	var decoded interpretedEffectPayloadOracle
	if err := ir.StrictJSONWithPresence(payload, []string{"semantic", "identities", "unresolved_facts", "contract", "manifest"}, &decoded); err != nil {
		return interpretedEffectPayloadOracle{}, err
	}
	return decoded, nil
}

func mustDecodeInterpretedEffectPayload(t *testing.T, payload []byte) interpretedEffectPayloadOracle {
	t.Helper()
	decoded, err := decodeInterpretedEffectPayload(payload)
	if err != nil {
		t.Fatalf("decode interpreted effect payload: %v", err)
	}
	return decoded
}

func assertInterpretedEffectEnvelope(t *testing.T, effect provenance.Effect) {
	t.Helper()
	if effect.Sort != provenance.EffectEvidence {
		t.Fatalf("effect sort = %v, want %v", effect.Sort, provenance.EffectEvidence)
	}
	if effect.ResultSlot != expectedInterpretedResultSlot {
		t.Fatalf("effect result slot = %q, want %q", effect.ResultSlot, expectedInterpretedResultSlot)
	}
	if effect.EvidenceKind != expectedInterpretedEvidenceKind {
		t.Fatalf("effect evidence kind = %q, want %q", effect.EvidenceKind, expectedInterpretedEvidenceKind)
	}
}

func assertActionableValidationError(t *testing.T, err error, wantWhat string, wantWhatFragments ...string) {
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
	if !strings.Contains(structured.What, wantWhat) {
		t.Fatalf("validation error What = %q, want substring %q", structured.What, wantWhat)
	}
	for _, fragment := range wantWhatFragments {
		if !strings.Contains(structured.What, fragment) {
			t.Fatalf("validation error What = %q, want contract fragment %q", structured.What, fragment)
		}
	}
}

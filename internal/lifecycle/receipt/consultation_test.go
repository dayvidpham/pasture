package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

type jsonPort struct {
	raw   []byte
	valid bool
	err   error
}
type nilJSONPort struct{}

func (*nilJSONPort) MarshalJSON() ([]byte, error)  { return []byte(`{"ok":true}`), nil }
func (*nilJSONPort) IsValid() bool                 { return true }
func (*nilJSONPort) ConsultationLegalized()        {}
func (*nilJSONPort) ConsultationResponse()         {}
func (*nilJSONPort) Value() (waist.Decision, bool) { return waist.Decision{}, false }

func (p jsonPort) MarshalJSON() ([]byte, error) { return append([]byte(nil), p.raw...), p.err }
func (p jsonPort) IsValid() bool                { return p.valid }
func (jsonPort) ConsultationLegalized()         {}
func (jsonPort) ConsultationResponse()          {}
func (p jsonPort) Value() (waist.Decision, bool) {
	decision, err := waist.NewDecision(waist.DecisionProceed, waist.ReasonLegal)
	return decision, p.valid && err == nil
}

func TestNewConsultationCanonicalAndOwned(t *testing.T) {
	t.Parallel()
	interpreted, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	legalized := jsonPort{raw: []byte(`{"rule":"allow"}`), valid: true}
	response := jsonPort{raw: []byte(`{"decision":"proceed"}`), valid: true}
	record, err := NewConsultation(interpreted, legalized, response)
	if err != nil {
		t.Fatal(err)
	}
	effect := record.Effect()
	if effect.EvidenceKind != provenance.EvidenceKind("pasture.lifecycle.consultation.v2") {
		t.Fatalf("new consultation kind=%s, want consultation.v2", effect.EvidenceKind)
	}
	if !bytes.HasPrefix(effect.Payload, []byte(`{"decision":{"decision":"proceed","reason":"legal"},"interpreted":`)) {
		t.Fatalf("payload=%s", effect.Payload)
	}
	sum := sha256.Sum256(effect.Payload)
	if !bytes.Equal(sum[:], effect.ContentDigest) {
		t.Fatal("digest mismatch")
	}
	effect.Payload[0] = 'X'
	if record.Effect().Payload[0] != '{' {
		t.Fatal("returned payload aliases record")
	}
}

func TestNewConsultationRejectsInvalidPortsAndJSON(t *testing.T) {
	t.Parallel()
	gate, _ := NewInterpreted(mustPostToolBatchL2(t, "s"), mustClaudeLifecycleContract(t), metamodel.Active())
	observation, _ := NewInterpreted(mustSessionStartL2(t, "s"), mustClaudeLifecycleContract(t), metamodel.Active())
	valid := jsonPort{raw: []byte(`{"ok":true}`), valid: true}
	cases := []struct {
		name                string
		record              Record
		legalized, response jsonPort
	}{
		{"wrong semantic", observation, valid, valid}, {"invalid legalized", gate, jsonPort{raw: []byte(`{"ok":true}`)}, valid}, {"marshal failure", gate, jsonPort{valid: true, err: errors.New("boom")}, valid}, {"null", gate, jsonPort{raw: []byte(`null`), valid: true}, valid}, {"array", gate, jsonPort{raw: []byte(`[]`), valid: true}, valid}, {"duplicate", gate, jsonPort{raw: []byte(`{"a":1,"a":2}`), valid: true}, valid}, {"trailing", gate, jsonPort{raw: []byte(`{"a":1}{}`), valid: true}, valid},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewConsultation(tc.record, tc.legalized, tc.response); err == nil {
				t.Fatal("accepted invalid consultation")
			}
		})
	}
}

func TestNewConsultationValidatesBothPortsSymmetrically(t *testing.T) {
	t.Parallel()
	gate, _ := NewInterpreted(mustPostToolBatchL2(t, "s"), mustClaudeLifecycleContract(t), metamodel.Active())
	valid := jsonPort{raw: []byte(`{"ok":true}`), valid: true}
	invalid := []struct {
		name string
		port jsonPort
	}{{"invalid", jsonPort{raw: []byte(`{"ok":true}`)}}, {"marshal", jsonPort{valid: true, err: errors.New("boom")}}, {"null", jsonPort{raw: []byte(`null`), valid: true}}, {"scalar", jsonPort{raw: []byte(`1`), valid: true}}, {"array", jsonPort{raw: []byte(`[]`), valid: true}}, {"duplicate", jsonPort{raw: []byte(`{"a":1,"a":2}`), valid: true}}, {"trailing", jsonPort{raw: []byte(`{"a":1}{}`), valid: true}}, {"malformed", jsonPort{raw: []byte(`{"a":`), valid: true}}}
	for _, side := range []string{"legalized", "response"} {
		side := side
		for _, tc := range invalid {
			tc := tc
			t.Run(side+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				legalized, response := valid, valid
				if side == "legalized" {
					legalized = tc.port
				} else {
					response = tc.port
				}
				if _, err := NewConsultation(gate, legalized, response); err == nil {
					t.Fatal("accepted invalid port")
				}
			})
		}
	}
	var typedNil *nilJSONPort
	if _, err := NewConsultation(gate, typedNil, valid); err == nil {
		t.Fatal("accepted typed-nil legalized port")
	}
	if _, err := NewConsultation(gate, valid, typedNil); err == nil {
		t.Fatal("accepted typed-nil response port")
	}
}

func TestNewConsultationPayloadBound(t *testing.T) {
	t.Parallel()
	gate, _ := NewInterpreted(mustPostToolBatchL2(t, "s"), mustClaudeLifecycleContract(t), metamodel.Active())
	response := jsonPort{raw: []byte(`{}`), valid: true}
	base := jsonPort{raw: []byte(`{"v":""}`), valid: true}
	baseRecord, err := NewConsultation(gate, base, response)
	if err != nil {
		t.Fatal(err)
	}
	baseLen := len(baseRecord.Effect().Payload)
	fill := model.MaxNativePayloadBytes - baseLen
	atBound := jsonPort{raw: []byte(`{"v":"` + strings.Repeat("x", fill) + `"}`), valid: true}
	record, err := NewConsultation(gate, atBound, response)
	if err != nil {
		t.Fatalf("exact bound rejected: %v", err)
	}
	if len(record.Effect().Payload) != model.MaxNativePayloadBytes {
		t.Fatalf("payload=%d want=%d", len(record.Effect().Payload), model.MaxNativePayloadBytes)
	}
	over := jsonPort{raw: []byte(`{"v":"` + strings.Repeat("x", fill+1) + `"}`), valid: true}
	if _, err := NewConsultation(gate, over, response); err == nil {
		t.Fatal("oversized consultation accepted")
	}
}

func TestReceiveRejectsInvalidPairMatrixBeforeWrites(t *testing.T) {
	t.Parallel()
	first, _ := NewInterpreted(mustPostToolBatchL2(t, "one"), mustClaudeLifecycleContract(t), metamodel.Active())
	second, _ := NewInterpreted(mustPostToolBatchL2(t, "two"), mustClaudeLifecycleContract(t), metamodel.Active())
	valid := jsonPort{raw: []byte(`{"ok":true}`), valid: true}
	consultFirst, _ := NewConsultation(first, valid, valid)
	consultSecond, _ := NewConsultation(second, valid, valid)
	i1, c1, c2 := first.Effect(), consultFirst.Effect(), consultSecond.Effect()
	malformed := i1
	malformed.Payload = []byte(`{"semantic":2}`)
	sum := sha256.Sum256(malformed.Payload)
	malformed.ContentDigest = sum[:]
	cases := []struct {
		name    string
		effects []provenance.Effect
	}{{"consultation without interpreted", []provenance.Effect{c1}}, {"duplicate interpreted", []provenance.Effect{i1, i1}}, {"reordered", []provenance.Effect{c1, i1}}, {"cross swapped", []provenance.Effect{i1, c2}}, {"malformed canonical bypass", []provenance.Effect{malformed}}, {"too many", []provenance.Effect{i1, c1, c1}}}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := []string{}
			inputs := []provenance.OperationInput{}
			clock := testClock{now: time.Unix(10, 0)}
			service := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls, inputs: &inputs}, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "invalid-pair"}}
			if _, err := service.Receive(context.Background(), mustDeliveryWarrant(), validDelivery(), tc.effects...); err == nil {
				t.Fatal("accepted invalid pair")
			}
			if len(calls) != 0 || len(inputs) != 0 {
				t.Fatalf("writes occurred calls=%v inputs=%d", calls, len(inputs))
			}
		})
	}
}

func TestConsultationV2RejectsMalformedDecisionVersionAndDigest(t *testing.T) {
	t.Parallel()
	interpreted, err := NewInterpreted(mustPostToolBatchL2(t, "v2-reader"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	port := jsonPort{raw: []byte(`{"ok":true}`), valid: true}
	record, err := NewConsultation(interpreted, port, port)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeConsultation(record.Effect(), interpreted.Effect())
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := decoded.Decision()
	if !ok || decision.Kind() != waist.DecisionProceed || decision.Reason() != waist.ReasonLegal {
		t.Fatal("current record lost its explicit evaluated decision")
	}

	for _, tc := range []struct {
		name string
		from string
		to   string
	}{
		{name: "missing reason", from: `,"reason":"legal"`, to: ""},
		{name: "fault as policy", from: `"legal"`, to: `"evaluation-fault"`},
		{name: "wrong pair", from: `"proceed"`, to: `"deny"`},
		{name: "null decision", from: `{"decision":"proceed","reason":"legal"}`, to: `null`},
		{name: "null reason", from: `"reason":"legal"`, to: `"reason":null`},
		{name: "duplicate reason", from: `"reason":"legal"`, to: `"reason":"legal","reason":"legal"`},
		{name: "unknown field", from: `"reason":"legal"`, to: `"reason":"legal","unknown":true`},
		{name: "wrong digest", from: digest.FromBytes(interpreted.Effect().Payload).String(), to: digest.FromString("different payload").String()},
		{name: "wrong slot", from: `"result_slot":"interpreted"`, to: `"result_slot":"other"`},
		{name: "null legalized", from: `"legalized":{"ok":true}`, to: `"legalized":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			effect := record.Effect()
			changed := strings.Replace(string(effect.Payload), tc.from, tc.to, 1)
			if changed == string(effect.Payload) {
				t.Fatal("attack did not change its intended field")
			}
			effect.Payload = []byte(changed)
			sum := sha256.Sum256(effect.Payload)
			effect.ContentDigest = sum[:]

			if _, err := DecodeConsultation(effect, interpreted.Effect()); err == nil {
				t.Fatal("malformed v2 accepted after recomputing outer hash")
			}
			if _, err := CanonicalizeLifecycleEffects([]provenance.Effect{interpreted.Effect(), effect}); err == nil {
				t.Fatal("canonicalization repaired a forged consultation")
			}
		})
	}
	for _, kind := range []provenance.EvidenceKind{consultationKindV1, "pasture.lifecycle.consultation.v3", ""} {
		effect := record.Effect()
		effect.EvidenceKind = kind
		if _, err := DecodeConsultation(effect, interpreted.Effect()); err == nil {
			t.Fatalf("v2 payload accepted under kind %q", kind)
		}
	}
	effect := record.Effect()
	effect.ContentDigest[0] ^= 1
	if _, err := DecodeConsultation(effect, interpreted.Effect()); err == nil {
		t.Fatal("changed outer hash accepted")
	}
}

func TestLegacyConsultationRemainsReadableAndRebindableWithoutInventedReason(t *testing.T) {
	t.Parallel()
	interpreted, err := NewInterpreted(mustPostToolBatchL2(t, "legacy-reader"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"legalized":{"ok":true},"response":{"decision":"proceed"},"interpreted":{"result_slot":"interpreted","content_digest":"` + digest.FromBytes(interpreted.Effect().Payload).String() + `"}}`)
	sum := sha256.Sum256(legacy)
	effect := provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    consultationSlot,
		EvidenceKind:  consultationKindV1,
		ContentDigest: sum[:],
		Payload:       legacy,
	}
	decoded, err := DecodeConsultation(effect, interpreted.Effect())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Effect().Payload, legacy) || !bytes.Equal(decoded.Effect().ContentDigest, sum[:]) {
		t.Fatal("legacy read rewrote historical bytes or digest")
	}
	if decoded.Effect().EvidenceKind != consultationKindV1 {
		t.Fatal("legacy read silently changed the evidence version")
	}
	if _, evaluated := decoded.Decision(); evaluated {
		t.Fatal("legacy host port manufactured a policy reason")
	}
	// Reproduce the old writer's producer-hash/stored-normalized-payload
	// distinction with the real public Provenance normalizer. Do not rewrite
	// either historical identity while reading it.
	normalizedInput, err := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{interpreted.Effect()}})
	if err != nil {
		t.Fatal(err)
	}
	oldInterpreted := normalizedInput.NormalizedEffects()[0]
	interpretedSum := sha256.Sum256(oldInterpreted.Payload)
	oldInterpreted.ContentDigest = interpretedSum[:]
	producer, err := rebindConsultationPayload(legacy, oldInterpreted.Payload, consultationKindV1)
	if err != nil {
		t.Fatal(err)
	}
	producerSum := sha256.Sum256(producer)
	oldEffect := effect
	oldEffect.Payload = producer
	oldEffect.ContentDigest = producerSum[:]
	normalizedRecord, err := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{oldEffect}})
	if err != nil {
		t.Fatal(err)
	}
	storedLegacy := normalizedRecord.NormalizedEffects()[0]
	if bytes.Equal(storedLegacy.Payload, producer) {
		t.Fatal("legacy proof failed to exercise distinct producer/stored encodings")
	}
	oldDecoded, err := DecodeConsultation(storedLegacy, oldInterpreted)
	if err != nil {
		t.Fatalf("historical normalized row rejected: %v", err)
	}
	if !bytes.Equal(oldDecoded.Effect().Payload, storedLegacy.Payload) || !bytes.Equal(oldDecoded.Effect().ContentDigest, producerSum[:]) {
		t.Fatal("legacy read rewrote the stored payload or producer digest")
	}
	if oldDecoded.Effect().EvidenceKind != consultationKindV1 {
		t.Fatal("normalized legacy read silently upgraded its version")
	}
	badHash := storedLegacy
	badHash.ContentDigest = append([]byte(nil), producerSum[:]...)
	badHash.ContentDigest[0] ^= 1
	if _, err := DecodeConsultation(badHash, oldInterpreted); err == nil {
		t.Fatal("legacy compatibility ignored a corrupt producer digest")
	}
	canonical, err := CanonicalizeLifecycleEffects([]provenance.Effect{interpreted.Effect(), effect})
	if err != nil {
		t.Fatal(err)
	}
	if canonical[1].EvidenceKind != consultationKindV1 {
		t.Fatal("rebind silently upgraded the legacy version")
	}
	if _, err := DecodeConsultation(canonical[1], canonical[0]); err != nil {
		t.Fatalf("legacy normalized/rebound pair is unreadable: %v", err)
	}
	if err := validateLifecycleExtras([]provenance.Effect{interpreted.Effect(), effect}); err == nil {
		t.Fatal("new writer accepted historical v1 instead of requiring v2")
	}
}

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
	"github.com/dayvidpham/provenance"
)

type jsonPort struct {
	raw   []byte
	valid bool
	err   error
}
type nilJSONPort struct{}

func (*nilJSONPort) MarshalJSON() ([]byte, error) { return []byte(`{"ok":true}`), nil }
func (*nilJSONPort) IsValid() bool                { return true }
func (*nilJSONPort) ConsultationLegalized()       {}
func (*nilJSONPort) ConsultationResponse()        {}

func (p jsonPort) MarshalJSON() ([]byte, error) { return append([]byte(nil), p.raw...), p.err }
func (p jsonPort) IsValid() bool                { return p.valid }
func (jsonPort) ConsultationLegalized()         {}
func (jsonPort) ConsultationResponse()          {}

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
	if !bytes.HasPrefix(effect.Payload, []byte(`{"legalized":{"rule":"allow"},"response":{"decision":"proceed"},"interpreted":`)) {
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
			service := Service{Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: contextJournal{calls: &calls, inputs: &inputs}, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "invalid-pair"}}
			if _, err := service.Receive(context.Background(), mustDeliveryWarrant(), validDelivery(), tc.effects...); err == nil {
				t.Fatal("accepted invalid pair")
			}
			if len(calls) != 0 || len(inputs) != 0 {
				t.Fatalf("writes occurred calls=%v inputs=%d", calls, len(inputs))
			}
		})
	}
}

package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
)

type jsonPort struct {
	raw   []byte
	valid bool
	err   error
}

func (p jsonPort) MarshalJSON() ([]byte, error) { return append([]byte(nil), p.raw...), p.err }
func (p jsonPort) IsValid() bool                { return p.valid }
func (jsonPort) ConsultationLegalized()         {}
func (jsonPort) ConsultationResponse()          {}

func TestNewConsultationCanonicalAndOwned(t *testing.T) {
	t.Parallel()
	interpreted, err := NewInterpreted(mustPostToolBatchL2(t, "session-1"), mustClaudeLifecycleContract(t))
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
	gate, _ := NewInterpreted(mustPostToolBatchL2(t, "s"), mustClaudeLifecycleContract(t))
	observation, _ := NewInterpreted(mustSessionStartL2(t, "s"), mustClaudeLifecycleContract(t))
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

func TestReceiveRejectsInvalidPairMatrixBeforeWrites(t *testing.T) {
	t.Parallel()
	first, _ := NewInterpreted(mustPostToolBatchL2(t, "one"), mustClaudeLifecycleContract(t))
	second, _ := NewInterpreted(mustPostToolBatchL2(t, "two"), mustClaudeLifecycleContract(t))
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
			if _, err := service.Receive(context.Background(), validDelivery(), tc.effects...); err == nil {
				t.Fatal("accepted invalid pair")
			}
			if len(calls) != 0 || len(inputs) != 0 {
				t.Fatalf("writes occurred calls=%v inputs=%d", calls, len(inputs))
			}
		})
	}
}

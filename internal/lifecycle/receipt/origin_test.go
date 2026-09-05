package receipt

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	origin "github.com/dayvidpham/pasture/internal/acceptance/origin"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// baselineUnsetOriginPayload is the FROZEN occurrence.v1 evidence payload the
// production encoder committed at baseline 8bfdf4c for validDelivery() — a
// native delivery carrying NO origin member, in the canonical key-sorted form
// the journal stores. It pins the SLICE-1 ZERO-diff guarantee: adding the
// origin carrier must leave the committed payload bytes byte-identical when
// origin is absent/unset, so pre-origin callers and golden native records stay
// unchanged. A change to the encoder (e.g. emitting an empty or defaulted
// origin member) fails this pin.
const baselineUnsetOriginPayload = `{"bindings":null,"body_digest":"sha256:f352f3001468c4e837240ca5714239468d614ec48ca47016bd69f2c64ad5fd8d","capture":1,"contract":"claude-code/2.1.210","envelope":{"HostVersion":"","Implementation":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Retention":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Runtime":{"Contract":"claude-code/2.1.210","Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}},"Schema":{"Definition":{"Content":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Definition":0,"Kind":0}}},"event":1}`

// receiveOnce delivers delivery through the in-memory service and returns the
// committed occurrence effect payload, mirroring the gate_test.go call-slice
// harness (gate/refusal-before-any-IO pattern): no test-only production path,
// the payload is the exact canonical bytes the journal appended.
func receiveOnce(t *testing.T, delivery Delivery) []byte {
	t.Helper()
	calls := []string{}
	inputs := []provenance.OperationInput{}
	clock := testClock{now: time.Unix(10, 0)}
	j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 41}}}}
	s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "origin-carrier"}}
	if _, err := s.Receive(context.Background(), mustDeliveryWarrant(), delivery); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(inputs) != 1 || len(inputs[0].Effects) != 1 {
		t.Fatalf("effects = %#v, want exactly one occurrence effect", inputs)
	}
	effect := inputs[0].Effects[0]
	if effect.EvidenceKind != expectedOccurrenceEvidenceKind {
		t.Fatalf("evidence kind = %s, want %s (occurrence.v1 reused, no new raw kind)", effect.EvidenceKind, expectedOccurrenceEvidenceKind)
	}
	return append([]byte(nil), effect.Payload...)
}

// TestReceiveUnsetOriginPayloadByteIdenticalToBaseline pins the ZERO-diff
// guarantee: a delivery that does not set Origin (every pre-origin caller and
// the golden native path) must produce committed payload bytes identical to the
// frozen baseline 8bfdf4c occurrence.v1 encoding — the origin member is
// omitted, not emitted empty or defaulted. FAILS if the encoder changes shape.
func TestReceiveUnsetOriginPayloadByteIdenticalToBaseline(t *testing.T) {
	t.Parallel()
	payload := receiveOnce(t, validDelivery())
	if string(payload) != baselineUnsetOriginPayload {
		t.Fatalf("unset-origin occurrence payload drifted from the frozen baseline:\n got: %s\nwant: %s", payload, baselineUnsetOriginPayload)
	}
	if bytes.Contains(payload, []byte("origin")) {
		t.Fatalf("unset-origin payload contains an origin member:\n%s", payload)
	}
}

// TestReceiveRawOriginRidesPayloadAndEnvelope proves the ratified UAT-Q4
// carriage: origin rides BOTH the occurrence envelope AND the receipt payload.
// The committed payload carries "origin":"raw" in the payload member and inside
// the envelope member, while the evidence kind stays occurrence.v1. The payload
// also decodes tolerantly through the pre-origin shape (unknown members
// ignored) and strictly through the origin-aware shape.
func TestReceiveRawOriginRidesPayloadAndEnvelope(t *testing.T) {
	t.Parallel()
	delivery := validDelivery()
	delivery.Origin = origin.OriginRaw
	delivery.Envelope.Origin = origin.OriginRaw
	payload := receiveOnce(t, delivery)

	if !bytes.Contains(payload, []byte(`"origin":"raw"`)) {
		t.Fatalf("raw-origin payload does not carry the origin member:\n%s", payload)
	}
	if strings.Count(string(payload), `"origin":"raw"`) != 2 {
		t.Fatalf("raw-origin payload carries origin %d times, want payload + envelope (2):\n%s", strings.Count(string(payload), `"origin":"raw"`), payload)
	}

	// Tolerant decode: the pre-origin reader shape (no origin members) must
	// still decode a raw-origin payload, ignoring the unknown members.
	var legacy struct {
		Contract string                   `json:"contract"`
		Event    model.ContractEventKind  `json:"event"`
		Capture  model.CaptureDisposition `json:"capture"`
	}
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatalf("pre-origin tolerant decode rejected a raw-origin payload: %v", err)
	}
	if legacy.Event != 1 || legacy.Capture != model.CaptureValid {
		t.Fatalf("tolerant decode fields = %#v, want event 1 capture valid", legacy)
	}

	// Strict decode: the origin-aware shape reads both carriers back.
	var decoded struct {
		Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
		Origin   origin.CaptureOrigin        `json:"origin"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("origin-aware decode rejected the raw-origin payload: %v", err)
	}
	if decoded.Origin != origin.OriginRaw {
		t.Fatalf("payload origin = %q, want %q", decoded.Origin, origin.OriginRaw)
	}
	if decoded.Envelope.Origin != origin.OriginRaw {
		t.Fatalf("envelope origin = %q, want %q", decoded.Envelope.Origin, origin.OriginRaw)
	}
}

// TestReceiveOriginPayloadDeterministic proves the origin carrier is
// idempotent and race-safe at the commit surface: identical origin-stamped
// deliveries commit byte-identical payloads, sequentially and from racing
// goroutines (the definition_test.go idempotence/race pattern). The encode has
// no shared state, so every racing receive resolves the same evidence bytes.
func TestReceiveOriginPayloadDeterministic(t *testing.T) {
	t.Parallel()
	delivery := validDelivery()
	delivery.Origin = origin.OriginRaw
	delivery.Envelope.Origin = origin.OriginRaw

	sequential := receiveOnce(t, delivery)
	if again := receiveOnce(t, delivery); !bytes.Equal(sequential, again) {
		t.Fatalf("identical deliveries produced different payloads:\n%s\n%s", sequential, again)
	}

	const goroutines = 8
	payloads := make([][]byte, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			calls := []string{}
			inputs := []provenance.OperationInput{}
			clock := testClock{now: time.Unix(10, 0)}
			j := contextJournal{calls: &calls, inputs: &inputs, result: provenance.CommittedResult{ResultSlots: []provenance.ResultSlotBinding{{Slot: expectedOccurrenceResultSlot, ProducedJournalID: 41}}}}
			s := Service{Window: time.Second, Blobs: orderedBlobs{calls: &calls}, Appender: JournalAppender{Journal: j, Clock: clock, Deadline: time.Second}, Identity: testIdentity{}, Clock: clock, Operations: testOperations{id: "origin-race"}}
			_, errs[i] = s.Receive(context.Background(), mustDeliveryWarrant(), delivery)
			if len(inputs) == 1 && len(inputs[0].Effects) == 1 {
				payloads[i] = append([]byte(nil), inputs[0].Effects[0].Payload...)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d Receive returned an error: %v", i, err)
		}
		if !bytes.Equal(payloads[i], sequential) {
			t.Fatalf("goroutine %d payload differs from the sequential payload", i)
		}
	}
}

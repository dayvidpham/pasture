package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
)

const consultationSlot = provenance.ResultSlotID("consultation")
const consultationKindV1 = provenance.EvidenceKind("pasture.lifecycle.consultation.v1")
const consultationKind = provenance.EvidenceKind("pasture.lifecycle.consultation.v2")

// CurrentConsultationEvidenceKind identifies newly constructed consultations.
// Readers may also support historical versions of this evidence kind.
func CurrentConsultationEvidenceKind() provenance.EvidenceKind {
	return consultationKind
}

type ConsultationRecord struct {
	payload  []byte
	kind     provenance.EvidenceKind
	decision waist.Decision
	// Historical journal rows can retain a digest of the producer encoding
	// while their payload is Provenance-normalized. Preserve that identity.
	contentDigest []byte
	constructed   bool
}

func NewConsultation(interpreted Record, legalized waist.ConsultationLegalized, response waist.ConsultationResponse) (ConsultationRecord, error) {
	if !interpreted.IsValid() || interpreted.Semantic() != runtime.SemanticGateConsultation || nilPort(legalized) || nilPort(response) || !legalized.IsValid() || !response.IsValid() {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation: interpreted gate record and valid non-nil legalized/response ports are required")
	}
	l, err := legalized.MarshalJSON()
	if err != nil {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation: marshal legalized value: %w", err)
	}
	r, err := response.MarshalJSON()
	if err != nil {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation: marshal host response: %w", err)
	}
	if err := validateJSONObject(l); err != nil {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation legalized value: %w", err)
	}
	if err := validateJSONObject(r); err != nil {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation response: %w", err)
	}
	decision, evaluated := response.Value()
	if !evaluated || !decision.IsValid() {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation.v2: response is not a constructor-validated evaluated decision; no consultation was produced; use the separate evaluation-fault path instead of recording a fault as policy")
	}
	ie := interpreted.Effect()
	payload, err := marshalConsultationV2(l, decision, ie.Payload)
	if err != nil {
		return ConsultationRecord{}, err
	}
	if len(payload) > model.MaxNativePayloadBytes {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation: payload is %d bytes, above %d-byte bound", len(payload), model.MaxNativePayloadBytes)
	}
	return ConsultationRecord{payload: append([]byte(nil), payload...), kind: consultationKind, decision: decision, constructed: true}, nil
}
func nilPort(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}
func validateJSONObject(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return fmt.Errorf("expected one non-null JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		k, _ := dec.Token()
		key := k.(string)
		if seen[key] {
			return fmt.Errorf("duplicate JSON member %q", key)
		}
		seen[key] = true
		var value any
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	if _, err = dec.Token(); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing JSON value")
	}
	var extra any
	if dec.Decode(&extra) == nil {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
func (r ConsultationRecord) IsValid() bool { return r.constructed }
func (r ConsultationRecord) Effect() provenance.Effect {
	if !r.IsValid() {
		return provenance.Effect{}
	}
	sum := sha256.Sum256(r.payload)
	contentDigest := sum[:]
	if r.contentDigest != nil {
		contentDigest = r.contentDigest
	}
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: consultationSlot, EvidenceKind: r.kind, ContentDigest: append([]byte(nil), contentDigest...), Payload: append(json.RawMessage(nil), r.payload...)}
}

// Decision is absent for historical v1 records: a legacy host response does
// not prove a policy reason. No reason is inferred during readback.
func (r ConsultationRecord) Decision() (waist.Decision, bool) {
	return r.decision, r.IsValid() && r.kind == consultationKind && r.decision.IsValid()
}

type consultationReference struct {
	ResultSlot    string `json:"result_slot"`
	ContentDigest string `json:"content_digest"`
}

type consultationWireV2 struct {
	Decision    waist.Decision        `json:"decision"`
	Interpreted consultationReference `json:"interpreted"`
	Legalized   json.RawMessage       `json:"legalized"`
}

func marshalConsultationV2(legalized []byte, decision waist.Decision, interpreted []byte) ([]byte, error) {
	return json.Marshal(consultationWireV2{
		Decision:    decision,
		Interpreted: consultationReference{ResultSlot: string(interpretedSlot), ContentDigest: digest.FromBytes(interpreted).String()},
		Legalized:   legalized,
	})
}

// DecodeConsultation validates a versioned durable pair without rewriting its
// historical bytes. The returned record owns its bytes; Effect returns copies.
func DecodeConsultation(effect, interpreted provenance.Effect) (ConsultationRecord, error) {
	if effect.Sort != provenance.EffectEvidence || effect.ResultSlot != consultationSlot {
		return ConsultationRecord{}, fmt.Errorf("decode lifecycle consultation: effect sort or slot is invalid; no record was accepted; pass the exact journal evidence")
	}
	if interpreted.Sort != provenance.EffectEvidence || interpreted.ResultSlot != interpretedSlot || !effectDigestValid(interpreted) {
		return ConsultationRecord{}, fmt.Errorf("decode lifecycle consultation: referenced interpreted effect is invalid; pass the exact immediately preceding interpreted evidence")
	}
	var err error
	var interpretedRecord model.InterpretedRecord
	switch interpreted.EvidenceKind {
	case interpretedKind:
		interpretedRecord, err = DecodeInterpreted(1, 1, interpreted.Payload)
	case interpretedKindV2:
		interpretedRecord, err = DecodeInterpretedV2(1, 1, interpreted.Payload)
	default:
		err = fmt.Errorf("unsupported interpreted kind %q", interpreted.EvidenceKind)
	}
	if err != nil {
		return ConsultationRecord{}, fmt.Errorf("decode lifecycle consultation interpreted reference: %w", err)
	}
	if interpretedRecord.Semantic() != runtime.SemanticGateConsultation {
		return ConsultationRecord{}, fmt.Errorf("decode lifecycle consultation: interpreted reference is not a gate; no decision can be attached to an observation or human-response record")
	}
	if err := validateConsultationPayload(effect.Payload, interpreted.Payload, effect.EvidenceKind); err != nil {
		return ConsultationRecord{}, err
	}
	if !effectDigestValid(effect) {
		// The historical v1 writer normalized, rebound into its producer field
		// order, then hashed. Provenance normalized again at Apply. Reconstruct
		// exactly that documented producer representation, not arbitrary JSON.
		if effect.EvidenceKind != consultationKindV1 {
			return ConsultationRecord{}, fmt.Errorf("decode consultation.v2: content digest does not authenticate the exact payload")
		}
		producer, err := rebindConsultationPayload(effect.Payload, interpreted.Payload, consultationKindV1)
		if err != nil {
			return ConsultationRecord{}, err
		}
		sum := sha256.Sum256(producer)
		if !bytes.Equal(sum[:], effect.ContentDigest) {
			return ConsultationRecord{}, fmt.Errorf("decode consultation.v1: digest matches neither payload nor historical producer encoding; no record accepted")
		}
	}
	record := ConsultationRecord{payload: append([]byte(nil), effect.Payload...), contentDigest: append([]byte(nil), effect.ContentDigest...), kind: effect.EvidenceKind, constructed: true}
	if effect.EvidenceKind == consultationKind {
		var wire consultationWireV2
		if err := json.Unmarshal(effect.Payload, &wire); err != nil {
			return ConsultationRecord{}, err
		}
		record.decision = wire.Decision
	}
	return record, nil
}

// ReplaceConsultationDecision is the validated decision-input boundary for a
// previously derived current-version gate pair. It changes neither interpreted
// evidence nor legalization. Observations and legacy writes are refused.
func ReplaceConsultationDecision(effects []provenance.Effect, decision waist.Decision) ([]provenance.Effect, error) {
	if len(effects) != 2 || !decision.IsValid() {
		return nil, fmt.Errorf("replace lifecycle consultation decision: expected a valid decision and one interpreted/consultation gate pair; no effects changed; derive and validate the gate before supplying a decision")
	}
	if err := validateLifecycleExtras(effects); err != nil {
		return nil, err
	}
	var wire consultationWireV2
	if err := json.Unmarshal(effects[1].Payload, &wire); err != nil {
		return nil, err
	}
	payload, err := marshalConsultationV2(wire.Legalized, decision, effects[0].Payload)
	if err != nil {
		return nil, err
	}
	out := append([]provenance.Effect(nil), effects...)
	record := ConsultationRecord{payload: payload, kind: consultationKind, decision: decision, constructed: true}
	out[1] = record.Effect()
	return out, nil
}

type consultationWire struct {
	Legalized   json.RawMessage `json:"legalized"`
	Response    json.RawMessage `json:"response"`
	Interpreted struct {
		ResultSlot    string `json:"result_slot"`
		ContentDigest string `json:"content_digest"`
	} `json:"interpreted"`
}

func validateConsultationPayload(payload, interpretedPayload []byte, kinds ...provenance.EvidenceKind) error {
	kind := consultationKind
	if len(kinds) > 1 {
		return fmt.Errorf("decode lifecycle consultation: supply exactly one evidence kind")
	}
	if len(kinds) == 1 {
		kind = kinds[0]
	}
	if kind == consultationKindV1 {
		return validateConsultationV1Payload(payload, interpretedPayload)
	}
	if kind != consultationKind {
		return fmt.Errorf("decode lifecycle consultation: unsupported evidence kind %q; no record accepted; use a reader that supports this version", kind)
	}
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return err
	}
	var wire consultationWireV2
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	if !wire.Decision.IsValid() {
		return fmt.Errorf("decode consultation.v2: missing or invalid evaluated decision; supply explicit validated kind and reason")
	}
	if err := validateJSONObject(wire.Legalized); err != nil {
		return err
	}
	if wire.Interpreted.ResultSlot != string(interpretedSlot) || wire.Interpreted.ContentDigest != digest.FromBytes(interpretedPayload).String() {
		return fmt.Errorf("decode consultation.v2: interpreted slot or exact payload digest does not match the preceding effect")
	}
	canonical, err := marshalConsultationV2(wire.Legalized, wire.Decision, interpretedPayload)
	return requireCanonicalMatch(canonical, err, payload, kind)
}

func validateConsultationV1Payload(payload, interpretedPayload []byte) error {
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return fmt.Errorf("decode lifecycle consultation: %w", err)
	}
	var wire consultationWire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("decode lifecycle consultation: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode lifecycle consultation: trailing JSON value")
	}
	if err := validateJSONObject(wire.Legalized); err != nil {
		return fmt.Errorf("decode lifecycle consultation legalized value: %w", err)
	}
	if err := validateJSONObject(wire.Response); err != nil {
		return fmt.Errorf("decode lifecycle consultation response: %w", err)
	}
	if wire.Interpreted.ResultSlot != string(interpretedSlot) || wire.Interpreted.ContentDigest != digest.FromBytes(interpretedPayload).String() {
		return fmt.Errorf("decode lifecycle consultation: interpreted slot or exact payload digest does not match the preceding interpreted effect")
	}
	canonical := []byte(`{"legalized":`)
	canonical = append(canonical, wire.Legalized...)
	canonical = append(canonical, `,"response":`...)
	canonical = append(canonical, wire.Response...)
	canonical = append(canonical, `,"interpreted":{"result_slot":"interpreted","content_digest":`...)
	quoted, _ := json.Marshal(wire.Interpreted.ContentDigest)
	canonical = append(canonical, quoted...)
	canonical = append(canonical, '}', '}')
	return requireCanonicalMatch(canonical, nil, payload, consultationKindV1)
}

func rebindConsultationPayload(payload, interpretedPayload []byte, kinds ...provenance.EvidenceKind) ([]byte, error) {
	kind := consultationKind
	if len(kinds) > 1 {
		return nil, fmt.Errorf("rebind consultation: supply exactly one evidence kind")
	}
	if len(kinds) == 1 {
		kind = kinds[0]
	}
	if kind == consultationKind {
		if err := rejectDuplicateJSONMembers(payload); err != nil {
			return nil, err
		}
		var wire consultationWireV2
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&wire); err != nil {
			return nil, err
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, err
		}
		if !wire.Decision.IsValid() {
			return nil, fmt.Errorf("rebind consultation.v2: invalid decision")
		}
		if err := validateJSONObject(wire.Legalized); err != nil {
			return nil, err
		}
		return marshalConsultationV2(wire.Legalized, wire.Decision, interpretedPayload)
	}
	if kind != consultationKindV1 {
		return nil, fmt.Errorf("rebind consultation: unsupported kind %q", kind)
	}
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return nil, err
	}
	var wire consultationWire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := validateJSONObject(wire.Legalized); err != nil {
		return nil, err
	}
	if err := validateJSONObject(wire.Response); err != nil {
		return nil, err
	}
	wire.Interpreted.ResultSlot = string(interpretedSlot)
	wire.Interpreted.ContentDigest = digest.FromBytes(interpretedPayload).String()
	out := []byte(`{"legalized":`)
	out = append(out, wire.Legalized...)
	out = append(out, `,"response":`...)
	out = append(out, wire.Response...)
	out = append(out, `,"interpreted":{"result_slot":"interpreted","content_digest":`...)
	quoted, _ := json.Marshal(wire.Interpreted.ContentDigest)
	out = append(out, quoted...)
	out = append(out, '}', '}')
	return out, nil
}

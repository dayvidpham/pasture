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
const consultationKind = provenance.EvidenceKind("pasture.lifecycle.consultation.v1")

type ConsultationRecord struct {
	payload     []byte
	constructed bool
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
	ie := interpreted.Effect()
	ref := digest.FromBytes(ie.Payload)
	payload := []byte(`{"legalized":`)
	payload = append(payload, l...)
	payload = append(payload, `,"response":`...)
	payload = append(payload, r...)
	payload = append(payload, `,"interpreted":{"result_slot":"interpreted","content_digest":`...)
	quoted, _ := json.Marshal(ref.String())
	payload = append(payload, quoted...)
	payload = append(payload, '}', '}')
	if len(payload) > model.MaxNativePayloadBytes {
		return ConsultationRecord{}, fmt.Errorf("construct lifecycle consultation: payload is %d bytes, above %d-byte bound", len(payload), model.MaxNativePayloadBytes)
	}
	return ConsultationRecord{payload: append([]byte(nil), payload...), constructed: true}, nil
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
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: consultationSlot, EvidenceKind: consultationKind, ContentDigest: append([]byte(nil), sum[:]...), Payload: append(json.RawMessage(nil), r.payload...)}
}

type consultationWire struct {
	Legalized   json.RawMessage `json:"legalized"`
	Response    json.RawMessage `json:"response"`
	Interpreted struct {
		ResultSlot    string `json:"result_slot"`
		ContentDigest string `json:"content_digest"`
	} `json:"interpreted"`
}

func validateConsultationPayload(payload, interpretedPayload []byte) error {
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
	if !bytes.Equal(canonical, payload) {
		return fmt.Errorf("decode lifecycle consultation: payload is not canonical compact field-ordered JSON")
	}
	return nil
}

func rebindConsultationPayload(payload, interpretedPayload []byte) ([]byte, error) {
	var wire consultationWire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
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

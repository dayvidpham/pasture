// Package claude captures Claude Code hook payloads without allowing host
// semantics to leak into target-neutral lifecycle packages.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

type Capture struct {
	Digest      digest.Digest
	Disposition model.CaptureDisposition
	Delivery    receipt.Delivery
}

// Parse classifies raw bytes against a trusted generated registration. The
// digest and defensive body copy are made before any decode attempt.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	body := append([]byte(nil), raw...)
	manifest := registration.ClaudeCode2_1_210()
	envelope.Runtime.Contract = manifest.Contract
	result := Capture{Digest: digest.FromBytes(body), Disposition: model.CaptureValid}
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: body}
	result.Delivery.Envelope.HostVersion = observedVersion
	if !utf8.Valid(body) {
		result.Disposition = model.CaptureInvalidUTF8
		result.Delivery.Capture = result.Disposition
		return result
	}
	members, duplicate, err := strictMembers(body)
	if duplicate {
		result.Disposition = model.CaptureDuplicateField
	} else if err != nil {
		result.Disposition = model.CaptureMalformed
	} else {
		result.Disposition, result.Delivery.Bindings = validateMembers(members, event)
	}
	result.Delivery.Capture = result.Disposition
	return result
}

func strictMembers(raw []byte) (map[string]json.RawMessage, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false, fmt.Errorf("payload is not a JSON object: %w", err)
	}
	members := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false, fmt.Errorf("object member name is not a string")
		}
		if _, exists := members[key]; exists {
			return nil, true, nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false, err
		}
		members[key] = append(json.RawMessage(nil), value...)
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false, fmt.Errorf("payload object is not terminated: %w", err)
	}
	if token, err = decoder.Token(); err != io.EOF {
		return nil, false, fmt.Errorf("payload has trailing JSON value or bytes")
	}
	return members, false, nil
}

func validateMembers(members map[string]json.RawMessage, event registration.Event) (model.CaptureDisposition, []model.NativeBinding) {
	allowed := make(map[string]struct{}, len(event.AllowedFields))
	for _, field := range event.AllowedFields {
		allowed[fieldNames[field]] = struct{}{}
	}
	for name := range members {
		if _, ok := allowed[name]; !ok {
			return model.CaptureUnsupportedSchema, nil
		}
	}
	var reported string
	if raw, ok := members["hook_event_name"]; !ok || json.Unmarshal(raw, &reported) != nil || reported != event.NativeName {
		return model.CaptureEventMismatch, nil
	}
	bindings := make([]model.NativeBinding, 0, len(event.Identities))
	for _, identity := range event.Identities {
		name := fieldNames[identity.Field]
		raw, present := members[name]
		if !present {
			if identity.Required {
				return model.CaptureUnsupportedSchema, nil
			}
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > 512 {
			return model.CaptureUnsupportedSchema, nil
		}
		bindings = append(bindings, model.NativeBinding{Kind: identity.Binding, NativeName: name, Value: value})
	}
	return model.CaptureValid, bindings
}

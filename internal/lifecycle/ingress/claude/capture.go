// Package claude captures Claude Code hook payloads without allowing host
// semantics to leak into target-neutral lifecycle packages.
package claude

import (
	"encoding/json"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
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
// shared refusals run first, through ingress.Validate, so the digest and the
// defensive body copy exist before any decode attempt and a malformed payload
// is refused with the same disposition on every harness. What follows is
// Claude-specific: the member set is validated against the fields the
// registration allows, and the declared identities are bound by exact name.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	validation := ingress.Validate(raw)
	manifest := registration.ClaudeCode2_1_261()
	envelope.Runtime.Contract = manifest.Contract
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}
	result.Delivery.Envelope.HostVersion = observedVersion
	if validation.Disposition == model.CaptureValid {
		result.Disposition, result.Delivery.Bindings = validateMembers(validation.Members, event)
	}
	result.Delivery.Capture = result.Disposition
	return result
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

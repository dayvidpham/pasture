// Package claude captures Claude Code hook payloads without allowing host
// semantics to leak into target-neutral lifecycle packages.
package claude

import (
	"bytes"
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
	Cause       model.CaptureCause
	Delivery    receipt.Delivery
}

// Parse classifies raw bytes against a trusted generated registration. The
// shared refusals run first, through ingress.Validate, so the digest and the
// defensive body copy exist before any decode attempt and a malformed payload
// is refused with the same disposition on every harness. What follows is
// Claude-specific: the event claim and declared identities are checked by
// exact name. Extra members remain raw evidence and never become bindings.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	validation := ingress.Validate(raw)
	manifest := registration.ClaudeCode2_1_261()
	envelope.Runtime.Contract = manifest.Contract
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Cause = model.JSONCaptureCause(validation.Body, validation.Disposition)
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}
	result.Delivery.Envelope.HostVersion = observedVersion
	if validation.Disposition == model.CaptureValid {
		result.Disposition, result.Delivery.Bindings, result.Cause = validateMembers(validation.Members, event)
	}
	result.Delivery.Capture = result.Disposition
	return result
}

// Validate the event claim, then identities in registration order. Multiple
// defects select the same cause regardless of map iteration or host member
// order. AllowedFields describes the reviewed catalogue, not a runtime
// whole-object allow-list; compatible hosts may add unrelated raw evidence.
func validateMembers(members map[string]json.RawMessage, event registration.Event) (model.CaptureDisposition, []model.NativeBinding, model.CaptureCause) {
	refuse := func(kind model.CaptureCauseKind, name string, required model.JSONKind, disposition model.CaptureDisposition) (model.CaptureDisposition, []model.NativeBinding, model.CaptureCause) {
		return disposition, nil, model.NewCaptureCause(kind, name, required, disposition)
	}
	var reported string
	raw, present := members["hook_event_name"]
	if !present {
		return refuse(model.CauseMissingMember, "hook_event_name", model.JSONString, model.CaptureEventMismatch)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &reported) != nil {
		return refuse(model.CauseWrongKind, "hook_event_name", model.JSONString, model.CaptureEventMismatch)
	}
	if reported != event.NativeName {
		return refuse(model.CauseEventMismatch, "hook_event_name", model.JSONString, model.CaptureEventMismatch)
	}
	bindings := make([]model.NativeBinding, 0, len(event.Identities))
	for _, identity := range event.Identities {
		name := fieldNames[identity.Field]
		raw, present := members[name]
		if !present {
			if identity.Required {
				return refuse(model.CauseMissingMember, name, model.JSONString, model.CaptureUnsupportedSchema)
			}
			continue
		}
		var value string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return refuse(model.CauseWrongKind, name, model.JSONString, model.CaptureUnsupportedSchema)
		}
		if value == "" {
			return refuse(model.CauseEmptyIdentity, name, model.JSONString, model.CaptureUnsupportedSchema)
		}
		if len(value) > 512 {
			return refuse(model.CauseOverlengthIdentity, name, model.JSONString, model.CaptureUnsupportedSchema)
		}
		if model.ValidateBindingText("value", value) != nil {
			return refuse(model.CauseUnusableIdentity, name, model.JSONString, model.CaptureUnsupportedSchema)
		}
		bindings = append(bindings, model.NativeBinding{Kind: identity.Binding, NativeName: name, Value: value})
	}
	return model.CaptureValid, bindings, model.CaptureCause{}
}

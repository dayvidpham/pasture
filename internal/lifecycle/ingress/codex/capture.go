// Package codex captures exact command-hook stdin bytes from the pinned Codex
// CLI lifecycle contract at the recorded version. Unlike the OpenCode ingress, the captured
// bytes ARE the native command-hook stdin payload; there is no in-process
// callback object and no nested record envelope to unwrap.
package codex

import (
	"encoding/json"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Capture is the disposition and durable delivery derived from one exact Codex
// command-hook stdin payload.
type Capture struct {
	Digest      digest.Digest
	Disposition model.CaptureDisposition
	Delivery    receipt.Delivery
}

// Parse captures the exact command-hook stdin bytes for a selected Codex event
// and extracts the provider-specific native correlation identities. The shared
// refusals run first, through ingress.Validate, so a malformed payload is
// refused with the same disposition on every harness. The raw bytes are
// retained byte-exact as the durable evidence body: Codex delivers the event
// JSON on stdin, so no field is lifted out or reformatted, and provider facts
// beyond correlation (tool name/input, permission mode, cwd) survive
// unflattened in the body.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	validation := ingress.Validate(raw)
	manifest := registration.Codex0_153_0()
	envelope.Runtime.Contract = manifest.Contract
	envelope.HostVersion = observedVersion
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}

	if validation.Disposition == model.CaptureValid {
		// The caller selects a pinned event; it cannot replace that event's
		// required fields with a weaker, caller-authored registration row.
		pinned, err := ingress.EventByNativeName(manifest, event.NativeName)
		var value map[string]json.RawMessage
		if err != nil || pinned.Kind != event.Kind {
			result.Disposition = model.CaptureUnsupportedSchema
		} else if err := json.Unmarshal(validation.Body, &value); err != nil {
			result.Disposition = model.CaptureMalformed
		} else {
			result.Disposition, result.Delivery.Bindings = bindingsFor(pinned, value)
		}
	}
	result.Delivery.Capture = result.Disposition
	return result
}

// bindingsFor reads only the identities declared by the generated pinned
// catalogue. fieldNames is generated from the same source. Unknown payload
// members stay in the exact retained body and never become bindings.
func bindingsFor(event registration.Event, value map[string]json.RawMessage) (model.CaptureDisposition, []model.NativeBinding) {
	var nativeName string
	if err := json.Unmarshal(value["hook_event_name"], &nativeName); err != nil {
		return model.CaptureUnsupportedSchema, nil
	}
	if nativeName != event.NativeName {
		return model.CaptureUnsupportedSchema, nil
	}
	bindings := make([]model.NativeBinding, 0, len(event.Identities))
	for _, identity := range event.Identities {
		name, declared := fieldNames[identity.Field]
		if !declared {
			return model.CaptureUnsupportedSchema, nil
		}
		wire, present := value[name]
		if !present {
			if identity.Required {
				return model.CaptureUnsupportedSchema, nil
			}
			continue
		}
		var text string
		if err := json.Unmarshal(wire, &text); err != nil {
			return model.CaptureMalformed, nil
		}
		binding := model.NativeBinding{Kind: identity.Binding, NativeName: name, Value: text}
		if err := model.ValidateNativeBinding(binding); err != nil {
			return model.CaptureUnsupportedSchema, nil
		}
		bindings = append(bindings, binding)
	}
	return model.CaptureValid, bindings
}

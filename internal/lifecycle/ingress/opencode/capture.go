// Package opencode captures callback objects from the pinned OpenCode plugin
// contract without treating the serialized object as a native wire stream.
package opencode

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

// Parse captures the JSON serialization of the in-process callback object.
// The shared refusals run first, through ingress.Validate, so a malformed
// payload is refused with the same disposition on every harness. The selected
// event determines the provider-specific identity paths.
func Parse(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	validation := ingress.Validate(raw)
	manifest := registration.OpenCode1_18_29()
	envelope.Runtime.Contract = manifest.Contract
	envelope.HostVersion = observedVersion
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}

	if validation.Disposition == model.CaptureValid {
		var value callbackValue
		if err := json.Unmarshal(validation.Body, &value); err != nil {
			result.Disposition = model.CaptureMalformed
		} else {
			result.Disposition, result.Delivery.Bindings = bindingsFor(event.NativeName, value)
		}
	}
	result.Delivery.Capture = result.Disposition
	return result
}

type callbackValue struct {
	Event struct {
		Type       string `json:"type"`
		Properties struct {
			SessionID string `json:"sessionID"`
		} `json:"properties"`
	} `json:"event"`
	Input struct {
		SessionID string `json:"sessionID"`
		CallID    string `json:"callID"`
	} `json:"input"`
}

func bindingsFor(nativeName string, value callbackValue) (model.CaptureDisposition, []model.NativeBinding) {
	switch nativeName {
	case "session.created":
		if value.Event.Type != nativeName || value.Event.Properties.SessionID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{{Kind: model.BindingSession, NativeName: "sessionID", Value: value.Event.Properties.SessionID}}
	case "tool.execute.before":
		if value.Input.SessionID == "" || value.Input.CallID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "sessionID", Value: value.Input.SessionID},
			{Kind: model.BindingToolCall, NativeName: "callID", Value: value.Input.CallID},
		}
	default:
		return model.CaptureUnsupportedSchema, nil
	}
}

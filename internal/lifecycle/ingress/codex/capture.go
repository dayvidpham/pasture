// Package codex captures exact command-hook stdin bytes from the pinned Codex
// 0.146.0 CLI lifecycle contract. Unlike the OpenCode ingress, the captured
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
	manifest := registration.Codex0_146_0()
	envelope.Runtime.Contract = manifest.Contract
	envelope.HostVersion = observedVersion
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}

	if validation.Disposition == model.CaptureValid {
		var value payload
		if err := json.Unmarshal(validation.Body, &value); err != nil {
			result.Disposition = model.CaptureMalformed
		} else {
			result.Disposition, result.Delivery.Bindings = bindingsFor(event.NativeName, value)
		}
	}
	result.Delivery.Capture = result.Disposition
	return result
}

// payload holds only the native correlation fields extracted at ingress. All
// other keys in the command-hook stdin JSON remain in the retained body.
type payload struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	ToolUseID string `json:"tool_use_id"`
}

func bindingsFor(nativeName string, value payload) (model.CaptureDisposition, []model.NativeBinding) {
	switch nativeName {
	case "SessionStart":
		if value.SessionID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "session_id", Value: value.SessionID},
		}
	case "PreToolUse":
		if value.SessionID == "" || value.TurnID == "" || value.ToolUseID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "session_id", Value: value.SessionID},
			{Kind: model.BindingTurn, NativeName: "turn_id", Value: value.TurnID},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: value.ToolUseID},
		}
	default:
		return model.CaptureUnsupportedSchema, nil
	}
}

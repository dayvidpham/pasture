// Package opencode captures callback objects from the pinned OpenCode plugin
// contract without treating the serialized object as a native wire stream.
package opencode

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// ParseV2 captures the JSON serialization of a 2.0.20 hook input or bus
// event. The 2.0.20 plugin forwards each hook input verbatim, so every hook
// payload below is a flat object: session hooks carry a top-level sessionID,
// both tool hooks carry sessionID plus the call id, the permission evaluation
// carries sessionID, and the shell hook carries no identity at all. The
// session.created bus event instead wraps its payload in data, following the
// durable envelope in packages/schema/src/event.ts at the 2.0.20 tag, so its
// sessionID and version ride at data.sessionID and data.version where the
// 1.18.29 contract carried event.properties.sessionID.
//
// The shared refusals run first, through ingress.Validate, so a malformed
// payload is refused with the same disposition on every harness and every
// OpenCode contract version. The selected event determines the
// provider-specific identity paths.
func ParseV2(raw []byte, event registration.Event, observedVersion string, envelope model.OccurrenceEnvelopeRef) Capture {
	validation := ingress.Validate(raw)
	manifest := registration.OpenCode2_0_20()
	envelope.Runtime.Contract = manifest.Contract
	envelope.HostVersion = observedVersion
	result := Capture{Digest: validation.Digest, Disposition: validation.Disposition}
	result.Cause = model.JSONCaptureCause(validation.Body, validation.Disposition)
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}

	if validation.Disposition == model.CaptureValid {
		var value callbackValueV2
		if err := json.Unmarshal(validation.Body, &value); err != nil {
			result.Disposition = model.CaptureMalformed
			var mismatch *json.UnmarshalTypeError
			if errors.As(err, &mismatch) {
				required := model.JSONString
				if mismatch.Type.Kind() == reflect.Struct {
					required = model.JSONObject
				}
				result.Cause = model.NewCaptureCause(model.CauseWrongKind, mismatch.Field, required, result.Disposition)
			}
		} else {
			result.Disposition, result.Delivery.Bindings = bindingsForV2(event.NativeName, value)
			if result.Disposition != model.CaptureValid {
				result.Cause = missingCallbackCauseV2(validation.Body, event.NativeName, value)
			} else {
				identityPrefix := ""
				if event.NativeName == "session.created" {
					identityPrefix = "data."
				}
				for _, binding := range result.Delivery.Bindings {
					if model.ValidateBindingText("value", binding.Value) == nil {
						continue
					}
					path := identityPrefix + binding.NativeName
					cause := model.CauseUnusableIdentity
					if len(binding.Value) > 512 {
						cause = model.CauseOverlengthIdentity
					}
					result.Disposition = model.CaptureUnsupportedSchema
					result.Cause = model.NewCaptureCause(cause, path, model.JSONString, result.Disposition)
					result.Delivery.Bindings = nil
					break
				}
			}
		}
	}
	result.Delivery.Capture = result.Disposition
	return result
}

// Decode errors follow encoding/json's first-error order in the wire object.
// Once decoding succeeds, required paths are checked in the same fixed order
// as bindingsForV2. The struct remains the admission authority: extra fields,
// case-insensitive matches and merged repeated nested objects stay compatible.
func missingCallbackCauseV2(raw []byte, nativeName string, value callbackValueV2) model.CaptureCause {
	switch nativeName {
	case "session.created":
		if value.Type == "" {
			return absentCallbackValue(raw, "type", false)
		}
		if value.Type != nativeName {
			return model.NewCaptureCause(model.CauseEventMismatch, "type", model.JSONString, model.CaptureUnsupportedSchema)
		}
		return absentCallbackValue(raw, "data.sessionID", true)
	case "tool.execute.before", "tool.execute.after":
		if value.SessionID == "" {
			return absentCallbackValue(raw, "sessionID", true)
		}
		return absentCallbackValue(raw, "id", true)
	case "session.prompt", "session.context", "session.compaction", "session.generate",
		"session.title", "session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "permission.evaluate":
		return absentCallbackValue(raw, "sessionID", true)
	case "shell.create.before":
		// The shell hook declares no session field: nothing is required and
		// nothing is diagnosed.
		return model.CaptureCause{}
	default:
		// Dispatch resolves names through the generated manifest first, so
		// an unlisted name never reaches here with a valid disposition. This
		// arm returns the same empty cause the 1.18.29 parser returns outside
		// its catalogue.
		return model.CaptureCause{}
	}
}

// callbackValueV2 is the 2.0.20 admission shape: the top-level bus type plus
// the flat identity fields every hook input declares. Session hooks declare a
// readonly sessionID, both tool hooks declare sessionID plus the call id, and
// the permission evaluation declares sessionID; the shell hook declares
// neither and decodes to the zero value, which its arm ignores. The bus
// event's payload rides inside data per the durable envelope, so Data carries
// the session.created identity while the flat fields stay zero there.
type callbackValueV2 struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	ID        string `json:"id"`
	Data      struct {
		SessionID string `json:"sessionID"`
	} `json:"data"`
}

func bindingsForV2(nativeName string, value callbackValueV2) (model.CaptureDisposition, []model.NativeBinding) {
	switch nativeName {
	case "session.created":
		if value.Type != nativeName || value.Data.SessionID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{{Kind: model.BindingSession, NativeName: "sessionID", Value: value.Data.SessionID}}
	case "tool.execute.before", "tool.execute.after":
		if value.SessionID == "" || value.ID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "sessionID", Value: value.SessionID},
			{Kind: model.BindingToolCall, NativeName: "id", Value: value.ID},
		}
	case "shell.create.before":
		return model.CaptureValid, nil
	case "session.prompt", "session.context", "session.compaction", "session.generate",
		"session.title", "session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "permission.evaluate":
		if value.SessionID == "" {
			return model.CaptureUnsupportedSchema, nil
		}
		return model.CaptureValid, []model.NativeBinding{{Kind: model.BindingSession, NativeName: "sessionID", Value: value.SessionID}}
	default:
		return model.CaptureUnsupportedSchema, nil
	}
}

// Package opencode captures callback objects from the pinned OpenCode plugin
// contract without treating the serialized object as a native wire stream.
package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

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
	result.Cause = model.JSONCaptureCause(validation.Body, validation.Disposition)
	result.Delivery = receipt.Delivery{Contract: manifest.Contract, Event: event.Kind, Envelope: envelope, Body: validation.Body}

	if validation.Disposition == model.CaptureValid {
		var value callbackValue
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
			result.Disposition, result.Delivery.Bindings = bindingsFor(event.NativeName, value)
			if result.Disposition != model.CaptureValid {
				result.Cause = missingCallbackCause(validation.Body, event.NativeName, value)
			} else {
				for _, binding := range result.Delivery.Bindings {
					if model.ValidateBindingText("value", binding.Value) == nil {
						continue
					}
					path := "input." + binding.NativeName
					if event.NativeName == "session.created" {
						path = "event.properties." + binding.NativeName
					}
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
// as bindingsFor. The struct remains the admission authority: extra fields,
// case-insensitive matches and merged repeated nested objects stay compatible.
func missingCallbackCause(raw []byte, nativeName string, value callbackValue) model.CaptureCause {
	switch nativeName {
	case "session.created":
		if value.Event.Type == "" {
			return absentCallbackValue(raw, "event.type", false)
		}
		if value.Event.Type != nativeName {
			return model.NewCaptureCause(model.CauseEventMismatch, "event.type", model.JSONString, model.CaptureUnsupportedSchema)
		}
		return absentCallbackValue(raw, "event.properties.sessionID", true)
	case "tool.execute.before":
		if value.Input.SessionID == "" {
			return absentCallbackValue(raw, "input.sessionID", true)
		}
		return absentCallbackValue(raw, "input.callID", true)
	default:
		return model.CaptureCause{}
	}
}

func absentCallbackValue(raw []byte, path string, identity bool) model.CaptureCause {
	parts := strings.Split(path, ".")
	objects := [][]byte{raw}
	for i, name := range parts {
		required := model.JSONObject
		if i == len(parts)-1 {
			required = model.JSONString
		}
		values := matchingMembers(objects, name)
		field := strings.Join(parts[:i+1], ".")
		cause := model.CauseMissingMember
		if len(values) > 0 {
			cause = model.CauseWrongKind
			objects = nil
			for _, value := range values {
				if len(value) > 0 && value[0] == '{' {
					objects = append(objects, value)
				}
			}
			if required == model.JSONObject && len(objects) > 0 {
				continue
			}
			if required == model.JSONString {
				// Null does not overwrite a string in encoding/json. Check the
				// last non-null value, exactly as the authoritative decoder did.
				for j := len(values) - 1; j >= 0; j-- {
					if bytes.Equal(values[j], []byte("null")) {
						continue
					}
					if identity {
						cause = model.CauseEmptyIdentity
					} else {
						cause = model.CauseEventMismatch
					}
					break
				}
			}
		}
		return model.NewCaptureCause(cause, field, required, model.CaptureUnsupportedSchema)
	}
	return model.CaptureCause{}
}

// matchingMembers preserves wire order and object merge semantics. A map lookup
// would lose both when two case variants match one Go struct field.
func matchingMembers(objects [][]byte, name string) [][]byte {
	var values [][]byte
	for _, object := range objects {
		decoder := json.NewDecoder(bytes.NewReader(object))
		_, _ = decoder.Token() // already validated and decoded by Parse
		for decoder.More() {
			key, _ := decoder.Token()
			var raw json.RawMessage
			_ = decoder.Decode(&raw)
			if strings.EqualFold(key.(string), name) {
				values = append(values, bytes.TrimSpace(raw))
			}
		}
	}
	return values
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

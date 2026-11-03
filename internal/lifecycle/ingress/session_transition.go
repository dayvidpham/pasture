package ingress

import (
	"encoding/json"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// ExtractSessionTransition reads only prompt/end/resume evidence from an
// admitted native delivery. Callers pass the parser's disposition and bindings,
// never a client-provided transition. It is pure and shared by direct, daemon
// and authenticated legacy-payload readers. A refusal yields no transition and
// cannot turn optional metadata into a policy denial.
func ExtractSessionTransition(contract ir.RuntimeContractID, kind model.ContractEventKind, raw []byte, capture model.CaptureDisposition, bindings []model.NativeBinding) model.SessionTransition {
	if capture != model.CaptureValid || len(raw) > model.MaxNativePayloadBytes {
		return model.SessionTransitionNone
	}
	manifest, event, known := registration.EventWithManifest(contract, kind)
	if !known {
		return model.SessionTransitionNone
	}
	harness := manifest.Harness
	if event.SessionTransition == model.SessionTransitionNone {
		return model.SessionTransitionNone
	}
	var session string
	for _, binding := range bindings {
		if binding.Kind != model.BindingSession {
			continue
		}
		if session != "" || model.ValidateBindingText("session", binding.Value) != nil || binding.Value == "" {
			return model.SessionTransitionNone
		}
		session = binding.Value
	}
	if session == "" {
		return model.SessionTransitionNone
	}
	validation := Validate(raw)
	if validation.Disposition != model.CaptureValid {
		return model.SessionTransitionNone
	}
	readString := func(name string) string {
		var value string
		if json.Unmarshal(validation.Members[name], &value) != nil {
			return ""
		}
		return value
	}
	if harness == ir.HarnessOpenCode {
		if readString("sessionID") != session {
			return model.SessionTransitionNone
		}
	} else {
		if readString("session_id") != session || readString("hook_event_name") != event.NativeName {
			return model.SessionTransitionNone
		}
	}
	if event.SessionTransition == model.SessionTransitionResume && (event.TransitionSourceField == "" || readString(event.TransitionSourceField) != "resume") {
		return model.SessionTransitionNone
	}
	return event.SessionTransition
}

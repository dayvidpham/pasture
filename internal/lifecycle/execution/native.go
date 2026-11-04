package execution

import (
	"encoding/json"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Native extracts only execution members from an already validated, session-bound
// delivery. It does not retain unrelated host payload fields.
func Native(harness ir.HarnessID, event model.ContractEventKind, body []byte) (Inputs, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return Inputs{}, false
	}
	in := Inputs{}
	if harness == ir.HarnessCodex {
		switch event {
		case registration.EventCodexSessionStart:
			event = registration.EventSessionStart
		case registration.EventCodexUserPromptSubmit:
			event = registration.EventUserPromptSubmit
		case registration.EventCodexSubagentStart:
			event = registration.EventSubagentStart
		default:
			return Inputs{}, false
		}
	}
	stringField := func(obj map[string]json.RawMessage, name string, member Member) Raw {
		value, ok := obj[name]
		if !ok {
			return Raw{}
		}
		var text string
		_ = json.Unmarshal(value, &text)
		return Raw{Present: true, Member: member, Value: text}
	}
	switch harness {
	case ir.HarnessClaudeCode, ir.HarnessCodex:
		switch event {
		case registration.EventSessionStart, registration.EventUserPromptSubmit:
			in.Kind = Selection
			in.Host.Model = stringField(object, "model", HostModel)
		case registration.EventPostModelSwitch:
			if harness != ir.HarnessClaudeCode {
				return Inputs{}, false
			}
			in.Kind = Selection
			in.Host.Model = stringField(object, "to_model", HostToModel)
		case registration.EventSubagentStart:
			in.Kind = Child
			in.Host.Role = stringField(object, "agent_type", HostAgentType)
			if harness == ir.HarnessCodex {
				in.Host.Model = stringField(object, "model", HostModel)
			}
			_ = json.Unmarshal(object["agent_id"], &in.Scope.AgentID)
		default:
			return Inputs{}, false
		}
		if harness == ir.HarnessCodex {
			_ = json.Unmarshal(object["turn_id"], &in.Scope.TurnID)
		}
	case ir.HarnessOpenCode:
		switch event {
		case registration.EventOpenCode2SessionContext:
			in.Kind = Primary
		case registration.EventOpenCode2SessionCompaction:
			in.Kind = Compaction
		case registration.EventOpenCode2SessionTitle:
			in.Kind = Title
		case registration.EventOpenCode2SessionGenerate:
			in.Kind = Generate
		case registration.EventOpenCode2SessionModelRequest, registration.EventOpenCode2SessionHttpRequest, registration.EventOpenCode2SessionHttpResponse:
			var kind string
			_ = json.Unmarshal(object["kind"], &kind)
			switch Kind(kind) {
			case Primary, Compaction, Title, Generate:
				in.Kind = Kind(kind)
			default:
				return Inputs{}, false
			}
		default:
			return Inputs{}, false
		}
		var model map[string]json.RawMessage
		if json.Unmarshal(object["model"], &model) == nil && model != nil {
			in.Host.Provider = stringField(model, "providerID", HostProvider)
			in.Host.Model = stringField(model, "id", HostModelID)
			in.Host.Variant = stringField(model, "variant", HostVariant)
		}
		in.Host.Role = stringField(object, "agent", HostAgent)
	default:
		return Inputs{}, false
	}
	return in, true
}

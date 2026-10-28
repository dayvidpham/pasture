package execution

import "encoding/json"

// Native extracts only execution members from an already validated, session-bound
// delivery. It does not retain unrelated host payload fields.
func Native(harness, event string, body []byte) (Inputs, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return Inputs{}, false
	}
	in := Inputs{}
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
	case "claude-code", "codex":
		switch event {
		case "SessionStart", "UserPromptSubmit":
			in.Kind = Selection
			in.Host.Model = stringField(object, "model", HostModel)
		case "PostModelSwitch":
			if harness != "claude-code" {
				return Inputs{}, false
			}
			in.Kind = Selection
			in.Host.Model = stringField(object, "to_model", HostToModel)
		case "SubagentStart":
			in.Kind = Child
			in.Host.Role = stringField(object, "agent_type", HostAgentType)
			if harness == "codex" {
				in.Host.Model = stringField(object, "model", HostModel)
			}
			_ = json.Unmarshal(object["agent_id"], &in.Scope.AgentID)
		default:
			return Inputs{}, false
		}
		if harness == "codex" {
			_ = json.Unmarshal(object["turn_id"], &in.Scope.TurnID)
		}
	case "opencode":
		switch event {
		case "session.context":
			in.Kind = Primary
		case "session.compaction":
			in.Kind = Compaction
		case "session.title":
			in.Kind = Title
		case "session.generate":
			in.Kind = Generate
		case "session.model.request", "session.http.request", "session.http.response":
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

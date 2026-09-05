package hostcontract

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

const (
	openCodeSessionID model.NativeFieldID = 1001 + iota
	openCodeCallID
)

var openCodeFields = []Field{
	{openCodeSessionID, "FieldOpenCodeSessionID", "sessionID"},
	{openCodeCallID, "FieldOpenCodeCallID", "callID"},
}

// OpenCode1_18_29 derives registration order and native names from the typed
// runtime lifecycle contract. Only the two authentically observed callbacks
// add provider payload fields and identity bindings at this ingress boundary.
func OpenCode1_18_29() Contract {
	runtimeContract := pastureruntime.OpenCode1_18_29Lifecycle()
	runtimeEvents := runtimeContract.Events()
	events := make([]Event, 0, len(runtimeEvents))
	for index, runtimeEvent := range runtimeEvents {
		mapping, err := runtimeContract.Mapping(runtimeEvent)
		if err != nil {
			panic(err)
		}
		event := Event{
			Kind:     model.ContractEventKind(31 + index),
			Symbol:   symbol("EventOpenCode", mapping.NativeName()),
			Name:     mapping.NativeName(),
			Blocking: openCodeBlocking(mapping.Blocking()),
			Mutation: MutationNone,
			Failure:  mapping.Failure(),
			StopLoop: StopLoopNotApplicable,
		}
		switch runtimeEvent {
		case pastureruntime.OpenCodeEventSessionCreated:
			event.Fields = []model.NativeFieldID{openCodeSessionID}
			event.Identities = []Identity{{Field: openCodeSessionID, Binding: model.BindingSession, Required: true}}
		case pastureruntime.OpenCodeEventToolExecuteBefore:
			event.Fields = []model.NativeFieldID{openCodeSessionID, openCodeCallID}
			event.Identities = []Identity{
				{Field: openCodeSessionID, Binding: model.BindingSession, Required: true},
				{Field: openCodeCallID, Binding: model.BindingToolCall, Required: true},
			}
		}
		events = append(events, event)
	}
	return Contract{Version: "1.18.29", Fields: append([]Field(nil), openCodeFields...), Events: events}
}

func openCodeBlocking(mode pastureruntime.BlockingMode) BlockingMode {
	if mode == pastureruntime.NonBlocking {
		return NonBlocking
	}
	return Blocking
}

func symbol(prefix, name string) string {
	out := []byte(prefix)
	upper := true
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			upper = true
			continue
		}
		c := name[i]
		if c == '-' {
			// A hyphen in a native name (installation.update-available) is
			// carried as an underscore, so the Go identifier stays the one the
			// tree has always used while the native name is spelled as the host
			// emits it.
			c = '_'
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		out = append(out, c)
	}
	return string(out)
}

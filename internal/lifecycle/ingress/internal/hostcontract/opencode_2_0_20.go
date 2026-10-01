package hostcontract

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

const (
	openCode2SessionID model.NativeFieldID = 1001 + iota
	openCode2CallID
)

var openCode2Fields = []Field{
	{openCode2SessionID, "FieldOpenCode2SessionID", "sessionID"},
	{openCode2CallID, "FieldOpenCode2CallID", "id"},
}

// OpenCode2_0_20 derives registration order and native names from the typed
// runtime lifecycle contract. Only the two authentically declared callbacks
// add provider payload fields and identity bindings at this ingress boundary.
//
// The sixteen native names are the dotted Plugin.define hook coordinates the
// OpenCode 2.0.20 host triggers: twelve session hooks from SessionHooks in
// packages/plugin/src/promise/session.ts, two tool hooks from ToolHooks in
// packages/plugin/src/promise/tool.ts, the permission evaluate hook from
// PermissionHooks in packages/plugin/src/promise/permission.ts, and the shell
// hook from ShellHooks in packages/plugin/src/promise/shell.ts.
//
// The permission evaluate row is the measured deny channel. At 2.0.20 the
// host's evaluateInput in packages/core/src/permission.ts computes the
// ruleset effect, yields hooks.trigger("permission", "evaluate", {... effect})
// and returns { effect: event.effect, message: event.message, rules }, so a
// plugin mutation of event.effect reaches the caller on the path where no
// saved or configured rule already denied (that path returns early without
// firing the hook). The host-visible refusal bytes for that denial are not
// established here; they await an authentic 2.0.20 capture. No row carries
// response-channel evidence, so every row derives CapabilityNone.
func OpenCode2_0_20() Contract {
	runtimeContract := pastureruntime.OpenCode2_0_20Lifecycle()
	runtimeEvents := runtimeContract.Events()
	events := make([]Event, 0, len(runtimeEvents))
	for index, runtimeEvent := range runtimeEvents {
		mapping, err := runtimeContract.Mapping(runtimeEvent)
		if err != nil {
			panic(err)
		}
		event := Event{
			Kind:     model.ContractEventKind(31 + index),
			Symbol:   symbol("EventOpenCode2", mapping.NativeName()),
			Name:     mapping.NativeName(),
			Blocking: openCodeBlocking(mapping.Blocking()),
			Mutation: MutationNone,
			Failure:  mapping.Failure(),
			StopLoop: StopLoopNotApplicable,
		}
		switch runtimeEvent {
		case pastureruntime.OpenCode2EventPermissionEvaluate:
			event.Fields = []model.NativeFieldID{openCode2SessionID}
			event.Identities = []Identity{{Field: openCode2SessionID, Binding: model.BindingSession, Required: true}}
		case pastureruntime.OpenCode2EventToolExecuteBefore:
			event.Fields = []model.NativeFieldID{openCode2SessionID, openCode2CallID}
			event.Identities = []Identity{
				{Field: openCode2SessionID, Binding: model.BindingSession, Required: true},
				{Field: openCode2CallID, Binding: model.BindingToolCall, Required: true},
			}
		}
		events = append(events, event)
	}
	return Contract{Version: "2.0.20", Fields: append([]Field(nil), openCode2Fields...), Events: events}
}

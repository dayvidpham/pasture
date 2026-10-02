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
// runtime lifecycle contract. Identity bindings follow the host's declared
// payload fields: every 2.0.20 session payload type carries a readonly
// sessionID, both tool hooks carry sessionID plus the call id, and the
// permission evaluation carries sessionID, so every event below binds the
// session identity and both tool events also bind the call identity. Only the
// shell hook declares no session field and binds none.
//
// That binding policy is deliberately broader than the capture-proof rule the
// v1 contract keeps (only capture-proved rows carry identities there). The v2
// bindings are source-declared and await proof: the committed OpenCode 2.0.20
// capture sitting (with inventory, substitution, secret scan and clearance)
// turns them into proved coordinates when it lands, and the v2 proof test
// beside the ingress package pins this exact policy until then.
//
// The permission evaluate row is the source-cited deny channel, not a
// measured one. At 2.0.20 the host's evaluateInput in
// packages/core/src/permission.ts computes the ruleset effect, yields
// hooks.trigger("permission", "evaluate", {... effect}) and returns
// { effect: event.effect, message: event.message, rules }, so a plugin
// mutation of event.effect reaches the caller on the path where no saved or
// configured rule already denied (that path returns early without firing the
// hook). The host-visible refusal bytes for that denial are established by
// the capture sitting named above. No row carries response-channel evidence,
// so every row derives CapabilityNone.
//
// Event kinds below are contract-local ordinals starting at 1, in runtime
// catalog order. The generator assigns the shared walk-order kinds when it
// renders registration; these values never leave this contract.
func OpenCode2_0_20() Contract {
	runtimeContract := pastureruntime.OpenCode2_0_20Lifecycle()
	runtimeEvents := runtimeContract.Events()
	events := make([]Event, 0, len(runtimeEvents))
	for index, runtimeEvent := range runtimeEvents {
		mapping, err := runtimeContract.Mapping(runtimeEvent)
		if err != nil {
			panic(err)
		}
		session := []model.NativeFieldID{openCode2SessionID}
		sessionIdentity := []Identity{{Field: openCode2SessionID, Binding: model.BindingSession, Required: true}}
		event := Event{
			Kind:     model.ContractEventKind(1 + index),
			Symbol:   symbol("EventOpenCode2", mapping.NativeName()),
			Name:     mapping.NativeName(),
			Blocking: openCodeBlocking(mapping.Blocking()),
			Mutation: MutationNone,
			Failure:  mapping.Failure(),
			StopLoop: StopLoopNotApplicable,
		}
		switch runtimeEvent {
		case pastureruntime.OpenCode2EventSessionCreated,
			pastureruntime.OpenCode2EventSessionPrompt,
			pastureruntime.OpenCode2EventSessionContext,
			pastureruntime.OpenCode2EventSessionCompaction,
			pastureruntime.OpenCode2EventSessionGenerate,
			pastureruntime.OpenCode2EventSessionTitle,
			pastureruntime.OpenCode2EventSessionModelRequest,
			pastureruntime.OpenCode2EventSessionHTTPRequest,
			pastureruntime.OpenCode2EventSessionHTTPResponse,
			pastureruntime.OpenCode2EventSessionExperimentalWSHandshake,
			pastureruntime.OpenCode2EventSessionExperimentalWSSend,
			pastureruntime.OpenCode2EventSessionExperimentalWSReceive,
			pastureruntime.OpenCode2EventSessionRetry,
			pastureruntime.OpenCode2EventPermissionEvaluate:
			event.Fields = session
			event.Identities = sessionIdentity
		case pastureruntime.OpenCode2EventToolExecuteBefore,
			pastureruntime.OpenCode2EventToolExecuteAfter:
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

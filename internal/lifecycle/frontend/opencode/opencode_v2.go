// Package opencode supplies the pinned OpenCode callback vocabulary as host data
// for the generic lifecycle frontend. All control flow lives in
// internal/lifecycle/frontend; this package is data plus a monomorphic wrapper.
package opencode

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// eventMappingsV2 pairs every generated OpenCode 2.0.20 registration ordinal
// with its runtime profile event, by native name. The registration and runtime
// enumerations are separate contracts, so every ordinal is explicit here and a
// test holds the pairing total and correct. A complete mapping is not an
// enabled event: the activation table decides admission before any payload is
// read, so an event without an authentic fixture stays withheld upstream.
var eventMappingsV2 = map[model.ContractEventKind]runtime.OpenCode2LifecycleEvent{
	registration.EventOpenCode2SessionCreated:                 runtime.OpenCode2EventSessionCreated,                 // session.created
	registration.EventOpenCode2SessionPrompt:                  runtime.OpenCode2EventSessionPrompt,                  // session.prompt
	registration.EventOpenCode2SessionContext:                 runtime.OpenCode2EventSessionContext,                 // session.context
	registration.EventOpenCode2SessionCompaction:              runtime.OpenCode2EventSessionCompaction,              // session.compaction
	registration.EventOpenCode2SessionGenerate:                runtime.OpenCode2EventSessionGenerate,                // session.generate
	registration.EventOpenCode2SessionTitle:                   runtime.OpenCode2EventSessionTitle,                   // session.title
	registration.EventOpenCode2SessionModelRequest:            runtime.OpenCode2EventSessionModelRequest,            // session.model.request
	registration.EventOpenCode2SessionHttpRequest:             runtime.OpenCode2EventSessionHTTPRequest,             // session.http.request
	registration.EventOpenCode2SessionHttpResponse:            runtime.OpenCode2EventSessionHTTPResponse,            // session.http.response
	registration.EventOpenCode2SessionExperimentalWsHandshake: runtime.OpenCode2EventSessionExperimentalWSHandshake, // session.experimental.ws.handshake
	registration.EventOpenCode2SessionExperimentalWsSend:      runtime.OpenCode2EventSessionExperimentalWSSend,      // session.experimental.ws.send
	registration.EventOpenCode2SessionExperimentalWsReceive:   runtime.OpenCode2EventSessionExperimentalWSReceive,   // session.experimental.ws.receive
	registration.EventOpenCode2SessionRetry:                   runtime.OpenCode2EventSessionRetry,                   // session.retry
	registration.EventOpenCode2ToolExecuteBefore:              runtime.OpenCode2EventToolExecuteBefore,              // tool.execute.before
	registration.EventOpenCode2ToolExecuteAfter:               runtime.OpenCode2EventToolExecuteAfter,               // tool.execute.after
	registration.EventOpenCode2PermissionEvaluate:             runtime.OpenCode2EventPermissionEvaluate,             // permission.evaluate
	registration.EventOpenCode2ShellCreateBefore:              runtime.OpenCode2EventShellCreateBefore,              // shell.create.before
}

// hostV2 is the OpenCode 2.0.20 data consumed by the generic frontend engine.
var hostV2 = frontend.Host[runtime.OpenCode2LifecycleEvent]{
	Label:    "OpenCode",
	Contract: runtime.OpenCode2_0_20Lifecycle,
	Events:   eventMappingsV2,
}

// BindV2 creates L1 and typed identities for a registered OpenCode 2.0.20
// callback. It delegates to the generic strictest-common frontend engine.
func BindV2(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(hostV2, modelKind, bindings)
}

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

var eventMappings = map[model.ContractEventKind]runtime.OpenCodeLifecycleEvent{
	registration.EventOpenCodeSessionCreated:    runtime.OpenCodeEventSessionCreated,
	registration.EventOpenCodeToolExecuteBefore: runtime.OpenCodeEventToolExecuteBefore,
}

// host is the pinned OpenCode data consumed by the generic frontend engine.
var host = frontend.Host[runtime.OpenCodeLifecycleEvent]{
	Label:    "OpenCode",
	Contract: runtime.OpenCode1_18_10Lifecycle,
	Events:   eventMappings,
}

// Bind creates L1 and typed identities for an authentically proven callback. It
// delegates to the generic strictest-common frontend engine.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(host, modelKind, bindings)
}

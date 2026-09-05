// Package codex supplies the Codex command-hook vocabulary, at the recorded Codex version, as
// host data for the generic lifecycle frontend. All control flow lives in
// internal/lifecycle/frontend; this package is data plus a monomorphic wrapper.
//
// This frontend binds ONLY the two authenticity-proven events, SessionStart
// (observation smoke) and PreToolUse (gate); every other source-derived catalog
// entry is rejected by Bind. Because of that closed positive scope, the
// deliberate divergence between the self-contained ingress catalog
// (internal/lifecycle/ingress/internal/hostcontract/codex_0_146_0.go) and the
// runtime Codex profile on the 8 non-proven events is inert here — that
// metadata is never ingested.
package codex

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexLifecycle returns the pinned Codex runtime lifecycle contract used to
// bind native events into the waist.
//
// IP-1 (resolved at M3-WAVE-1 consolidation): the sole runtime seam in this
// package. M3-SLICE-1 owns the exact profile; this frontend binds against the
// Codex runtime contract and its two authentically proven events
// (SessionStart observation, PreToolUse gate).
func codexLifecycle() runtime.LifecycleContract[runtime.CodexLifecycleEvent] {
	return runtime.Codex0_146_0Lifecycle()
}

var eventMappings = map[model.ContractEventKind]runtime.CodexLifecycleEvent{
	registration.EventCodexSessionStart: runtime.CodexEventSessionStart,
	registration.EventCodexPreToolUse:   runtime.CodexEventPreToolUse,
}

// host is the pinned Codex data consumed by the generic frontend engine.
var host = frontend.Host[runtime.CodexLifecycleEvent]{
	Label:    "Codex",
	Contract: codexLifecycle,
	Events:   eventMappings,
}

// Bind creates L1 and typed identities for an authentically proven Codex
// command-hook event. Only SessionStart and PreToolUse have authentic runtime
// evidence and a frontend binding; every other catalog entry is rejected. It
// delegates to the generic strictest-common frontend engine.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(host, modelKind, bindings)
}

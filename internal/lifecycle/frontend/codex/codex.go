// Package codex supplies the Codex command-hook vocabulary, at the recorded Codex version, as
// host data for the generic lifecycle frontend. All control flow lives in
// internal/lifecycle/frontend; this package is data plus a monomorphic wrapper.
//
// The event mapping is COMPLETE over the generated Codex registration: every
// registered event maps to its runtime profile row, paired by native name, and
// a test holds the pairing total. A complete mapping is not an enabled event:
// the activation table decides admission before any payload is read, so an
// event without an authentic fixture stays withheld upstream and never reaches
// Bind in production.
//
// MEASURED: the self-contained ingress catalogue
// (internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go, read by the
// handler's admission) and the runtime Codex profile (read by Bind) disagree on
// every registered event on at least one axis: the failure mode on 11 of the 12
// (the catalogue declares report-and-continue or exit-2-blocks where the profile
// declares strict-hook-failure or a demoted exit-2-blocks), the mutation mode on
// PreToolUse and PostToolUse, the blocking mode on PostCompact, and the declared
// identities on the 10 events without an authentic capture. The frontend
// binds with the profile's row; the handler admits with the catalogue's row.
// Re-deriving the catalogue from the profile is the OpenCode-style remedy and is
// separate work; until it lands, this package states the divergence.
//
// The counts above are read back from the tree by
// internal/lifecycle/registration/failure_divergence_test.go, so a new
// registration or a new capture turns that test RED instead of leaving a stale
// number here.
package codex

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexLifecycle returns the Codex runtime lifecycle contract used to bind
// native events into the waist: the sole runtime seam in this package.
func codexLifecycle() runtime.LifecycleContract[runtime.CodexLifecycleEvent] {
	return runtime.Codex0_153_0Lifecycle()
}

// eventMappings pairs every generated Codex registration ordinal with its
// runtime profile event, by native name. The registration and runtime
// enumerations are separate contracts, so every ordinal is explicit here and a
// test holds the pairing total and correct.
var eventMappings = map[model.ContractEventKind]runtime.CodexLifecycleEvent{
	registration.EventCodexSessionStart:      runtime.CodexEventSessionStart,      // SessionStart
	registration.EventCodexUserPromptSubmit:  runtime.CodexEventUserPromptSubmit,  // UserPromptSubmit
	registration.EventCodexPreToolUse:        runtime.CodexEventPreToolUse,        // PreToolUse
	registration.EventCodexPermissionRequest: runtime.CodexEventPermissionRequest, // PermissionRequest
	registration.EventCodexPostToolUse:       runtime.CodexEventPostToolUse,       // PostToolUse
	registration.EventCodexPreCompact:        runtime.CodexEventPreCompact,        // PreCompact
	registration.EventCodexPostCompact:       runtime.CodexEventPostCompact,       // PostCompact
	registration.EventCodexSubagentStart:     runtime.CodexEventSubagentStart,     // SubagentStart
	registration.EventCodexSubagentStop:      runtime.CodexEventSubagentStop,      // SubagentStop
	registration.EventCodexStop:              runtime.CodexEventStop,              // Stop
	registration.EventCodexSessionEnd:        runtime.CodexEventSessionEnd,        // SessionEnd
	registration.EventCodexInterrupt:         runtime.CodexEventInterrupt,         // Interrupt
}

// host is the pinned Codex data consumed by the generic frontend engine.
var host = frontend.Host[runtime.CodexLifecycleEvent]{
	Label:    "Codex",
	Contract: codexLifecycle,
	Events:   eventMappings,
}

// Bind creates L1 and typed identities for a registered Codex command-hook
// event. It delegates to the generic strictest-common frontend engine.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	return frontend.Bind(host, modelKind, bindings)
}

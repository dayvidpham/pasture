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
// MEASURED: the committed manifest the handler admits with
// (internal/lifecycle/registration/codex_0_153_0.gen.go, which code generation
// renders from the self-contained ingress catalogue
// internal/lifecycle/ingress/internal/hostcontract/codex_0_153_0.go) and the
// runtime Codex profile (read by Bind) state the same failure mode on all 12 of
// the 12 registered events, because that catalogue READS that field from the
// profile.
//
// Failure-arm agreement does not imply agreement elsewhere.
// The gate-or-observation semantic differs on PostCompact, an
// observation in the catalogue and a gate in the profile. The mutation mode
// differs on PostToolUse, which mutates the tool OUTPUT in the profile and has
// no output arm to be spelled with in the catalogue vocabulary. The correlation
// identities are the widest: the profile declares on 8 rows where the catalogue
// declares none, and 8 of those 8 are events with no authentic capture, because
// the catalogue declares an identity only from a capture. The frontend binds
// with the profile's row; the handler admits with the row code generation wrote
// into internal/lifecycle/registration/codex_0_153_0.gen.go from the catalogue.
//
// See internal/lifecycle/registration/failure_divergence_test.go for the
// sentence-specific measurements.
package codex

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexLifecycle returns the Codex runtime lifecycle contract used to bind
// native events into the waist.
func codexLifecycle() runtime.LifecycleContract[runtime.CodexLifecycleEvent] {
	return runtime.Codex0_153_0Lifecycle()
}

// eventMappings pairs every generated Codex registration ordinal with its
// runtime profile event, by native name. The registration and runtime
// enumerations are separate contracts, so every ordinal is explicit here and a
// test holds the pairing total and correct.
var eventMappings = map[model.ContractEventKind]runtime.CodexLifecycleEvent{
	registration.EventCodexSessionStart:      runtime.CodexEventSessionStart,
	registration.EventCodexUserPromptSubmit:  runtime.CodexEventUserPromptSubmit,
	registration.EventCodexPreToolUse:        runtime.CodexEventPreToolUse,
	registration.EventCodexPermissionRequest: runtime.CodexEventPermissionRequest,
	registration.EventCodexPostToolUse:       runtime.CodexEventPostToolUse,
	registration.EventCodexPreCompact:        runtime.CodexEventPreCompact,
	registration.EventCodexPostCompact:       runtime.CodexEventPostCompact,
	registration.EventCodexSubagentStart:     runtime.CodexEventSubagentStart,
	registration.EventCodexSubagentStop:      runtime.CodexEventSubagentStop,
	registration.EventCodexStop:              runtime.CodexEventStop,
	registration.EventCodexSessionEnd:        runtime.CodexEventSessionEnd,
	registration.EventCodexInterrupt:         runtime.CodexEventInterrupt,
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

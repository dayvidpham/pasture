// Package codex binds selected Codex 0.146.0 command-hook identities to the
// target-neutral lifecycle waist, producing verified L2.
package codex

import (
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// codexLifecycle returns the pinned Codex runtime lifecycle contract used to
// bind native events into the waist.
//
// IP-1-SWAP (M3-WAVE-1 consolidation): M3-SLICE-1 replaces the runtime Codex
// profile constructor with runtime.Codex0_146_0Lifecycle() (contract
// codex@0.146.0), removing runtime.Codex0_144_1Lifecycle(). Until that lands at
// wave consolidation this worktree references the base-tree constructor so the
// slice builds and its focused -race tests pass standalone. The two selected
// events (SessionStart observation, PreToolUse gate) are version-stable in
// semantics and declared identities, so the supervisor swaps this single call
// at consolidation and only the L2 origin version coordinate changes
// (codex@0.144.1 -> codex@0.146.0). This is the sole runtime reference in the
// slice; see the L1 completion note on aura-plugins-tcsxr for the mismatch risk.
func codexLifecycle() runtime.LifecycleContract[runtime.CodexLifecycleEvent] {
	return runtime.Codex0_144_1Lifecycle() // IP-1-SWAP -> runtime.Codex0_146_0Lifecycle()
}

var eventMappings = map[model.ContractEventKind]runtime.CodexLifecycleEvent{
	registration.EventCodexSessionStart: runtime.CodexEventSessionStart,
	registration.EventCodexPreToolUse:   runtime.CodexEventPreToolUse,
}

// Bind creates L1 and typed identities for an authentically proven Codex
// command-hook event.
//
// L1 skeleton: the body is implemented in L3 (M3-SLICE-2-L3). It returns an
// error so the L2 production-path tests fail until the real implementation
// lands.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	_ = modelKind
	_ = bindings
	return waist.L1{}, nil, bindError(
		"The Codex frontend binding is not implemented yet.",
		"L1 provides only the binding skeleton; the L3 implementation performs identity binding.",
		"No verified L2 lifecycle event was produced.",
		"Complete M3-SLICE-2-L3 to implement Codex identity binding.",
		nil,
	)
}

func bindError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    "Binding a Codex lifecycle command-hook event (internal/lifecycle/frontend/codex/codex.go in codex.Bind).",
		Impact:   impact,
		Fix:      fix,
		Cause:    cause,
	}
}

// eventMappings is referenced by the L3 implementation; declared in L1 so the
// binding contract is fixed before tests are written.
var _ = eventMappings

// codexLifecycle is referenced by the L3 implementation; the reference keeps the
// IP-1 seam live under vet in the L1 skeleton.
var _ = codexLifecycle

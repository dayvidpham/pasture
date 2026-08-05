// Package codex binds selected Codex 0.146.0 command-hook identities to the
// target-neutral lifecycle waist, producing verified L2.
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
	"fmt"

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
// command-hook event. Only SessionStart and PreToolUse have authentic runtime
// evidence and a frontend binding; every other catalog entry is rejected.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	runtimeKind, ok := eventMappings[modelKind]
	if !ok {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Codex lifecycle event ordinal %d has no authentic frontend binding.", modelKind),
			"Only command-hook kinds with authentic runtime evidence are bound in this profile.",
			"The command-hook cannot enter the lifecycle waist.",
			"Use SessionStart or PreToolUse from the pinned Codex registration.",
			nil,
		)
	}
	contract := codexLifecycle()
	mapping, err := contract.Mapping(runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Codex lifecycle event ordinal %d has no pinned runtime mapping.", modelKind),
			"The frontend and exact runtime catalog must select the same typed event.",
			"The command-hook cannot enter the lifecycle waist.",
			"Repair the Codex runtime profile before accepting this command-hook.",
			err,
		)
	}
	l1, err := waist.BindEvent(contract, runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Codex lifecycle event %q could not bind to the pinned runtime profile.", mapping.NativeName()),
			"The waist requires a constructor-built event binding.",
			"No lifecycle event or identities were returned.",
			"Use the matching Codex profile and generated event ordinal.",
			err,
		)
	}
	declared := mapping.Identities()
	identities := make([]waist.Identity, 0, len(bindings))
	for _, binding := range bindings {
		field, found := findDeclaredField(declared, binding.NativeName)
		if !found || uint8(binding.Kind) != uint8(field.Kind()) {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Codex event %q supplied undeclared identity %q.", mapping.NativeName(), binding.NativeName),
				"Command-hook identities must exactly match the pinned runtime mapping.",
				"The frontend rejected the command-hook identity.",
				"Forward the generated session_id, turn_id, and tool_use_id bindings without renaming them.",
				nil,
			)
		}
		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), binding.Value)
		if err != nil {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Codex event %q supplied an invalid %q identity.", mapping.NativeName(), binding.NativeName),
				"The waist validates identities before semantic correlation.",
				"The frontend rejected the command-hook identity.",
				"Forward the exact non-empty Codex identifier.",
				err,
			)
		}
		identities = append(identities, identity)
	}
	return l1, identities, nil
}

func findDeclaredField(declared []runtime.NativeIdentityField, name string) (runtime.NativeIdentityField, bool) {
	for _, field := range declared {
		if field.NativeName() == name {
			return field, true
		}
	}
	return runtime.NativeIdentityField{}, false
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

// Package opencode binds selected OpenCode callback identities to the
// target-neutral lifecycle waist.
package opencode

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

var eventMappings = map[model.ContractEventKind]runtime.OpenCodeLifecycleEvent{
	registration.EventOpenCodeSessionCreated:    runtime.OpenCodeEventSessionCreated,
	registration.EventOpenCodeToolExecuteBefore: runtime.OpenCodeEventToolExecuteBefore,
}

// Bind creates L1 and typed identities for an authentically proven callback.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	runtimeKind, ok := eventMappings[modelKind]
	if !ok {
		return waist.L1{}, nil, bindError(fmt.Sprintf("OpenCode lifecycle event ordinal %d has no authentic frontend binding.", modelKind), "Only callback kinds with authentic runtime evidence are bound in this profile.", "The callback cannot enter the lifecycle waist.", "Use session.created or tool.execute.before from the pinned OpenCode registration.", nil)
	}
	contract := runtime.OpenCode1_18_10Lifecycle()
	mapping, err := contract.Mapping(runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(fmt.Sprintf("OpenCode lifecycle event ordinal %d has no pinned runtime mapping.", modelKind), "The frontend and exact runtime catalog must select the same typed event.", "The callback cannot enter the lifecycle waist.", "Repair the OpenCode runtime profile before accepting this callback.", err)
	}
	l1, err := waist.BindEvent(contract, runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(fmt.Sprintf("OpenCode lifecycle event %q could not bind to the pinned runtime profile.", mapping.NativeName()), "The waist requires a constructor-built event binding.", "No lifecycle event or identities were returned.", "Use the matching OpenCode profile and generated event ordinal.", err)
	}
	declared := mapping.Identities()
	identities := make([]waist.Identity, 0, len(bindings))
	for _, binding := range bindings {
		field, found := findDeclaredField(declared, binding.NativeName)
		if !found || uint8(binding.Kind) != uint8(field.Kind()) {
			return waist.L1{}, nil, bindError(fmt.Sprintf("OpenCode event %q supplied undeclared identity %q.", mapping.NativeName(), binding.NativeName), "Callback identities must exactly match the pinned runtime mapping.", "The frontend rejected the callback identity.", "Forward the generated sessionID and callID bindings without renaming them.", nil)
		}
		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), binding.Value)
		if err != nil {
			return waist.L1{}, nil, bindError(fmt.Sprintf("OpenCode event %q supplied an invalid %q identity.", mapping.NativeName(), binding.NativeName), "The waist validates identities before semantic correlation.", "The frontend rejected the callback identity.", "Forward the exact non-empty OpenCode identifier.", err)
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
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: what, Why: why, Where: "Binding an OpenCode lifecycle callback (internal/lifecycle/frontend/opencode/opencode.go in opencode.Bind).", Impact: impact, Fix: fix, Cause: cause}
}

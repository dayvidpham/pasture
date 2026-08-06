// Package frontend is the single generic lifecycle frontend skeleton. Each
// harness (claude, opencode, codex) supplies its pinned data as a Host[E] and
// delegates to Bind; the per-host packages carry no control flow of their own.
//
// Bind implements the strictest-common admission policy: it hoists Claude's
// duplicate-name guard and its separated undeclared-field vs kind-mismatch
// errors (with a binding index) to every host. The admission decision is
// identical to the pre-refactor per-host frontends — only the error site and
// text change (the waist L2 transform already rejected duplicate {kind,
// nativeName} identities, so no accepted input becomes rejected and no
// rejected input becomes accepted).
package frontend

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Host is the per-harness data the generic Bind consumes. E is the harness's
// closed comparable event enum (runtime.ClaudeLifecycleEvent, etc.). Host is
// plain data with no behaviour: the ordinal→enum map, the pinned contract
// constructor, and the attribution label are all host-pinned facts.
type Host[E comparable] struct {
	// Label attributes errors to the harness (e.g. "Claude", "OpenCode",
	// "Codex"). It is the only per-host token that varies the error template.
	Label string
	// Contract returns the pinned, reviewed runtime lifecycle profile for the
	// harness. It is a constructor so Bind consumes a fresh immutable value.
	Contract func() runtime.LifecycleContract[E]
	// Events is the closed ordinal→typed-enum map. Only ordinals present here
	// have an authentic frontend binding; every other ordinal is rejected.
	Events map[model.ContractEventKind]E
}

// Bind is the one shared frontend boundary. It resolves the model ordinal to
// the host's typed event, binds it against the pinned contract, and converts
// each supplied native binding into a waist identity in Claude's exact check
// order: undeclared-field → duplicate-name → kind-agreement → value
// construction. It never persists and never constructs an L2 value.
func Bind[E comparable](
	host Host[E],
	modelKind model.ContractEventKind,
	bindings []model.NativeBinding,
) (waist.L1, []waist.Identity, error) {
	where := fmt.Sprintf(
		"Binding a %s lifecycle event (internal/lifecycle/frontend/frontend.go in frontend.Bind).",
		host.Label,
	)

	runtimeKind, ok := host.Events[modelKind]
	if !ok {
		return waist.L1{}, nil, bindError(
			where,
			fmt.Sprintf("%s lifecycle event ordinal %d is not declared.", host.Label, modelKind),
			fmt.Sprintf("The frontend accepts only the closed event ordinals emitted by the pinned %s registration.", host.Label),
			"The native event cannot enter the lifecycle waist.",
			fmt.Sprintf("Use one of the generated %s event ordinals from the registration contract.", host.Label),
			nil,
		)
	}

	contract := host.Contract()
	mapping, err := contract.Mapping(runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			where,
			fmt.Sprintf("%s lifecycle event ordinal %d has no pinned runtime mapping.", host.Label, modelKind),
			"Every frontend ordinal must select one reviewed semantic lifecycle mapping.",
			"The native event cannot enter the lifecycle waist.",
			fmt.Sprintf("Repair the pinned %s runtime profile before accepting this event.", host.Label),
			err,
		)
	}

	l1, err := waist.BindEvent(contract, runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			where,
			fmt.Sprintf("%s lifecycle event %q could not be bound to the pinned runtime profile.", host.Label, mapping.NativeName()),
			"The waist requires a constructor-built event binding from the reviewed lifecycle contract.",
			"No native event or identities were returned.",
			fmt.Sprintf("Use the matching pinned %s lifecycle profile and generated event ordinal.", host.Label),
			err,
		)
	}

	seenNames := make(map[string]struct{}, len(bindings))
	identities := make([]waist.Identity, 0, len(bindings))
	for index, binding := range bindings {
		field, found := mapping.DeclaredField(binding.NativeName)
		if !found {
			return waist.L1{}, nil, bindError(
				where,
				fmt.Sprintf("%s event %q binding %d names undeclared native field %q.", host.Label, mapping.NativeName(), index, binding.NativeName),
				"Only exact native identity names declared by the pinned runtime mapping may cross into the waist.",
				"The frontend rejected the binding and returned no identities.",
				"Use the exact NativeName from the runtime lifecycle mapping.",
				nil,
			)
		}
		if _, duplicate := seenNames[binding.NativeName]; duplicate {
			return waist.L1{}, nil, bindError(
				where,
				fmt.Sprintf("%s event %q binding %d repeats native field %q.", host.Label, mapping.NativeName(), index, binding.NativeName),
				"Each declared native identity must be supplied at most once.",
				"The frontend rejected the binding and returned no identities.",
				"Supply one value for each native identity field.",
				nil,
			)
		}
		seenNames[binding.NativeName] = struct{}{}

		if uint8(binding.Kind) != uint8(field.Kind()) {
			return waist.L1{}, nil, bindError(
				where,
				fmt.Sprintf("%s event %q binding %d classifies native field %q as kind %d, but the runtime mapping declares kind %d.", host.Label, mapping.NativeName(), index, binding.NativeName, uint8(binding.Kind), uint8(field.Kind())),
				"The occurrence binding and semantic runtime mapping must agree on the numeric typed identity kind without a second conversion table.",
				"The frontend rejected the binding and returned no identities.",
				"Use the model binding kind whose numeric value matches the pinned runtime identity kind.",
				nil,
			)
		}

		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), binding.Value)
		if err != nil {
			return waist.L1{}, nil, bindError(
				where,
				fmt.Sprintf("%s event %q binding %d for native field %q has an invalid value.", host.Label, mapping.NativeName(), index, binding.NativeName),
				"The waist validates native identity values before they can be used for semantic correlation.",
				"The frontend rejected the binding and returned no identities.",
				"Forward the exact non-empty UTF-8 native identifier within the waist size limit.",
				err,
			)
		}
		identities = append(identities, identity)
	}
	return l1, identities, nil
}

func bindError(where, what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    where,
		Impact:   impact,
		Fix:      fix,
		Cause:    cause,
	}
}

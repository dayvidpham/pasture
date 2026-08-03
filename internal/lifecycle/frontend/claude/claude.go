// Package claude converts the pinned Claude capture vocabulary into the
// target-neutral lifecycle waist.
package claude

import (
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// claudeEventMappings is the sole Claude frontend event mapping. Keep every
// generated model ordinal explicit here: the registration and runtime
// enumerations are separate contracts even though their current ordinals agree.
var claudeEventMappings = map[model.ContractEventKind]runtime.ClaudeLifecycleEvent{
	registration.EventSessionStart:        runtime.ClaudeEventSessionStart,
	registration.EventSetup:               runtime.ClaudeEventSetup,
	registration.EventSessionEnd:          runtime.ClaudeEventSessionEnd,
	registration.EventUserPromptSubmit:    runtime.ClaudeEventUserPromptSubmit,
	registration.EventUserPromptExpansion: runtime.ClaudeEventUserPromptExpansion,
	registration.EventStop:                runtime.ClaudeEventStop,
	registration.EventStopFailure:         runtime.ClaudeEventStopFailure,
	registration.EventPreToolUse:          runtime.ClaudeEventPreToolUse,
	registration.EventPermissionRequest:   runtime.ClaudeEventPermissionRequest,
	registration.EventPermissionDenied:    runtime.ClaudeEventPermissionDenied,
	registration.EventPostToolUse:         runtime.ClaudeEventPostToolUse,
	registration.EventPostToolUseFailure:  runtime.ClaudeEventPostToolUseFailure,
	registration.EventPostToolBatch:       runtime.ClaudeEventPostToolBatch,
	registration.EventFileChanged:         runtime.ClaudeEventFileChanged,
	registration.EventCwdChanged:          runtime.ClaudeEventCwdChanged,
	registration.EventConfigChange:        runtime.ClaudeEventConfigChange,
	registration.EventInstructionsLoaded:  runtime.ClaudeEventInstructionsLoaded,
	registration.EventWorktreeCreate:      runtime.ClaudeEventWorktreeCreate,
	registration.EventWorktreeRemove:      runtime.ClaudeEventWorktreeRemove,
	registration.EventSubagentStart:       runtime.ClaudeEventSubagentStart,
	registration.EventSubagentStop:        runtime.ClaudeEventSubagentStop,
	registration.EventTeammateIdle:        runtime.ClaudeEventTeammateIdle,
	registration.EventTaskCreated:         runtime.ClaudeEventTaskCreated,
	registration.EventTaskCompleted:       runtime.ClaudeEventTaskCompleted,
	registration.EventPreCompact:          runtime.ClaudeEventPreCompact,
	registration.EventPostCompact:         runtime.ClaudeEventPostCompact,
	registration.EventNotification:        runtime.ClaudeEventNotification,
	registration.EventMessageDisplay:      runtime.ClaudeEventMessageDisplay,
	registration.EventElicitation:         runtime.ClaudeEventElicitation,
	registration.EventElicitationResult:   runtime.ClaudeEventElicitationResult,
}

// Bind is the pure Claude frontend boundary. It binds the typed native event to
// the pinned semantic lifecycle profile and converts only the supplied native
// identities; it never performs persistence or constructs an L2 value.
func Bind(modelKind model.ContractEventKind, bindings []model.NativeBinding) (waist.L1, []waist.Identity, error) {
	runtimeKind, ok := claudeEventMappings[modelKind]
	if !ok {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Claude lifecycle event ordinal %d is not declared.", modelKind),
			"The frontend accepts only the closed event ordinals emitted by the pinned Claude registration.",
			"The native event cannot enter the lifecycle waist.",
			"Use one of the generated Claude event ordinals from the registration contract.",
			nil,
		)
	}

	contract := runtime.ClaudeCode2_1_210Lifecycle()
	mapping, err := contract.Mapping(runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Claude lifecycle event ordinal %d has no pinned runtime mapping.", modelKind),
			"Every frontend ordinal must select one reviewed semantic lifecycle mapping.",
			"The native event cannot enter the lifecycle waist.",
			"Repair the pinned Claude runtime profile before accepting this event.",
			err,
		)
	}
	l1, err := waist.BindEvent(contract, runtimeKind)
	if err != nil {
		return waist.L1{}, nil, bindError(
			fmt.Sprintf("Claude lifecycle event %q could not be bound to the pinned runtime profile.", mapping.NativeName()),
			"The waist requires a constructor-built event binding from the reviewed lifecycle contract.",
			"No native event or identities were returned.",
			"Use the matching pinned Claude lifecycle profile and generated event ordinal.",
			err,
		)
	}

	declared := mapping.Identities()
	seenNames := make(map[string]struct{}, len(bindings))
	identities := make([]waist.Identity, 0, len(bindings))
	for index, binding := range bindings {
		field, found := findDeclaredField(declared, binding.NativeName)
		if !found {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Claude event %q binding %d names undeclared native field %q.", mapping.NativeName(), index, binding.NativeName),
				"Only exact native identity names declared by the pinned runtime mapping may cross into the waist.",
				"The frontend rejected the binding and returned no identities.",
				"Use the exact NativeName from the runtime lifecycle mapping.",
				nil,
			)
		}
		if _, duplicate := seenNames[binding.NativeName]; duplicate {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Claude event %q binding %d repeats native field %q.", mapping.NativeName(), index, binding.NativeName),
				"Each declared native identity must be supplied at most once.",
				"The frontend rejected the binding and returned no identities.",
				"Supply one value for each native identity field.",
				nil,
			)
		}
		seenNames[binding.NativeName] = struct{}{}

		if uint8(binding.Kind) != uint8(field.Kind()) {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Claude event %q binding %d classifies native field %q as kind %d, but the runtime mapping declares kind %d.", mapping.NativeName(), index, binding.NativeName, uint8(binding.Kind), uint8(field.Kind())),
				"The occurrence binding and semantic runtime mapping must agree on the numeric typed identity kind without a second conversion table.",
				"The frontend rejected the binding and returned no identities.",
				"Use the model binding kind whose numeric value matches the pinned runtime identity kind.",
				nil,
			)
		}

		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), binding.Value)
		if err != nil {
			return waist.L1{}, nil, bindError(
				fmt.Sprintf("Claude event %q binding %d for native field %q has an invalid value.", mapping.NativeName(), index, binding.NativeName),
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

func findDeclaredField(declared []runtime.NativeIdentityField, nativeName string) (runtime.NativeIdentityField, bool) {
	for _, field := range declared {
		if field.NativeName() == nativeName {
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
		Where:    "Binding a Claude lifecycle event (internal/lifecycle/frontend/claude/claude.go in claude.Bind).",
		Impact:   impact,
		Fix:      fix,
		Cause:    cause,
	}
}

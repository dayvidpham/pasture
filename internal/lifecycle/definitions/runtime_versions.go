package definitions

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// runtimeSpecificationV1 freezes the released registration wire shape. In
// particular, future registration fields must not become accepted v1 JSON just
// because the active Go manifest grows them. Historical semantic references
// resolve through this decoder without comparison to today's source inventory.
type runtimeSpecificationV1 struct {
	Contract            ir.RuntimeContractID                  `json:"contract"`
	Harness             ir.HarnessID                          `json:"harness"`
	Minimum             string                                `json:"minimum"`
	Maximum             string                                `json:"maximum"`
	HasUpperBound       bool                                  `json:"has_upper_bound"`
	IncludesPrereleases bool                                  `json:"includes_prereleases"`
	NativeTransport     string                                `json:"native_transport"`
	Operations          []pastureruntime.BindingDescriptor    `json:"operations"`
	Effects             []pastureruntime.BindingDescriptor    `json:"effects"`
	Capabilities        []pastureruntime.CapabilityDescriptor `json:"capabilities"`
	Registration        runtimeRegistrationV1                 `json:"registration"`
	Lifecycle           []LifecycleDescriptor                 `json:"lifecycle"`
}

type runtimeRegistrationV1 struct {
	Harness  ir.HarnessID                 `json:"Harness"`
	Version  string                       `json:"Version"`
	Contract ir.RuntimeContractID         `json:"Contract"`
	Events   []runtimeRegistrationEventV1 `json:"Events"`
}

type runtimeRegistrationEventV1 struct {
	Kind          model.ContractEventKind     `json:"Kind"`
	NativeName    string                      `json:"NativeName"`
	AllowedFields []model.NativeFieldID       `json:"AllowedFields"`
	Identities    []registration.Identity     `json:"Identities"`
	Blocking      registration.BlockingMode   `json:"Blocking"`
	Mutation      registration.MutationMode   `json:"Mutation"`
	Failure       pastureruntime.FailureMode  `json:"Failure"`
	StopLoop      registration.StopLoopPolicy `json:"StopLoop"`
}

func validateRuntimeV1Shape(payload []byte) error {
	var historical struct {
		Kind          model.DefinitionKind   `json:"kind"`
		ID            model.DefinitionID     `json:"id"`
		Version       uint32                 `json:"version"`
		Codec         model.DefinitionCodec  `json:"codec"`
		Specification runtimeSpecificationV1 `json:"specification"`
	}
	if err := strictDecode(payload, &historical); err != nil {
		return failure("decodeRuntimeV1", "The historical runtime v1 document contains malformed, duplicate or unknown fields; keep its released wire shape and use a newer document version for new metadata.", err)
	}
	return nil
}

func validateRuntimeV2Transitions(spec RuntimeSpecification) error {
	for _, event := range spec.Registration.Events {
		switch event.SessionTransition {
		case model.SessionTransitionNone, model.SessionTransitionPrompt, model.SessionTransitionEnd:
			if event.TransitionSourceField != "" {
				return failure("decodeRuntimeV2", "A non-resume session classification carries a source predicate; regenerate the typed runtime document.", nil)
			}
			if event.SessionTransition == model.SessionTransitionPrompt && !((spec.Harness == ir.HarnessOpenCode && event.NativeName == "session.prompt") || ((spec.Harness == ir.HarnessClaudeCode || spec.Harness == ir.HarnessCodex) && event.NativeName == "UserPromptSubmit")) {
				return failure("decodeRuntimeV2", "The prompt classification is not a supported native prompt event; regenerate the typed runtime document.", nil)
			}
			if event.SessionTransition == model.SessionTransitionEnd && (event.NativeName != "SessionEnd" || (spec.Harness != ir.HarnessClaudeCode && spec.Harness != ir.HarnessCodex)) {
				return failure("decodeRuntimeV2", "The end classification is not a supported native session-end event; regenerate the typed runtime document.", nil)
			}
		case model.SessionTransitionResume:
			if event.NativeName != "SessionStart" || event.TransitionSourceField != "source" || (spec.Harness != ir.HarnessClaudeCode && spec.Harness != ir.HarnessCodex) {
				return failure("decodeRuntimeV2", "The resume classification is not an exact supported SessionStart source predicate; regenerate the typed runtime document.", nil)
			}
		default:
			return failure("decodeRuntimeV2", "The runtime session classification is unknown; upgrade Pasture or regenerate a supported document.", nil)
		}
	}
	return nil
}

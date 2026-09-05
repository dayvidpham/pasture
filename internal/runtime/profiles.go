package runtime

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// OrchestrationRequest is the typed request shape the pinned core operation
// descriptors carry. It is deliberately simple and JSON-round-trippable so a
// native binding's fixtures validate a full request shape, not a paraphrase.
type OrchestrationRequest struct {
	Operation string            `json:"operation"`
	Arguments map[string]string `json:"arguments"`
}

// OrchestrationResult is the typed result shape the pinned core operation
// descriptors carry.
type OrchestrationResult struct {
	Status  string            `json:"status"`
	Handles map[string]string `json:"handles"`
}

// CoreOperationDescriptor is the shared typed descriptor kind for every pinned
// core operation. Every profile classifies exactly this descriptor set, so a
// caller looks operations up with the same descriptors the contracts registered.
type CoreOperationDescriptor = ir.OperationDescriptor[OrchestrationRequest, OrchestrationResult]

func mustCoreDescriptor(kind ir.OperationKind, summary, result string) CoreOperationDescriptor {
	id, ok := ir.CoreOperationID(kind)
	if !ok {
		panic(fmt.Sprintf("runtime: %q is not a core operation kind", kind))
	}
	inSchema := ir.SchemaID(id.String() + ".request/v1")
	outSchema := ir.SchemaID(id.String() + ".result/v1")
	input, err := ir.NewJSONCodec[OrchestrationRequest](inSchema, nil)
	if err != nil {
		panic(err)
	}
	output, err := ir.NewJSONCodec[OrchestrationResult](outSchema, nil)
	if err != nil {
		panic(err)
	}
	descriptor, err := ir.NewOperationDescriptor(id, input, output, ir.DescriptorSemantics{
		Summary: summary, Result: result,
	}, mustEmptyEffectSet())
	if err != nil {
		panic(err)
	}
	return descriptor
}

func mustEmptyEffectSet() ir.EffectSet {
	set, err := ir.NewEffectSet()
	if err != nil {
		panic(err)
	}
	return set
}

// coreOperationDescriptors is the single fixed descriptor table every pinned
// contract classifies. It is built once so profiles and their fixtures share
// one descriptor identity per operation.
var coreOperationDescriptors = map[ir.OperationKind]CoreOperationDescriptor{
	ir.OperationInvokeSkill:              mustCoreDescriptor(ir.OperationInvokeSkill, "invoke a reviewed protocol skill", "the skill's typed result"),
	ir.OperationDelegateAssignment:       mustCoreDescriptor(ir.OperationDelegateAssignment, "delegate one or more assignments to child agents", "handles for the delegated assignments"),
	ir.OperationContinueAssignment:       mustCoreDescriptor(ir.OperationContinueAssignment, "continue an existing assignment on a fresh or resumed agent", "the continued assignment handle"),
	ir.OperationSendAssignmentMessage:    mustCoreDescriptor(ir.OperationSendAssignmentMessage, "send a message to an assignment", "message delivery acknowledgement"),
	ir.OperationCollectAssignmentResults: mustCoreDescriptor(ir.OperationCollectAssignmentResults, "collect the results of delegated assignments", "the gathered assignment results"),
	ir.OperationStopAssignment:           mustCoreDescriptor(ir.OperationStopAssignment, "stop one or more running assignments", "stop acknowledgement"),
	ir.OperationRequestUserDecision:      mustCoreDescriptor(ir.OperationRequestUserDecision, "request a bounded user decision", "the reported user decision"),
}

// CoreOperationDescriptorFor returns the shared descriptor for a core operation
// kind, so callers look bindings up with the exact descriptor the pinned
// contracts registered.
func CoreOperationDescriptorFor(kind ir.OperationKind) (CoreOperationDescriptor, bool) {
	descriptor, ok := coreOperationDescriptors[kind]
	return descriptor, ok
}

// operationLowering is a static, declarative classification for one core
// operation on one harness.
type operationLowering struct {
	class    effects.RuntimeClass
	native   NativeCall
	mediated MediatedLowering
	semantic SemanticLowering
	reason   string
}

func mustNativeCall(name string, arguments []string, result, context string) NativeCall {
	call, err := NewNativeCall(name, arguments, result, context)
	if err != nil {
		panic(err)
	}
	return call
}

func mustMediated(mediator, instruction string) MediatedLowering {
	lowering, err := NewMediatedLowering(mediator, instruction)
	if err != nil {
		panic(err)
	}
	return lowering
}

func mustSemantic(template string) SemanticLowering {
	lowering, err := NewSemanticLowering(template)
	if err != nil {
		panic(err)
	}
	return lowering
}

func buildCoreBindings(table map[ir.OperationKind]operationLowering) CoreRuntimeBindings {
	bindings := make([]OperationBinding, 0, len(table))
	for kind, lowering := range table {
		descriptor, ok := coreOperationDescriptors[kind]
		if !ok {
			panic(fmt.Sprintf("runtime: lowering references unknown core kind %q", kind))
		}
		binding, err := lowering.toOperationBinding(descriptor)
		if err != nil {
			panic(err)
		}
		bindings = append(bindings, binding)
	}
	core, err := NewCoreRuntimeBindings(bindings, nil)
	if err != nil {
		panic(err)
	}
	return core
}

func (l operationLowering) toOperationBinding(descriptor CoreOperationDescriptor) (OperationBinding, error) {
	switch l.class {
	case effects.RuntimeClassNative:
		return NativeOperationBinding(descriptor, l.native)
	case effects.RuntimeClassParentMediated:
		return MediatedOperationBinding(descriptor, l.mediated)
	case effects.RuntimeClassSemanticInstruction:
		return SemanticOperationBinding(descriptor, l.semantic)
	case effects.RuntimeClassUnsupported:
		return UnsupportedOperationBinding(descriptor, l.reason)
	default:
		return OperationBinding{}, fmt.Errorf("runtime: unclassified lowering for operation %q", descriptor.ID())
	}
}

// productionHostVersion returns the host version the harness's production
// runtime contract id records (artifact.ProductionRuntimeContract), the ONE
// root every version a profile states or admits is read from. A profile never
// writes a version by hand: prose formats this value, and admission is a floor
// at it.
func productionHostVersion(harness ir.HarnessID) HostVersion {
	id, err := artifact.ProductionRuntimeContract(harness)
	if err != nil {
		panic(err)
	}
	_, version, ok := strings.Cut(id.String(), "@")
	if !ok {
		panic("production runtime contract has no version")
	}
	host, err := ParseHostVersion(version)
	if err != nil {
		panic(err)
	}
	return host
}

// mustFloorContract builds a harness's runtime contract with admission "at or
// above the recorded version": the recorded version is the one root, the host
// may move to any later release, and a host below the recorded version is
// refused (NewVersionFloor). Every harness uses this one shape.
func mustFloorContract(harness ir.HarnessID, core CoreRuntimeBindings) RuntimeContract {
	id, err := artifact.ProductionRuntimeContract(harness)
	if err != nil {
		panic(err)
	}
	constraint, err := NewVersionFloor(productionHostVersion(harness))
	if err != nil {
		panic(err)
	}
	contract, err := NewRuntimeContract(id, harness, constraint, core)
	if err != nil {
		panic(err)
	}
	return contract
}

// ClaudeCode2_1_261 is the runtime contract for Claude Code at the recorded
// host version, the one its id carries (artifact.ProductionRuntimeContract).
// Admission is a floor: that version and every later release. Its native
// bindings name only Agent/SendMessage/TaskStop-era tools; it never names a
// removed team-lifecycle call (TeamCreate/TeamDelete). Any schema or semantic
// change to this profile requires a new RuntimeContractID.
func ClaudeCode2_1_261() RuntimeContract {
	table := map[ir.OperationKind]operationLowering{
		ir.OperationInvokeSkill: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("Skill", []string{"command", "arguments"}, "the skill's structured result on the tool result channel", "runs in the invoking agent's context"),
		},
		ir.OperationDelegateAssignment: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("Agent", []string{"description", "prompt", "subagent_type"}, "a spawned agent handle whose result returns on completion", "child inherits the delegated assignment context only"),
		},
		ir.OperationContinueAssignment: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("SendMessage", []string{"to", "message"}, "resumes the addressed agent from its retained transcript", "resumes the target assignment's own context"),
		},
		ir.OperationSendAssignmentMessage: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("SendMessage", []string{"to", "summary", "message"}, "delivery acknowledgement", "delivers into the target assignment's context"),
		},
		ir.OperationCollectAssignmentResults: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent gathers each delegated Agent result as it completes; this contract binds no native batch-wait call"),
		},
		ir.OperationStopAssignment: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("TaskStop", []string{"assignment", "reason"}, "stop acknowledgement for the addressed assignment", "targets the addressed assignment"),
		},
		ir.OperationRequestUserDecision: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("AskUserQuestion", []string{"questions"}, "the user's selected option bound to the originating request", "presents to the interactive user"),
		},
	}
	return mustFloorContract(ir.HarnessClaudeCode, buildCoreBindings(table))
}

// OpenCode1_18_29 is the runtime contract for OpenCode at the recorded host
// version, the one its id carries (artifact.ProductionRuntimeContract).
// Admission is a floor: that version and every later release. It uses only
// OpenCode's documented skill/task/question surfaces. It never invents a
// persistent-message, follow-up, wait, or close tool: operations with no
// documented native surface are lowered as semantic instructions, and stopping
// an assignment is explicitly unsupported rather than a fabricated close call.
func OpenCode1_18_29() RuntimeContract {
	table := map[ir.OperationKind]operationLowering{
		ir.OperationInvokeSkill: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("skill", []string{"name", "arguments"}, "the skill's result", "runs in the invoking agent's context"),
		},
		ir.OperationDelegateAssignment: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("task", []string{"description", "prompt"}, "the spawned task's result on completion", "child receives the delegated assignment context"),
		},
		ir.OperationContinueAssignment: {
			class:    effects.RuntimeClassSemanticInstruction,
			semantic: mustSemantic("this contract binds no native follow-up call: reconstruct the assignment as a fresh task with its complete retained role, evidence, decisions, and outstanding work"),
		},
		ir.OperationSendAssignmentMessage: {
			class:    effects.RuntimeClassSemanticInstruction,
			semantic: mustSemantic("this contract binds no native persistent-message call: carry the message content into the next task prompt for the target assignment"),
		},
		ir.OperationCollectAssignmentResults: {
			class:    effects.RuntimeClassSemanticInstruction,
			semantic: mustSemantic("this contract binds no native wait call: collect each task result inline as tasks return"),
		},
		ir.OperationStopAssignment: {
			class:  effects.RuntimeClassUnsupported,
			reason: "this contract binds no native close or stop call; stopping a running task has no modeled native semantics and must not be lowered to a fabricated close call",
		},
		ir.OperationRequestUserDecision: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("question", []string{"prompt", "options"}, "the user's selected option bound to the originating request", "presents to the interactive user"),
		},
	}
	return mustFloorContract(ir.HarnessOpenCode, buildCoreBindings(table))
}

// Codex0_153_0 is the runtime contract for Codex at the recorded host version,
// the one its id carries (artifact.ProductionRuntimeContract). Admission is a
// floor: that version and every later release. It binds a native call only
// where this contract declares one; every other operation is parent-mediated or
// lowered as a semantic instruction rather than invented. The instruction texts
// state what THIS CONTRACT binds; they make no claim about what the host does
// or does not expose, because a claim about the host cannot be held true by a
// version bump.
func Codex0_153_0() RuntimeContract {
	table := map[ir.OperationKind]operationLowering{
		ir.OperationInvokeSkill: {
			class:    effects.RuntimeClassSemanticInstruction,
			semantic: mustSemantic("this contract binds no native skill call: perform the skill's steps directly following its reviewed protocol instructions"),
		},
		ir.OperationDelegateAssignment: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent drives Codex delegation over the collaboration surface; this contract binds no native agent-spawn call"),
		},
		ir.OperationContinueAssignment: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent resumes the Codex assignment with its complete reconstructed context"),
		},
		ir.OperationSendAssignmentMessage: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent relays the message over the Codex collaboration surface"),
		},
		ir.OperationCollectAssignmentResults: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent collects Codex assignment results over the collaboration surface"),
		},
		ir.OperationStopAssignment: {
			class:    effects.RuntimeClassParentMediated,
			mediated: mustMediated("parent orchestrator", "the parent stops the Codex assignment; this contract binds no native stop call"),
		},
		ir.OperationRequestUserDecision: {
			class:  effects.RuntimeClassNative,
			native: mustNativeCall("request-input", []string{"prompt", "options"}, "the user's requested input bound to the originating request", "presents to the interactive user"),
		},
	}
	return mustFloorContract(ir.HarnessCodex, buildCoreBindings(table))
}

// PinnedContracts returns the three runtime contracts, one per enabled harness,
// each admitting its recorded host version and every later release.
func PinnedContracts() []RuntimeContract {
	return []RuntimeContract{ClaudeCode2_1_261(), OpenCode1_18_29(), Codex0_153_0()}
}

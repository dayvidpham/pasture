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

// mustFrozenFloorContract builds a historical runtime contract whose identity
// and floor are fixed at a recorded version instead of the production root. It
// exists for a superseded profile that stays reachable for older hosts; the
// production profile of every harness is built by mustFloorContract.
func mustFrozenFloorContract(harness ir.HarnessID, contractID, version string, core CoreRuntimeBindings) RuntimeContract {
	host, err := ParseHostVersion(version)
	if err != nil {
		panic(err)
	}
	constraint, err := NewVersionFloor(host)
	if err != nil {
		panic(err)
	}
	id, err := ir.NewRuntimeContractID(harness, contractID)
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

// OpenCode1_18_29 is the historical runtime contract for OpenCode 1.18.29. It
// is NOT the production profile: its identity (opencode@1.18.29) and its floor
// (1.18.29) are frozen here rather than read from
// artifact.ProductionRuntimeContract, so it stays the contract the v1 dispatch
// row and the recorded 1.18.29 corpus evidence were proved against after the
// production profile moved to OpenCode2_0_20. It uses only OpenCode's
// documented skill/task/question surfaces. It never invents a
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
	return mustFrozenFloorContract(ir.HarnessOpenCode, "opencode@1.18.29", "1.18.29", buildCoreBindings(table))
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
			semantic: mustSemantic("this contract binds no native skill call: perform the skill's steps directly following its protocol instructions"),
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
	return []RuntimeContract{ClaudeCode2_1_261(), OpenCode2_0_20(), Codex0_153_0()}
}

// OpenCode2_0_20 is the production runtime contract for OpenCode, at the
// recorded host version its id carries (artifact.ProductionRuntimeContract).
// Admission is a floor: that version and every later release. It classifies the
// same core operation surface as the historical 1.18.29 contract: the v2 plugin
// API replaces hook registration while the native skill, task and question
// tools keep their names, so the operation lowerings are unchanged.
func OpenCode2_0_20() RuntimeContract {
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

// HarnessToolLowering is the native resolution of one ir.HarnessTool on one
// harness. A harness either declares the tool natively (Available is true and
// NativeName names the exact host spelling) or it has no per-agent declaration
// for that tool (Available is false and DegradeInstruction says what the
// emitter must do instead). Exactly one of the two is set, so a caller cannot
// read a native name from an unavailable lowering or invent one.
type HarnessToolLowering struct {
	native    string
	available bool
	degrade   string
}

func (l HarnessToolLowering) NativeName() string { return l.native }
func (l HarnessToolLowering) Available() bool    { return l.available }
func (l HarnessToolLowering) DegradeInstruction() string {
	return l.degrade
}

// IsValid reports whether the lowering is exactly one of the two documented
// shapes: an available native name, or an unavailable degradation instruction.
func (l HarnessToolLowering) IsValid() bool {
	if l.available {
		return strings.TrimSpace(l.native) != "" && l.degrade == ""
	}
	return l.native == "" && strings.TrimSpace(l.degrade) != ""
}

func nativeHarnessTool(name string) HarnessToolLowering {
	return HarnessToolLowering{native: name, available: true}
}

func unavailableHarnessTool(reason string) HarnessToolLowering {
	return HarnessToolLowering{degrade: reason}
}

// harnessToolLowerings is the ONE table that maps the closed ir.HarnessTool set
// to each harness's native spelling. The code emitters resolve every role tool
// through it; no other file writes a native file/shell tool name.
//
// Codex declares no per-agent tool list in a custom-agent profile, so every
// HarnessTool is unavailable there and carries the degradation instruction the
// Codex emitter already follows: the host grants the parent's tool surface to
// every agent.
var harnessToolLowerings = map[ir.HarnessID]map[ir.HarnessTool]HarnessToolLowering{
	ir.HarnessClaudeCode: {
		ir.HarnessToolRead:     nativeHarnessTool("Read"),
		ir.HarnessToolGlob:     nativeHarnessTool("Glob"),
		ir.HarnessToolGrep:     nativeHarnessTool("Grep"),
		ir.HarnessToolBash:     nativeHarnessTool("Bash"),
		ir.HarnessToolEdit:     nativeHarnessTool("Edit"),
		ir.HarnessToolWrite:    nativeHarnessTool("Write"),
		ir.HarnessToolTask:     nativeHarnessTool("Task"),
		ir.HarnessToolWebFetch: nativeHarnessTool("WebFetch"),
	},
	ir.HarnessOpenCode: {
		ir.HarnessToolRead:     nativeHarnessTool("read"),
		ir.HarnessToolGlob:     nativeHarnessTool("glob"),
		ir.HarnessToolGrep:     nativeHarnessTool("grep"),
		ir.HarnessToolBash:     nativeHarnessTool("bash"),
		ir.HarnessToolEdit:     nativeHarnessTool("edit"),
		ir.HarnessToolWrite:    nativeHarnessTool("write"),
		ir.HarnessToolTask:     nativeHarnessTool("task"),
		ir.HarnessToolWebFetch: nativeHarnessTool("webfetch"),
	},
	ir.HarnessCodex: {
		ir.HarnessToolRead:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolGlob:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolGrep:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolBash:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolEdit:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolWrite:    unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolTask:     unavailableHarnessTool(codexNoPerAgentToolList),
		ir.HarnessToolWebFetch: unavailableHarnessTool(codexNoPerAgentToolList),
	},
}

const codexNoPerAgentToolList = "the Codex custom-agent profile declares no per-agent tool list; the host grants the parent's tool surface to every agent, so the emitter writes no native tool name"

// LookupHarnessTool resolves one HarnessTool on one harness. It accepts only a
// registered harness and a registered tool; an unregistered operand returns an
// actionable error instead of an empty lowering.
func LookupHarnessTool(harness ir.HarnessID, tool ir.HarnessTool) (HarnessToolLowering, error) {
	table, knownHarness := harnessToolLowerings[harness]
	if !knownHarness {
		return HarnessToolLowering{}, runtimeError(
			fmt.Sprintf("harness %q has no harness-tool lowering table", harness),
			"only the pinned harnesses declare native file and shell tool names",
			"LookupHarnessTool", "the caller cannot resolve a tool name",
			"resolve a harness listed in PinnedContracts", nil,
		)
	}
	if !tool.IsValid() {
		return HarnessToolLowering{}, runtimeError(
			fmt.Sprintf("harness tool %q is not registered", tool),
			"the harness-tool set is closed; an unregistered value has no native spelling",
			"LookupHarnessTool", "the caller cannot resolve a tool name",
			"use one of ir.AllHarnessTools", nil,
		)
	}
	lowering, found := table[tool]
	if !found {
		return HarnessToolLowering{}, runtimeError(
			fmt.Sprintf("harness %q has no lowering for registered harness tool %q", harness, tool),
			"every registered tool must be classified on every pinned harness",
			"LookupHarnessTool", "the emitter would otherwise write a wrong or missing tool name",
			"add the harness/tool row to harnessToolLowerings", nil,
		)
	}
	if !lowering.IsValid() {
		return HarnessToolLowering{}, runtimeError(
			fmt.Sprintf("harness %q harness-tool lowering for %q is malformed", harness, tool),
			"a lowering is either an available native name or an unavailable degradation instruction",
			"LookupHarnessTool", "the emitter cannot trust the row",
			"fix the harnessToolLowerings row", nil,
		)
	}
	return lowering, nil
}

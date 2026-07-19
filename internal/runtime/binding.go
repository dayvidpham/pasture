package runtime

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// NativeCall is the complete native lowering a native binding owns: the exact
// host call name, its declared argument names, the result/handle semantics, and
// how the call inherits assignment context. It is opaque and
// constructor-validated so a native binding can never carry an empty call name
// or an unnamed result contract.
type NativeCall struct {
	callName           string
	arguments          []string
	resultSemantics    string
	contextInheritance string
	constructed        bool
}

// NewNativeCall validates and constructs a native call lowering. callName and
// resultSemantics are required; arguments and contextInheritance describe the
// full request/result dataflow the native fixture must reproduce.
func NewNativeCall(callName string, arguments []string, resultSemantics, contextInheritance string) (NativeCall, error) {
	if strings.TrimSpace(callName) == "" {
		return NativeCall{}, runtimeError(
			"native call has an empty call name",
			"a native binding must name the exact host runtime call it lowers to",
			"NewNativeCall", "the native lowering cannot be rendered or fixtured",
			"supply the exact native call name", nil,
		)
	}
	if strings.TrimSpace(resultSemantics) == "" {
		return NativeCall{}, runtimeError(
			fmt.Sprintf("native call %q has empty result semantics", callName),
			"a native binding owns the complete result/handle meaning, not just the call name",
			"NewNativeCall", "callers could not interpret the native result",
			"describe the native call's result and handle semantics", nil,
		)
	}
	owned := make([]string, 0, len(arguments))
	seen := make(map[string]struct{}, len(arguments))
	for index, argument := range arguments {
		if strings.TrimSpace(argument) == "" {
			return NativeCall{}, runtimeError(
				fmt.Sprintf("native call %q argument %d is empty", callName, index),
				"every declared native argument must be nameable",
				"NewNativeCall", "the native request shape would be ambiguous",
				"remove empty argument names", nil,
			)
		}
		if _, duplicate := seen[argument]; duplicate {
			return NativeCall{}, runtimeError(
				fmt.Sprintf("native call %q argument %q is duplicated", callName, argument),
				"one native request field cannot be declared twice",
				"NewNativeCall", "the native request shape would be ambiguous",
				"declare each argument once", nil,
			)
		}
		seen[argument] = struct{}{}
		owned = append(owned, argument)
	}
	return NativeCall{
		callName: callName, arguments: owned,
		resultSemantics: resultSemantics, contextInheritance: contextInheritance,
		constructed: true,
	}, nil
}

func (n NativeCall) CallName() string           { return n.callName }
func (n NativeCall) Arguments() []string        { return append([]string(nil), n.arguments...) }
func (n NativeCall) ResultSemantics() string    { return n.resultSemantics }
func (n NativeCall) ContextInheritance() string { return n.contextInheritance }
func (n NativeCall) IsValid() bool              { return n.constructed }

// MediatedLowering is the lowering a parent-mediated binding owns: the mediator
// that executes the effect on the agent's behalf, plus the exact instruction it
// carries out. A mediated binding proves semantic parity by carrying the
// descriptor's own preconditions, postconditions, result, and effect set
// (copied at construction), never a paraphrase.
type MediatedLowering struct {
	mediator    string
	instruction string
	constructed bool
}

// NewMediatedLowering validates and constructs a parent-mediated lowering.
func NewMediatedLowering(mediator, instruction string) (MediatedLowering, error) {
	if strings.TrimSpace(mediator) == "" {
		return MediatedLowering{}, runtimeError(
			"mediated lowering has an empty mediator",
			"a parent-mediated binding must name who performs the effect",
			"NewMediatedLowering", "the mediated lowering cannot be executed",
			"name the mediator (for example the parent orchestrator)", nil,
		)
	}
	if strings.TrimSpace(instruction) == "" {
		return MediatedLowering{}, runtimeError(
			"mediated lowering has an empty instruction",
			"the mediator must carry out an exact instruction",
			"NewMediatedLowering", "the mediated lowering cannot be executed",
			"supply the mediation instruction", nil,
		)
	}
	return MediatedLowering{mediator: mediator, instruction: instruction, constructed: true}, nil
}

func (m MediatedLowering) Mediator() string    { return m.mediator }
func (m MediatedLowering) Instruction() string { return m.instruction }
func (m MediatedLowering) IsValid() bool       { return m.constructed }

// SemanticLowering is the lowering a semantic-instruction binding owns: the
// instruction template the agent must carry out. Like a mediated binding, it
// proves parity by carrying the descriptor's own semantics and effect set.
type SemanticLowering struct {
	instructionTemplate string
	constructed         bool
}

// NewSemanticLowering validates and constructs a semantic-instruction lowering.
func NewSemanticLowering(instructionTemplate string) (SemanticLowering, error) {
	if strings.TrimSpace(instructionTemplate) == "" {
		return SemanticLowering{}, runtimeError(
			"semantic lowering has an empty instruction template",
			"a semantic-instruction binding must tell the agent exactly what to do",
			"NewSemanticLowering", "the semantic lowering cannot be rendered",
			"supply the semantic instruction template", nil,
		)
	}
	return SemanticLowering{instructionTemplate: instructionTemplate, constructed: true}, nil
}

func (s SemanticLowering) InstructionTemplate() string { return s.instructionTemplate }
func (s SemanticLowering) IsValid() bool               { return s.constructed }

// boundEntry is the immutable, type-erased binding the contract stores for one
// descriptor. It retains the concrete Go input/output types and codec schemas so
// a later typed lookup can reject a descriptor whose types or schemas disagree,
// rather than silently returning a mismatched binding.
type boundEntry struct {
	id        string
	class     effects.RuntimeClass
	inType    reflect.Type
	outType   reflect.Type
	inSchema  ir.SchemaID
	outSchema ir.SchemaID
	semantics ir.DescriptorSemantics
	effects   ir.EffectSet
	native    NativeCall
	mediated  MediatedLowering
	semantic  SemanticLowering
	// unsupportedReason is set only when class is RuntimeClassUnsupported; it
	// feeds the actionable lookup error so an unsupported construct fails with a
	// reason instead of collapsing into a zero binding.
	unsupportedReason string
}

func (e boundEntry) matchesTypes(inType, outType reflect.Type, inSchema, outSchema ir.SchemaID) bool {
	return e.inType == inType && e.outType == outType && e.inSchema == inSchema && e.outSchema == outSchema
}

// RuntimeBinding is the typed result of a successful operation or effect
// lookup. It is always one of the three executable classes — native,
// parent-mediated, or semantic-instruction; an unsupported classification never
// yields a binding, it yields an actionable lookup error. The In/Out type
// parameters are re-attached at lookup time from the descriptor, so a binding
// value always agrees with the descriptor that produced it.
type RuntimeBinding[In, Out any] interface {
	runtimeBinding()
	// Class reports the executable runtime class (never unsupported).
	Class() effects.RuntimeClass
	// DescriptorID returns the descriptor identity string. It is metadata only:
	// it is not a lookup operand and cannot be used to fetch a binding.
	DescriptorID() string
	InputSchema() ir.SchemaID
	OutputSchema() ir.SchemaID
	// Semantics returns the preconditions/postconditions/result the binding
	// proves. For mediated and semantic bindings these are the descriptor's own
	// semantics, copied at construction, not a paraphrase.
	Semantics() ir.DescriptorSemantics
	Effects() ir.EffectSet
	// Native, Mediated, and Semantic expose the class-specific lowering. Exactly
	// one returns ok == true, matching Class.
	Native() (NativeCall, bool)
	Mediated() (MediatedLowering, bool)
	Semantic() (SemanticLowering, bool)
}

type runtimeBinding[In, Out any] struct {
	entry boundEntry
}

func (runtimeBinding[In, Out]) runtimeBinding()               {}
func (b runtimeBinding[In, Out]) Class() effects.RuntimeClass { return b.entry.class }
func (b runtimeBinding[In, Out]) DescriptorID() string        { return b.entry.id }
func (b runtimeBinding[In, Out]) InputSchema() ir.SchemaID    { return b.entry.inSchema }
func (b runtimeBinding[In, Out]) OutputSchema() ir.SchemaID   { return b.entry.outSchema }
func (b runtimeBinding[In, Out]) Semantics() ir.DescriptorSemantics {
	return ir.DescriptorSemantics{
		Summary:        b.entry.semantics.Summary,
		Preconditions:  append([]string(nil), b.entry.semantics.Preconditions...),
		Postconditions: append([]string(nil), b.entry.semantics.Postconditions...),
		Result:         b.entry.semantics.Result,
	}
}
func (b runtimeBinding[In, Out]) Effects() ir.EffectSet { return b.entry.effects }
func (b runtimeBinding[In, Out]) Native() (NativeCall, bool) {
	return b.entry.native, b.entry.class == effects.RuntimeClassNative
}
func (b runtimeBinding[In, Out]) Mediated() (MediatedLowering, bool) {
	return b.entry.mediated, b.entry.class == effects.RuntimeClassParentMediated
}
func (b runtimeBinding[In, Out]) Semantic() (SemanticLowering, bool) {
	return b.entry.semantic, b.entry.class == effects.RuntimeClassSemanticInstruction
}

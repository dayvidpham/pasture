package runtime

import (
	"fmt"
	"reflect"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

func typeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// OperationBinding is the opaque, type-erased binding for one semantic
// operation descriptor, produced by the NativeOperationBinding family and
// collected by NewCoreRuntimeBindings. It carries the descriptor's identity,
// codecs, semantics, effects, and its exhaustive runtime classification.
type OperationBinding struct{ entry boundEntry }

// EffectBinding is the opaque, type-erased binding for one typed effect
// descriptor, produced by the NativeEffectBinding family.
type EffectBinding struct{ entry boundEntry }

func (b OperationBinding) id() string                  { return b.entry.id }
func (b OperationBinding) class() effects.RuntimeClass { return b.entry.class }
func (b OperationBinding) valid() bool                 { return b.entry.id != "" && b.entry.class.IsValid() }
func (b EffectBinding) id() string                     { return b.entry.id }
func (b EffectBinding) class() effects.RuntimeClass    { return b.entry.class }
func (b EffectBinding) valid() bool                    { return b.entry.id != "" && b.entry.class.IsValid() }

func operationEntry[In, Out any](
	descriptor ir.OperationDescriptor[In, Out],
	where string,
) (boundEntry, error) {
	if !descriptor.IsValid() {
		return boundEntry{}, runtimeError(
			"operation descriptor is zero or invalid",
			"a runtime binding must lower a constructor-validated typed descriptor",
			where, "the operation cannot be classified",
			"construct the descriptor with ir.NewOperationDescriptor", nil,
		)
	}
	return boundEntry{
		id:        descriptor.ID().String(),
		inType:    typeOf[In](),
		outType:   typeOf[Out](),
		inSchema:  descriptor.InputCodec().Schema(),
		outSchema: descriptor.OutputCodec().Schema(),
		semantics: descriptor.Semantics(),
		effects:   descriptor.Effects(),
	}, nil
}

func effectEntry[In, Out any](
	descriptor ir.EffectDescriptor[In, Out],
	where string,
) (boundEntry, error) {
	if !descriptor.IsValid() {
		return boundEntry{}, runtimeError(
			"effect descriptor is zero or invalid",
			"a runtime binding must lower a constructor-validated typed descriptor",
			where, "the effect cannot be classified",
			"construct the descriptor with ir.NewEffectDescriptor", nil,
		)
	}
	return boundEntry{
		id:        descriptor.ID().String(),
		inType:    typeOf[In](),
		outType:   typeOf[Out](),
		inSchema:  descriptor.InputCodec().Schema(),
		outSchema: descriptor.OutputCodec().Schema(),
		semantics: descriptor.Semantics(),
		effects:   descriptor.Effects(),
	}, nil
}

// NativeOperationBinding classifies a semantic operation as executed directly
// by the host runtime, owning the complete native call, arguments, result
// semantics, and context inheritance.
func NativeOperationBinding[In, Out any](descriptor ir.OperationDescriptor[In, Out], call NativeCall) (OperationBinding, error) {
	entry, err := operationEntry(descriptor, "NativeOperationBinding")
	if err != nil {
		return OperationBinding{}, err
	}
	if !call.IsValid() {
		return OperationBinding{}, runtimeError(
			fmt.Sprintf("native binding for operation %q has an invalid native call", entry.id),
			"a native binding owns a complete validated native call",
			"NativeOperationBinding", "the operation cannot be lowered natively",
			"construct the call with NewNativeCall", nil,
		)
	}
	entry.class = effects.RuntimeClassNative
	entry.native = call
	return OperationBinding{entry: entry}, nil
}

// MediatedOperationBinding classifies a semantic operation as parent-mediated.
func MediatedOperationBinding[In, Out any](descriptor ir.OperationDescriptor[In, Out], lowering MediatedLowering) (OperationBinding, error) {
	entry, err := operationEntry(descriptor, "MediatedOperationBinding")
	if err != nil {
		return OperationBinding{}, err
	}
	if !lowering.IsValid() {
		return OperationBinding{}, runtimeError(
			fmt.Sprintf("mediated binding for operation %q has an invalid lowering", entry.id),
			"a parent-mediated binding must name its mediator and instruction",
			"MediatedOperationBinding", "the operation cannot be mediated",
			"construct the lowering with NewMediatedLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassParentMediated
	entry.mediated = lowering
	return OperationBinding{entry: entry}, nil
}

// SemanticOperationBinding classifies a semantic operation as a
// semantic-instruction the agent must carry out.
func SemanticOperationBinding[In, Out any](descriptor ir.OperationDescriptor[In, Out], lowering SemanticLowering) (OperationBinding, error) {
	entry, err := operationEntry(descriptor, "SemanticOperationBinding")
	if err != nil {
		return OperationBinding{}, err
	}
	if !lowering.IsValid() {
		return OperationBinding{}, runtimeError(
			fmt.Sprintf("semantic binding for operation %q has an invalid lowering", entry.id),
			"a semantic-instruction binding must carry an instruction template",
			"SemanticOperationBinding", "the operation cannot be lowered to an instruction",
			"construct the lowering with NewSemanticLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassSemanticInstruction
	entry.semantic = lowering
	return OperationBinding{entry: entry}, nil
}

// UnsupportedOperationBinding classifies a semantic operation as unsupported on
// this contract. A later lookup fails actionably with reason; it never falls
// through to a similarly named native call.
func UnsupportedOperationBinding[In, Out any](descriptor ir.OperationDescriptor[In, Out], reason string) (OperationBinding, error) {
	entry, err := operationEntry(descriptor, "UnsupportedOperationBinding")
	if err != nil {
		return OperationBinding{}, err
	}
	if reason == "" {
		return OperationBinding{}, runtimeError(
			fmt.Sprintf("unsupported binding for operation %q has no reason", entry.id),
			"an unsupported classification must explain why the construct has no runtime lowering",
			"UnsupportedOperationBinding", "callers could not act on the failure",
			"supply a reason describing why the operation is unsupported", nil,
		)
	}
	entry.class = effects.RuntimeClassUnsupported
	entry.unsupportedReason = reason
	return OperationBinding{entry: entry}, nil
}

// NativeEffectBinding classifies an effect as executed directly by the host.
func NativeEffectBinding[In, Out any](descriptor ir.EffectDescriptor[In, Out], call NativeCall) (EffectBinding, error) {
	entry, err := effectEntry(descriptor, "NativeEffectBinding")
	if err != nil {
		return EffectBinding{}, err
	}
	if !call.IsValid() {
		return EffectBinding{}, runtimeError(
			fmt.Sprintf("native binding for effect %q has an invalid native call", entry.id),
			"a native binding owns a complete validated native call",
			"NativeEffectBinding", "the effect cannot be lowered natively",
			"construct the call with NewNativeCall", nil,
		)
	}
	entry.class = effects.RuntimeClassNative
	entry.native = call
	return EffectBinding{entry: entry}, nil
}

// MediatedEffectBinding classifies an effect as parent-mediated.
func MediatedEffectBinding[In, Out any](descriptor ir.EffectDescriptor[In, Out], lowering MediatedLowering) (EffectBinding, error) {
	entry, err := effectEntry(descriptor, "MediatedEffectBinding")
	if err != nil {
		return EffectBinding{}, err
	}
	if !lowering.IsValid() {
		return EffectBinding{}, runtimeError(
			fmt.Sprintf("mediated binding for effect %q has an invalid lowering", entry.id),
			"a parent-mediated binding must name its mediator and instruction",
			"MediatedEffectBinding", "the effect cannot be mediated",
			"construct the lowering with NewMediatedLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassParentMediated
	entry.mediated = lowering
	return EffectBinding{entry: entry}, nil
}

// SemanticEffectBinding classifies an effect as a semantic instruction.
func SemanticEffectBinding[In, Out any](descriptor ir.EffectDescriptor[In, Out], lowering SemanticLowering) (EffectBinding, error) {
	entry, err := effectEntry(descriptor, "SemanticEffectBinding")
	if err != nil {
		return EffectBinding{}, err
	}
	if !lowering.IsValid() {
		return EffectBinding{}, runtimeError(
			fmt.Sprintf("semantic binding for effect %q has an invalid lowering", entry.id),
			"a semantic-instruction binding must carry an instruction template",
			"SemanticEffectBinding", "the effect cannot be lowered to an instruction",
			"construct the lowering with NewSemanticLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassSemanticInstruction
	entry.semantic = lowering
	return EffectBinding{entry: entry}, nil
}

// UnsupportedEffectBinding classifies an effect as unsupported on this contract.
func UnsupportedEffectBinding[In, Out any](descriptor ir.EffectDescriptor[In, Out], reason string) (EffectBinding, error) {
	entry, err := effectEntry(descriptor, "UnsupportedEffectBinding")
	if err != nil {
		return EffectBinding{}, err
	}
	if reason == "" {
		return EffectBinding{}, runtimeError(
			fmt.Sprintf("unsupported binding for effect %q has no reason", entry.id),
			"an unsupported classification must explain why the construct has no runtime lowering",
			"UnsupportedEffectBinding", "callers could not act on the failure",
			"supply a reason describing why the effect is unsupported", nil,
		)
	}
	entry.class = effects.RuntimeClassUnsupported
	entry.unsupportedReason = reason
	return EffectBinding{entry: entry}, nil
}

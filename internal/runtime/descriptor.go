package runtime

import (
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// BindingDescriptor is a detached, read-only description of a compiled binding,
// including unsupported bindings which executable lookup intentionally refuses.
type BindingDescriptor struct {
	ID                 string
	Class              effects.RuntimeClass
	InputSchema        ir.SchemaID
	OutputSchema       ir.SchemaID
	Semantics          ir.DescriptorSemantics
	Effects            []string
	NativeCall         string
	Arguments          []string
	ResultSemantics    string
	ContextInheritance string
	Mediator           string
	Instruction        string
	Template           string
	UnsupportedReason  string
}

type CapabilityDescriptor struct {
	ID      ir.CapabilityID
	Version ir.CapabilityContractVersion
	Minimum ir.CapabilityContractVersion
	Maximum ir.CapabilityContractVersion
	Binding BindingDescriptor
}

func describeBindings(entries map[string]boundEntry) []BindingDescriptor {
	out := make([]BindingDescriptor, 0, len(entries))
	for _, e := range entries {
		ids := make([]string, 0)
		for _, id := range e.effects.IDs() {
			ids = append(ids, id.String())
		}
		semantics := e.semantics
		semantics.Preconditions = append([]string(nil), semantics.Preconditions...)
		semantics.Postconditions = append([]string(nil), semantics.Postconditions...)
		out = append(out, BindingDescriptor{
			ID: e.id, Class: e.class, InputSchema: e.inSchema, OutputSchema: e.outSchema,
			Semantics: semantics, Effects: ids, NativeCall: e.native.CallName(), Arguments: e.native.Arguments(),
			ResultSemantics: e.native.ResultSemantics(), ContextInheritance: e.native.ContextInheritance(),
			Mediator: e.mediated.Mediator(), Instruction: e.mediated.Instruction(), Template: e.semantic.InstructionTemplate(),
			UnsupportedReason: e.unsupportedReason,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c RuntimeContract) OperationDescriptors() []BindingDescriptor {
	return describeBindings(c.core.operations)
}
func (c RuntimeContract) EffectDescriptors() []BindingDescriptor {
	return describeBindings(c.core.effects)
}
func (c RuntimeContract) CapabilityDescriptors() []CapabilityDescriptor {
	out := make([]CapabilityDescriptor, 0, len(c.capabilities))
	for _, contribution := range c.capabilities {
		out = append(out, CapabilityDescriptor{ID: contribution.capabilityID, Version: contribution.version,
			Minimum: contribution.versions.Min(), Maximum: contribution.versions.Max(),
			Binding: describeBindings(map[string]boundEntry{contribution.entry.id: contribution.entry})[0]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

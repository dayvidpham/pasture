package runtime

import (
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// BindingDescriptor is a detached, read-only description of a compiled binding,
// including unsupported bindings which executable lookup intentionally refuses.
// Explicit JSON member names retain the initial persisted spelling independent
// of Go field renames; changes to wire names require a document codec revision.
type BindingDescriptor struct {
	ID                 string                 `json:"ID"`
	Class              effects.RuntimeClass   `json:"Class"`
	InputSchema        ir.SchemaID            `json:"InputSchema"`
	OutputSchema       ir.SchemaID            `json:"OutputSchema"`
	Semantics          ir.DescriptorSemantics `json:"Semantics"`
	Effects            []string               `json:"Effects"`
	NativeCall         string                 `json:"NativeCall"`
	Arguments          []string               `json:"Arguments"`
	ResultSemantics    string                 `json:"ResultSemantics"`
	ContextInheritance string                 `json:"ContextInheritance"`
	Mediator           string                 `json:"Mediator"`
	Instruction        string                 `json:"Instruction"`
	Template           string                 `json:"Template"`
	UnsupportedReason  string                 `json:"UnsupportedReason"`
}

type CapabilityDescriptor struct {
	ID      ir.CapabilityID              `json:"ID"`
	Version ir.CapabilityContractVersion `json:"Version"`
	Minimum ir.CapabilityContractVersion `json:"Minimum"`
	Maximum ir.CapabilityContractVersion `json:"Maximum"`
	Binding BindingDescriptor            `json:"Binding"`
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

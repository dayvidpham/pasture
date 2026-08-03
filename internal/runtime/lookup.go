package runtime

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// LookupOperationBinding resolves the runtime binding for a semantic operation
// descriptor. The operand is a constructor-validated typed descriptor, never a
// raw ID, string, or untyped literal. Missing, invalid, type-incompatible, or
// unsupported descriptors return an actionable error; none collapses into a
// nil or zero binding.
func LookupOperationBinding[In, Out any](
	contract RuntimeContract,
	descriptor ir.OperationDescriptor[In, Out],
) (RuntimeBinding[In, Out], error) {
	if !contract.constructed {
		return nil, runtimeError(
			"operation lookup used a zero runtime contract",
			"a lookup needs a contract built by NewRuntimeContract",
			"LookupOperationBinding", "no binding could be resolved",
			"construct the contract with NewRuntimeContract", nil,
		)
	}
	if !descriptor.IsValid() {
		return nil, runtimeError(
			"operation descriptor operand is zero or invalid",
			"lookup accepts only a constructor-validated typed descriptor, never a raw ID or untyped literal",
			"LookupOperationBinding", "no binding could be resolved",
			"construct the descriptor with ir.NewOperationDescriptor", nil,
		)
	}
	entry, found := contract.core.operations[descriptor.ID().String()]
	if !found {
		return nil, unboundError("operation", descriptor.ID().String(), contract.id.String(), "LookupOperationBinding")
	}
	if !entry.matchesTypes(typeOf[In](), typeOf[Out](), descriptor.InputCodec().Schema(), descriptor.OutputCodec().Schema()) {
		return nil, incompatibleError("operation", descriptor.ID().String(), "LookupOperationBinding")
	}
	if err := requireExecutable("operation", entry, "LookupOperationBinding"); err != nil {
		return nil, err
	}
	return runtimeBinding[In, Out]{entry: entry}, nil
}

// LookupEffectBinding resolves the runtime binding for a typed effect
// descriptor with the same guarantees as LookupOperationBinding.
func LookupEffectBinding[In, Out any](
	contract RuntimeContract,
	descriptor ir.EffectDescriptor[In, Out],
) (RuntimeBinding[In, Out], error) {
	if !contract.constructed {
		return nil, runtimeError(
			"effect lookup used a zero runtime contract",
			"a lookup needs a contract built by NewRuntimeContract",
			"LookupEffectBinding", "no binding could be resolved",
			"construct the contract with NewRuntimeContract", nil,
		)
	}
	if !descriptor.IsValid() {
		return nil, runtimeError(
			"effect descriptor operand is zero or invalid",
			"lookup accepts only a constructor-validated typed descriptor, never a raw ID or untyped literal",
			"LookupEffectBinding", "no binding could be resolved",
			"construct the descriptor with ir.NewEffectDescriptor", nil,
		)
	}
	entry, found := contract.core.effects[descriptor.ID().String()]
	if !found {
		return nil, unboundError("effect", descriptor.ID().String(), contract.id.String(), "LookupEffectBinding")
	}
	if !entry.matchesTypes(typeOf[In](), typeOf[Out](), descriptor.InputCodec().Schema(), descriptor.OutputCodec().Schema()) {
		return nil, incompatibleError("effect", descriptor.ID().String(), "LookupEffectBinding")
	}
	if err := requireExecutable("effect", entry, "LookupEffectBinding"); err != nil {
		return nil, err
	}
	return runtimeBinding[In, Out]{entry: entry}, nil
}

// LookupCapabilityBinding resolves the external capability binding contributed
// for capability. The capability's exact contract version must fall within the
// contribution's bound range, and its codecs must match the contributed binding.
func LookupCapabilityBinding[In, Out any](
	contract RuntimeContract,
	capability ir.Capability[In, Out],
) (CapabilityRuntimeBinding[In, Out], error) {
	if !contract.constructed {
		return nil, runtimeError(
			"capability lookup used a zero runtime contract",
			"a lookup needs a contract built by NewRuntimeContract",
			"LookupCapabilityBinding", "no binding could be resolved",
			"construct the contract with NewRuntimeContract", nil,
		)
	}
	if !capability.IsValid() {
		return nil, runtimeError(
			"capability operand is zero or invalid",
			"lookup accepts only a descriptor built by ir.DefineCapability, never a raw CapabilityID",
			"LookupCapabilityBinding", "no binding could be resolved",
			"construct the capability with ir.DefineCapability or ir.MustDefineCapability", nil,
		)
	}
	contribution, found := contract.capabilities[capability.ID()]
	if !found {
		return nil, unboundError("capability", string(capability.ID()), contract.id.String(), "LookupCapabilityBinding")
	}
	if !contribution.versions.Includes(capability.Version()) {
		return nil, runtimeError(
			fmt.Sprintf("capability %q version %q is outside the contract's bound range [%s, %s]", capability.ID(), capability.Version(), contribution.versions.Min(), contribution.versions.Max()),
			"a version-bounded contract lowers only the capability versions its contribution covers",
			"LookupCapabilityBinding", "the runtime binding would honor an out-of-range version",
			"request a capability version the contract binds, or widen the contribution range", nil,
		)
	}
	if !contribution.entry.matchesTypes(typeOf[In](), typeOf[Out](), capability.InputCodec().Schema(), capability.OutputCodec().Schema()) {
		return nil, incompatibleError("capability", string(capability.ID()), "LookupCapabilityBinding")
	}
	return capabilityRuntimeBinding[In, Out]{
		capabilityID: capability.ID(),
		version:      capability.Version(),
		entry:        contribution.entry,
	}, nil
}

func requireExecutable(domain string, entry boundEntry, where string) error {
	if entry.class == effects.RuntimeClassUnsupported {
		reason := entry.unsupportedReason
		if reason == "" {
			reason = "the construct has no modeled runtime semantics on this contract"
		}
		return runtimeError(
			fmt.Sprintf("%s %q is classified unsupported on this contract", domain, entry.id),
			reason,
			where, "the construct cannot be lowered and must not fall through to a similarly named native call",
			"make it a dedicated supported operation, or target a contract that classifies it as native, mediated, or a semantic instruction", nil,
		)
	}
	if !entry.class.IsValid() {
		return runtimeError(
			fmt.Sprintf("%s %q has no valid runtime classification", domain, entry.id),
			"a resolved binding must be native, parent-mediated, or a semantic instruction",
			where, "the construct cannot be lowered",
			"rebuild the contract with a classified binding", nil,
		)
	}
	return nil
}

func unboundError(domain, id, contract, where string) error {
	return runtimeError(
		fmt.Sprintf("%s %q is unbound on contract %q", domain, id, contract),
		"a contract lowers only the descriptors it explicitly classifies",
		where, "no runtime behavior exists for the descriptor",
		"classify the descriptor in the contract before looking it up", nil,
	)
}

func incompatibleError(domain, id, where string) error {
	return runtimeError(
		fmt.Sprintf("%s %q was looked up with incompatible input/output types or schemas", domain, id),
		"a typed lookup must present the exact Go types and codec schemas the descriptor declared; a differing type cannot silently reuse another binding",
		where, "the binding cannot be returned for a mismatched typed lookup",
		"look the descriptor up with the same In/Out type parameters and codecs it was registered with", nil,
	)
}

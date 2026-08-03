package runtime

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
)

// CapabilityRuntimeBinding is the typed binding an external package contributes
// for one opaque capability descriptor. Its marker method is unexported, so the
// only values that satisfy it are produced by this package's constructors
// (NativeCapabilityBinding and its siblings): a foreign implementation carrying
// a dynamic native name or untyped argument map can never cross the
// BindCapability boundary.
type CapabilityRuntimeBinding[In, Out any] interface {
	capabilityRuntimeBinding()
	Class() effects.RuntimeClass
	CapabilityID() ir.CapabilityID
	Version() ir.CapabilityContractVersion
	InputSchema() ir.SchemaID
	OutputSchema() ir.SchemaID
	Semantics() ir.CapabilitySemantics
	Effects() ir.EffectSet
	Native() (NativeCall, bool)
	Mediated() (MediatedLowering, bool)
	Semantic() (SemanticLowering, bool)
}

type capabilityRuntimeBinding[In, Out any] struct {
	capabilityID ir.CapabilityID
	version      ir.CapabilityContractVersion
	entry        boundEntry
}

func (capabilityRuntimeBinding[In, Out]) capabilityRuntimeBinding()     {}
func (b capabilityRuntimeBinding[In, Out]) Class() effects.RuntimeClass { return b.entry.class }
func (b capabilityRuntimeBinding[In, Out]) CapabilityID() ir.CapabilityID {
	return b.capabilityID
}
func (b capabilityRuntimeBinding[In, Out]) Version() ir.CapabilityContractVersion {
	return b.version
}
func (b capabilityRuntimeBinding[In, Out]) InputSchema() ir.SchemaID  { return b.entry.inSchema }
func (b capabilityRuntimeBinding[In, Out]) OutputSchema() ir.SchemaID { return b.entry.outSchema }
func (b capabilityRuntimeBinding[In, Out]) Semantics() ir.CapabilitySemantics {
	return ir.DescriptorSemantics{
		Summary:        b.entry.semantics.Summary,
		Preconditions:  append([]string(nil), b.entry.semantics.Preconditions...),
		Postconditions: append([]string(nil), b.entry.semantics.Postconditions...),
		Result:         b.entry.semantics.Result,
	}
}
func (b capabilityRuntimeBinding[In, Out]) Effects() ir.EffectSet { return b.entry.effects }
func (b capabilityRuntimeBinding[In, Out]) Native() (NativeCall, bool) {
	return b.entry.native, b.entry.class == effects.RuntimeClassNative
}
func (b capabilityRuntimeBinding[In, Out]) Mediated() (MediatedLowering, bool) {
	return b.entry.mediated, b.entry.class == effects.RuntimeClassParentMediated
}
func (b capabilityRuntimeBinding[In, Out]) Semantic() (SemanticLowering, bool) {
	return b.entry.semantic, b.entry.class == effects.RuntimeClassSemanticInstruction
}

func capabilityEntry[In, Out any](capability ir.Capability[In, Out], where string) (boundEntry, error) {
	if !capability.IsValid() {
		return boundEntry{}, runtimeError(
			"capability descriptor is zero or invalid",
			"a capability binding must lower a descriptor built by ir.DefineCapability",
			where, "the capability cannot be bound",
			"construct the capability with ir.DefineCapability or ir.MustDefineCapability", nil,
		)
	}
	return boundEntry{
		id:        string(capability.ID()),
		inType:    typeOf[In](),
		outType:   typeOf[Out](),
		inSchema:  capability.InputCodec().Schema(),
		outSchema: capability.OutputCodec().Schema(),
		semantics: capability.Semantics(),
		effects:   capability.Effects(),
	}, nil
}

// NativeCapabilityBinding builds a native capability binding.
func NativeCapabilityBinding[In, Out any](capability ir.Capability[In, Out], call NativeCall) (CapabilityRuntimeBinding[In, Out], error) {
	entry, err := capabilityEntry(capability, "NativeCapabilityBinding")
	if err != nil {
		return nil, err
	}
	if !call.IsValid() {
		return nil, runtimeError(
			fmt.Sprintf("native binding for capability %q has an invalid native call", entry.id),
			"a native binding owns a complete validated native call",
			"NativeCapabilityBinding", "the capability cannot be lowered natively",
			"construct the call with NewNativeCall", nil,
		)
	}
	entry.class = effects.RuntimeClassNative
	entry.native = call
	return capabilityRuntimeBinding[In, Out]{capabilityID: capability.ID(), version: capability.Version(), entry: entry}, nil
}

// MediatedCapabilityBinding builds a parent-mediated capability binding.
func MediatedCapabilityBinding[In, Out any](capability ir.Capability[In, Out], lowering MediatedLowering) (CapabilityRuntimeBinding[In, Out], error) {
	entry, err := capabilityEntry(capability, "MediatedCapabilityBinding")
	if err != nil {
		return nil, err
	}
	if !lowering.IsValid() {
		return nil, runtimeError(
			fmt.Sprintf("mediated binding for capability %q has an invalid lowering", entry.id),
			"a parent-mediated binding must name its mediator and instruction",
			"MediatedCapabilityBinding", "the capability cannot be mediated",
			"construct the lowering with NewMediatedLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassParentMediated
	entry.mediated = lowering
	return capabilityRuntimeBinding[In, Out]{capabilityID: capability.ID(), version: capability.Version(), entry: entry}, nil
}

// SemanticCapabilityBinding builds a semantic-instruction capability binding.
func SemanticCapabilityBinding[In, Out any](capability ir.Capability[In, Out], lowering SemanticLowering) (CapabilityRuntimeBinding[In, Out], error) {
	entry, err := capabilityEntry(capability, "SemanticCapabilityBinding")
	if err != nil {
		return nil, err
	}
	if !lowering.IsValid() {
		return nil, runtimeError(
			fmt.Sprintf("semantic binding for capability %q has an invalid lowering", entry.id),
			"a semantic-instruction binding must carry an instruction template",
			"SemanticCapabilityBinding", "the capability cannot be lowered to an instruction",
			"construct the lowering with NewSemanticLowering", nil,
		)
	}
	entry.class = effects.RuntimeClassSemanticInstruction
	entry.semantic = lowering
	return capabilityRuntimeBinding[In, Out]{capabilityID: capability.ID(), version: capability.Version(), entry: entry}, nil
}

// RuntimeBindingContribution is the opaque, heterogeneous contribution
// BindCapability produces and NewRuntimeContract consumes. Its marker method is
// unexported so no dynamic native name or untyped argument map can masquerade
// as a contribution; the only source is BindCapability.
type RuntimeBindingContribution interface{ runtimeBindingContribution() }

// capabilityContribution is the concrete contribution: a version-bounded
// capability binding kept type-erased until a typed lookup re-attaches In/Out.
type capabilityContribution struct {
	capabilityID ir.CapabilityID
	version      ir.CapabilityContractVersion
	versions     CapabilityVersionRange
	entry        boundEntry
}

func (capabilityContribution) runtimeBindingContribution() {}

// BindCapability contributes a version-bounded runtime binding for one opaque
// capability descriptor without editing this package. It validates the
// descriptor's codecs, semantics, and effect set (already enforced by
// ir.DefineCapability), and the version intersection: the requested
// capability's exact contract version must fall within versions. The resulting
// contribution is opaque; NewRuntimeContract validates it further against the
// full requested-capability set.
func BindCapability[In, Out any](
	capability ir.Capability[In, Out],
	versions CapabilityVersionRange,
	binding CapabilityRuntimeBinding[In, Out],
) (RuntimeBindingContribution, error) {
	if !capability.IsValid() {
		return nil, runtimeError(
			"capability descriptor is zero or invalid",
			"a contribution must bind a descriptor built by ir.DefineCapability",
			"BindCapability", "the contribution cannot be constructed",
			"construct the capability with ir.DefineCapability or ir.MustDefineCapability", nil,
		)
	}
	if !versions.IsValid() {
		return nil, runtimeError(
			fmt.Sprintf("capability %q contribution has an invalid version range", capability.ID()),
			"a version-bounded contribution requires a constructor-validated range",
			"BindCapability", "the contribution cannot be constructed",
			"construct the range with NewCapabilityVersionRange", nil,
		)
	}
	if !versions.Includes(capability.Version()) {
		return nil, runtimeError(
			fmt.Sprintf("capability %q version %q is outside contribution range [%s, %s]", capability.ID(), capability.Version(), versions.Min(), versions.Max()),
			"the requested capability's exact contract version must intersect the bound range",
			"BindCapability", "the contribution would honor a version the capability does not declare",
			"widen the version range or bind the capability version it covers", nil,
		)
	}
	concrete, ok := binding.(capabilityRuntimeBinding[In, Out])
	if !ok {
		return nil, runtimeError(
			fmt.Sprintf("capability %q binding was not produced by this package", capability.ID()),
			"only NativeCapabilityBinding, MediatedCapabilityBinding, or SemanticCapabilityBinding produce a valid binding; a foreign implementation could smuggle a dynamic native name",
			"BindCapability", "the contribution cannot be trusted",
			"build the binding with one of this package's capability binding constructors", nil,
		)
	}
	if concrete.capabilityID != capability.ID() || concrete.version != capability.Version() {
		return nil, runtimeError(
			fmt.Sprintf("capability %q binding names a different capability or version", capability.ID()),
			"a contribution's binding must lower exactly the capability it is bound for",
			"BindCapability", "the contribution would bind the wrong capability",
			"build the binding for the same capability descriptor passed to BindCapability", nil,
		)
	}
	if !concrete.entry.class.IsValid() || concrete.entry.class == effects.RuntimeClassUnsupported {
		return nil, runtimeError(
			fmt.Sprintf("capability %q binding has no executable class", capability.ID()),
			"an external contribution must lower to native, parent-mediated, or semantic-instruction",
			"BindCapability", "the capability could not be lowered",
			"build a native, mediated, or semantic capability binding", nil,
		)
	}
	return capabilityContribution{
		capabilityID: capability.ID(),
		version:      capability.Version(),
		versions:     versions,
		entry:        concrete.entry,
	}, nil
}

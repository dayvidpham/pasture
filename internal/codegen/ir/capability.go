package ir

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sync"
)

// CapabilityID is the portable identity of one typed extension capability:
// the safe InvokeTool escape for semantic operations that do not justify a
// dedicated core IR variant (see OperationInvokeSkill and its siblings for
// the closed, reviewed core vocabulary). It is a named string type, not an
// opaque struct like SemanticOperationID/SkillID/EffectID: Go's own
// untyped-literal assignability rule therefore lets a raw string literal
// compile as a CapabilityID value. That is intentional and accepted by the
// contract this type implements — the canonical-declaration-site rule is
// enforced by a separate static-analysis rule
// (internal/codegen/capabilitylint), not by the type system, precisely
// because the type system cannot express it for a named string type.
type CapabilityID string

// CapabilityContractVersion is the exact MAJOR.MINOR.PATCH version of one
// capability's contract. Two registrations sharing a CapabilityID and
// CapabilityContractVersion must describe an identical contract (see
// registerCapabilityContract); a capability evolves by registering a new
// version, never by silently changing an already-registered version.
type CapabilityContractVersion string

// CapabilitySemantics is the portable behavioral contract a capability
// descriptor carries: summary, preconditions, postconditions, and result
// contract, for a runtime binding to preserve across every harness. It is a
// type alias for DescriptorSemantics — the identical shape #38 already
// defined for OperationDescriptor/EffectDescriptor — so a Capability and a
// core operation/effect descriptor share one semantics contract instead of
// two structurally identical types that could silently drift apart.
type CapabilitySemantics = DescriptorSemantics

// capabilityContract is the non-generic registered shape of one
// (CapabilityID, CapabilityContractVersion) pair, used only for registry
// conflict detection. It intentionally does not retain the Codec values
// themselves (codecs are typed and cannot be compared for equality); it
// retains everything the registry can compare: the concrete Go types the
// codecs were built for, the codecs' own schema identities, the semantics,
// and the effect set.
type capabilityContract struct {
	version   CapabilityContractVersion
	inType    reflect.Type
	outType   reflect.Type
	inSchema  SchemaID
	outSchema SchemaID
	semantics DescriptorSemantics
	effects   EffectSet
}

func (c capabilityContract) equal(other capabilityContract) bool {
	return c.version == other.version &&
		c.inType == other.inType &&
		c.outType == other.outType &&
		c.inSchema == other.inSchema &&
		c.outSchema == other.outSchema &&
		c.semantics.Summary == other.semantics.Summary &&
		c.semantics.Result == other.semantics.Result &&
		slices.Equal(c.semantics.Preconditions, other.semantics.Preconditions) &&
		slices.Equal(c.semantics.Postconditions, other.semantics.Postconditions) &&
		c.effects.Equal(other.effects)
}

var (
	capabilityRegistryMu sync.Mutex
	// capabilityRegistry is process-global by design: capability identities
	// are declared once at package scope (typically via a top-level `var`
	// initialized through MustDefineCapability) and must stay unique for the
	// lifetime of the process, exactly like coreOperationIDs is a fixed,
	// process-wide table for the closed core operation set. Access is
	// guarded by capabilityRegistryMu because DefineCapability may also be
	// called dynamically (the error-returning form exists precisely for
	// that case), including concurrently from multiple goroutines.
	capabilityRegistry = map[CapabilityID]map[CapabilityContractVersion]capabilityContract{}
)

// registerCapabilityContract records contract under id, or returns an
// actionable conflict diagnostic. A second registration of the exact same
// (id, version) pair is always rejected — even a byte-identical contract —
// because a capability's canonical declaration site is defined exactly once;
// a second call, identical or not, means either an accidental duplicate
// declaration or two packages racing to define the same identity.
func registerCapabilityContract(id CapabilityID, contract capabilityContract) error {
	capabilityRegistryMu.Lock()
	defer capabilityRegistryMu.Unlock()

	versions := capabilityRegistry[id]
	if existing, seen := versions[contract.version]; seen {
		if existing.equal(contract) {
			return capabilityError(
				fmt.Sprintf("capability %q version %q is already registered", id, contract.version),
				"each capability identity and contract version is defined exactly once, at its canonical DefineCapability/MustDefineCapability declaration site",
				"DefineCapability registry validation",
				fmt.Sprintf("remove the duplicate definition of capability %q version %q, or reuse the already-registered Capability value instead of redefining it", id, contract.version),
				nil,
			)
		}
		return capabilityError(
			fmt.Sprintf("capability %q version %q is already registered with a different contract", id, contract.version),
			"two definitions sharing one capability identity and contract version must declare identical types, codecs, semantics, and effects, or a runtime binding could not know which contract to honor",
			"DefineCapability registry validation",
			fmt.Sprintf("give the changed contract a new CapabilityContractVersion, or align capability %q version %q exactly with its existing registration", id, contract.version),
			nil,
		)
	}
	if versions == nil {
		versions = make(map[CapabilityContractVersion]capabilityContract, 1)
		capabilityRegistry[id] = versions
	}
	versions[contract.version] = contract
	return nil
}

// Capability is the opaque typed descriptor for one extension capability
// contract. It is deliberately not a constructible exported struct: the only
// way to produce a non-zero value is DefineCapability (or its panicking
// static-declaration form MustDefineCapability), so a Capability[In, Out]
// value in hand has already passed identity, codec, semantics, and effect
// validation, and is registered exactly once for its (CapabilityID,
// CapabilityContractVersion) pair. InvokeTool accepts only this descriptor —
// never a raw CapabilityID, string, native harness tool name, or untyped
// argument map.
type Capability[In, Out any] struct {
	id        CapabilityID
	version   CapabilityContractVersion
	semantics CapabilitySemantics
	effects   EffectSet
	input     Codec[In]
	output    Codec[Out]
	inType    reflect.Type
	outType   reflect.Type
}

func (c Capability[In, Out]) ID() CapabilityID                   { return c.id }
func (c Capability[In, Out]) Version() CapabilityContractVersion { return c.version }
func (c Capability[In, Out]) Semantics() CapabilitySemantics     { return cloneSemantics(c.semantics) }
func (c Capability[In, Out]) Effects() EffectSet                 { return c.effects }
func (c Capability[In, Out]) InputCodec() Codec[In]              { return c.input }
func (c Capability[In, Out]) OutputCodec() Codec[Out]            { return c.output }

func (c Capability[In, Out]) IsValid() bool {
	return validateCapabilityID(c.id) == nil &&
		validateCapabilityVersion(c.version) == nil &&
		c.input != nil && c.output != nil &&
		c.inType == typeOf[In]() && c.outType == typeOf[Out]() &&
		c.effects.IsValid()
}

// DefineCapability validates id, version, semantics, effects, and both
// codecs, registers the resulting contract exactly once (see
// registerCapabilityContract), and returns the opaque descriptor. Dynamic or
// user-supplied inputs must use this error-returning form; a static package
// declaration should use MustDefineCapability instead.
func DefineCapability[In, Out any](
	id CapabilityID,
	version CapabilityContractVersion,
	semantics CapabilitySemantics,
	effects EffectSet,
	input Codec[In],
	output Codec[Out],
) (Capability[In, Out], error) {
	var zero Capability[In, Out]

	if err := validateCapabilityID(id); err != nil {
		return zero, err
	}
	if err := validateCapabilityVersion(version); err != nil {
		return zero, err
	}
	if input == nil || output == nil || input.Schema() == "" || output.Schema() == "" {
		return zero, capabilityError(
			fmt.Sprintf("capability %q has a missing input or output codec", id),
			"typed runtime binding requires stable schemas in both directions",
			"DefineCapability",
			"supply non-zero typed codecs constructed with NewJSONCodec or an equivalent Codec implementation",
			nil,
		)
	}
	if err := semantics.validate(string(id)); err != nil {
		return zero, err
	}
	if !effects.IsValid() {
		return zero, capabilityError(
			fmt.Sprintf("capability %q has a noncanonical effect set", id),
			"effects are part of binding compatibility across every harness a runtime binding later targets",
			"DefineCapability",
			"construct effects with NewEffectSet",
			nil,
		)
	}

	contract := capabilityContract{
		version:   version,
		inType:    typeOf[In](),
		outType:   typeOf[Out](),
		inSchema:  input.Schema(),
		outSchema: output.Schema(),
		semantics: cloneSemantics(semantics),
		effects:   effects,
	}
	if err := registerCapabilityContract(id, contract); err != nil {
		return zero, err
	}

	return Capability[In, Out]{
		id: id, version: version, semantics: cloneSemantics(semantics), effects: effects,
		input: input, output: output, inType: typeOf[In](), outType: typeOf[Out](),
	}, nil
}

// MustDefineCapability is DefineCapability restricted to static package
// declarations (e.g. a top-level `var RenderDiagram = MustDefineCapability(...)`):
// it panics with DefineCapability's own actionable validation error instead
// of returning one. Dynamic or user-supplied inputs must use DefineCapability.
func MustDefineCapability[In, Out any](
	id CapabilityID,
	version CapabilityContractVersion,
	semantics CapabilitySemantics,
	effects EffectSet,
	input Codec[In],
	output Codec[Out],
) Capability[In, Out] {
	capability, err := DefineCapability(id, version, semantics, effects, input, output)
	if err != nil {
		panic(err)
	}
	return capability
}

var capabilityVersionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

func validateCapabilityID(id CapabilityID) error {
	_, err := validateOpaqueID("capability identity", string(id), true)
	return err
}

// IsValid reports whether id is a non-empty, namespaced, control-character-
// free UTF-8 identity without surrounding whitespace — the same shape
// validateOpaqueID enforces for every other portable identity in this
// package. Internal whitespace is permitted (surrounding whitespace is not);
// this intentionally matches every sibling identity domain (SkillID,
// EffectID, SemanticOperationID, ...), which validateOpaqueID also backs.
func (id CapabilityID) IsValid() bool { return validateCapabilityID(id) == nil }

func validateCapabilityVersion(version CapabilityContractVersion) error {
	value := string(version)
	if !capabilityVersionPattern.MatchString(value) {
		return capabilityError(
			fmt.Sprintf("capability contract version %q is not a MAJOR.MINOR.PATCH semantic version", value),
			"capability contracts are compared, deduplicated, and range-bound by exact MAJOR.MINOR.PATCH identity",
			"capability contract version validation",
			`use a version such as "1.0.0"`,
			nil,
		)
	}
	return nil
}

// IsValid reports whether version is a well-formed MAJOR.MINOR.PATCH triple.
func (v CapabilityContractVersion) IsValid() bool { return validateCapabilityVersion(v) == nil }

// OperationInvokeTool is the closed protocol-meaning identity shared by
// every InvokeTool operation, regardless of which capability it invokes —
// exactly as every core operation kind shares one fixed SemanticOperationID
// (see coreOperationIDs). It is deliberately not added to
// canonicalOperationKinds/AllOperationKinds: that accessor is the closed,
// reviewed *core* orchestration vocabulary, and InvokeTool is explicitly the
// escape hatch for semantics that do not (yet) justify a dedicated core IR
// variant. A frequently used capability graduating to a dedicated core
// operation is a separate, later change to operations.go, not something this
// escape hatch does on its own.
const OperationInvokeTool OperationKind = "invoke_tool"

var invokeToolOperationID = mustSemanticOperationID("pasture.orchestration.invoke-tool/v1")

// InvokeToolOperation is the SemanticOperation InvokeTool constructs. It is
// exported (unlike Capability's internal registration bookkeeping) so a
// later runtime binding can recover the invoked capability's identity,
// contract version, and codec-validated input without a JSON round trip
// through CanonicalSemanticOperation — mirroring InvokeSkill's own Skill()/
// Operands() accessors. Its fields are unexported: the only way to produce a
// non-zero value is InvokeTool itself, so an InvokeToolOperation in hand
// always carries a capability identity that passed DefineCapability
// validation, never a raw string, native harness tool name, or arbitrary map.
type InvokeToolOperation struct {
	capabilityID CapabilityID
	version      CapabilityContractVersion
	inputSchema  SchemaID
	outputSchema SchemaID
	input        json.RawMessage
	constructErr error
}

func (InvokeToolOperation) semanticOperation()                 {}
func (InvokeToolOperation) operationKind() OperationKind       { return OperationInvokeTool }
func (o InvokeToolOperation) operationID() SemanticOperationID { return invokeToolOperationID }

// Capability returns the invoked capability's identity.
func (o InvokeToolOperation) Capability() CapabilityID { return o.capabilityID }

// Version returns the invoked capability's contract version.
func (o InvokeToolOperation) Version() CapabilityContractVersion { return o.version }

// EncodedInput returns a defensive copy of the codec-encoded input bytes.
func (o InvokeToolOperation) EncodedInput() []byte { return append([]byte(nil), o.input...) }

// InputSchema returns the capability input codec's schema identity.
func (o InvokeToolOperation) InputSchema() SchemaID { return o.inputSchema }

// OutputSchema returns the capability output codec's schema identity.
func (o InvokeToolOperation) OutputSchema() SchemaID { return o.outputSchema }

func (o InvokeToolOperation) validateOperation() error {
	if o.constructErr != nil {
		return o.constructErr
	}
	if err := validateCapabilityID(o.capabilityID); err != nil {
		return err
	}
	if err := validateCapabilityVersion(o.version); err != nil {
		return err
	}
	if o.inputSchema == "" || o.outputSchema == "" {
		return capabilityError(
			"invoke-tool operation is missing its capability input or output schema",
			"an invocation must retain the exact codec schema identities its capability descriptor declared",
			"InvokeTool operation validation",
			"construct the operation through InvokeTool with a Capability built by DefineCapability",
			nil,
		)
	}
	if len(o.input) == 0 || !json.Valid(o.input) {
		return capabilityError(
			"invoke-tool operation input is empty or not valid JSON",
			"the capability's input codec must produce one complete encoded value before the operation can enter a document",
			"InvokeTool operation validation",
			"construct the operation through InvokeTool with a value accepted by the capability's input codec",
			nil,
		)
	}
	return nil
}

type invokeToolWire struct {
	Capability string          `json:"capability"`
	Version    string          `json:"version"`
	Input      json.RawMessage `json:"input"`
}

func (o InvokeToolOperation) canonicalOperation() ([]byte, error) {
	if err := o.validateOperation(); err != nil {
		return nil, err
	}
	return json.Marshal(operationWire{
		Kind: o.operationKind(),
		ID:   o.operationID().String(),
		Payload: invokeToolWire{
			Capability: string(o.capabilityID),
			Version:    string(o.version),
			Input:      o.input,
		},
		// An empty (non-nil) slice, not nil: every core operation's wire
		// form goes through marshalOperation -> resultWires(...), which
		// always returns a non-nil []resultSlotWire and therefore emits
		// "results":[] even with zero declared result slots (see e.g.
		// SendAssignmentMessage/StopAssignment, which also call
		// marshalOperation with nil results). invoke_tool has no result
		// slots of its own, but its canonical shape must stay uniform with
		// every other SemanticOperation variant so a consumer typing
		// "results" as a JSON array never sees null for exactly this one
		// variant.
		Results: []resultSlotWire{},
	})
}

// InvokeTool constructs the SemanticOperation that invokes capability with
// input. It accepts only an opaque Capability descriptor — never a
// CapabilityID, raw string, native harness tool name, or untyped argument
// map — and codec-encodes input immediately so the resulting operation's
// serialized IR always carries validated identity, capability contract
// version, and encoded input, never a raw native name. Because this
// signature (unlike every other SemanticOperation constructor in this
// package) does not return an error, any construction failure — an invalid
// capability or an input the capability's codec rejects — is retained and
// surfaced the first time the operation is validated: when it enters a
// Document via Operation(...), or through
// SemanticOperationKind/SemanticOperationIdentity/CanonicalSemanticOperation.
func InvokeTool[In, Out any](capability Capability[In, Out], input In) SemanticOperation {
	if !capability.IsValid() {
		return InvokeToolOperation{constructErr: capabilityError(
			"capability descriptor is zero or invalid",
			"InvokeTool can only invoke a descriptor constructed by DefineCapability or MustDefineCapability",
			"InvokeTool",
			"construct the capability with DefineCapability before invoking it",
			nil,
		)}
	}
	encoded, err := capability.input.Encode(input)
	if err != nil {
		return InvokeToolOperation{
			capabilityID: capability.id,
			version:      capability.version,
			constructErr: capabilityError(
				fmt.Sprintf("capability %q input could not be encoded", capability.id),
				"InvokeTool must retain a validated, codec-encoded input before the operation can enter a document",
				"InvokeTool",
				"supply a value accepted by the capability's input codec",
				err,
			),
		}
	}
	return InvokeToolOperation{
		capabilityID: capability.id,
		version:      capability.version,
		inputSchema:  capability.input.Schema(),
		outputSchema: capability.output.Schema(),
		input:        append(json.RawMessage(nil), encoded...),
	}
}

func capabilityError(what, why, where, fix string, cause error) error {
	return diagnostic(
		what, why, where, "capability validation",
		"the capability or invocation is rejected before it can enter a document or bind at runtime",
		fix, cause,
	)
}

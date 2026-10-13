package definitions

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

const RuntimeDocumentVersion uint32 = 1

type runtimeDocument struct {
	Kind          model.DefinitionKind  `json:"kind"`
	ID            model.DefinitionID    `json:"id"`
	Version       uint32                `json:"version"`
	Codec         model.DefinitionCodec `json:"codec"`
	Specification RuntimeSpecification  `json:"specification"`
}

// RuntimeSpecification joins the complete compiled runtime classification and
// native lifecycle catalogs. It contains no host instance, payload or authority.
type RuntimeSpecification struct {
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
	Registration        registration.Manifest                 `json:"registration"`
	Lifecycle           []LifecycleDescriptor                 `json:"lifecycle"`
}

type IdentityDescriptor struct {
	Kind     pastureruntime.NativeIdentityKind `json:"kind"`
	Name     string                            `json:"name"`
	Required bool                              `json:"required"`
}

type LifecycleDescriptor struct {
	Name                 string                              `json:"name"`
	Semantic             pastureruntime.EventSemantic        `json:"semantic"`
	Surface              pastureruntime.HookSurface          `json:"surface"`
	Blocking             pastureruntime.BlockingMode         `json:"blocking"`
	Mutation             pastureruntime.MutationMode         `json:"mutation"`
	Order                pastureruntime.HandlerOrder         `json:"order"`
	Reconciliation       pastureruntime.ReconciliationMode   `json:"reconciliation"`
	Failure              pastureruntime.FailureMode          `json:"failure"`
	DeclaredFailure      pastureruntime.FailureMode          `json:"declared_failure"`
	Evidence             pastureruntime.FailureEvidence      `json:"evidence"`
	AskEvidence          pastureruntime.FailureEvidence      `json:"ask_evidence"`
	PreAction            bool                                `json:"pre_action"`
	Response             pastureruntime.ResponseCapability   `json:"response"`
	StopLoop             pastureruntime.StopLoopPolicy       `json:"stop_loop"`
	IdentityPolicy       pastureruntime.IdentityPolicy       `json:"identity_policy"`
	Identities           []IdentityDescriptor                `json:"identities"`
	UnresolvedIdentities []pastureruntime.NativeIdentityKind `json:"unresolved_identities"`
}

func lifecycleDescriptors[E comparable](contract pastureruntime.LifecycleContract[E]) ([]LifecycleDescriptor, error) {
	result := make([]LifecycleDescriptor, 0, len(contract.Events()))
	for _, event := range contract.Events() {
		m, err := contract.Mapping(event)
		if err != nil {
			return nil, err
		}
		identities := make([]IdentityDescriptor, 0)
		for _, identity := range m.Identities() {
			identities = append(identities, IdentityDescriptor{identity.Kind(), identity.NativeName(), identity.Required()})
		}
		result = append(result, LifecycleDescriptor{
			Name: m.NativeName(), Semantic: m.Semantic(), Surface: m.Surface(), Blocking: m.Blocking(), Mutation: m.Mutation(),
			Order: m.Order(), Reconciliation: m.Reconciliation(), Failure: m.Failure(), DeclaredFailure: m.DeclaredFailure(),
			Evidence: m.Evidence(), AskEvidence: m.AskEvidence(), PreAction: m.PreAction(), Response: m.Response(), StopLoop: m.StopLoop(),
			Identities: identities, UnresolvedIdentities: m.UnresolvedIdentities(), IdentityPolicy: m.IdentityPolicy(),
		})
	}
	return result, nil
}

// RuntimeContracts includes the historical OpenCode dispatcher still supported
// by this binary, not just the three installation defaults.
func RuntimeContracts() []ir.RuntimeContractID {
	return []ir.RuntimeContractID{pastureruntime.ClaudeCode2_1_261().ID(), pastureruntime.OpenCode1_18_29().ID(), pastureruntime.OpenCode2_0_20().ID(), pastureruntime.Codex0_153_0().ID()}
}

// RuntimeSpecificationForContract is the pure owner descriptor used by both the
// document generator and compiled-profile agreement checks.
func RuntimeSpecificationForContract(id ir.RuntimeContractID) (RuntimeSpecification, error) {
	var contract pastureruntime.RuntimeContract
	var manifest registration.Manifest
	var lifecycle []LifecycleDescriptor
	var err error
	transport := "stdin-json-hooks"
	switch id {
	case pastureruntime.ClaudeCode2_1_261().ID():
		contract, manifest = pastureruntime.ClaudeCode2_1_261(), registration.ClaudeCode2_1_261()
		lifecycle, err = lifecycleDescriptors(pastureruntime.ClaudeCode2_1_261Lifecycle())
	case pastureruntime.OpenCode1_18_29().ID():
		contract, manifest = pastureruntime.OpenCode1_18_29(), registration.OpenCode1_18_29()
		lifecycle, err = lifecycleDescriptors(pastureruntime.OpenCode1_18_29Lifecycle())
		transport = "plugin-events"
	case pastureruntime.OpenCode2_0_20().ID():
		contract, manifest = pastureruntime.OpenCode2_0_20(), registration.OpenCode2_0_20()
		lifecycle, err = lifecycleDescriptors(pastureruntime.OpenCode2_0_20Lifecycle())
		transport = "plugin-events"
	case pastureruntime.Codex0_153_0().ID():
		contract, manifest = pastureruntime.Codex0_153_0(), registration.Codex0_153_0()
		lifecycle, err = lifecycleDescriptors(pastureruntime.Codex0_153_0Lifecycle())
	default:
		return RuntimeSpecification{}, failure("RuntimeSpecificationForContract", fmt.Sprintf("Runtime contract %q is not registered in this binary.", id), nil)
	}
	if err != nil {
		return RuntimeSpecification{}, err
	}
	v := contract.Versions()
	return RuntimeSpecification{
		Contract: id, Harness: contract.Harness(), Minimum: v.Min().String(), Maximum: v.Max().String(),
		HasUpperBound: v.HasUpperBound(), IncludesPrereleases: v.IncludesPrereleases(), NativeTransport: transport,
		Operations: contract.OperationDescriptors(), Effects: contract.EffectDescriptors(), Capabilities: contract.CapabilityDescriptors(),
		Registration: manifest, Lifecycle: lifecycle,
	}, nil
}

// GenerateRuntimeBody is the production owner codec used by explicit generation.
// It never reads source files or a store at runtime.
func GenerateRuntimeBody(contract ir.RuntimeContractID) ([]byte, error) {
	spec, err := RuntimeSpecificationForContract(contract)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(runtimeDocument{model.DefinitionRuntimeContract, runtimeID(contract), RuntimeDocumentVersion, model.DefinitionCanonicalJSON, spec})
	if err != nil {
		return nil, failure("GenerateRuntimeBody", "The typed runtime specification could not be encoded.", err)
	}
	return NormalizePayload(EvidenceKind, raw)
}

func runtimeID(contract ir.RuntimeContractID) model.DefinitionID {
	return model.DefinitionID("pasture.lifecycle.runtime-contract." + contract.String())
}

func NewRuntimeContract(contract ir.RuntimeContractID) (Document, error) {
	body, err := generatedRuntimeBody(contract)
	if err != nil {
		return Document{}, err
	}
	document, err := decodeRuntime(body)
	if err != nil {
		return Document{}, err
	}
	if document.ID() != runtimeID(contract) {
		return Document{}, failure("NewRuntimeContract", "The embedded document does not identify the selected runtime contract; regenerate the runtime documents.", nil)
	}
	document.ownerBuilt = true
	return document, nil
}

func decodeRuntime(payload []byte) (Document, error) {
	var wire runtimeDocument
	if err := strictDecode(payload, &wire); err != nil {
		return Document{}, failure("decodeRuntime", "The typed runtime document contains malformed, duplicate or unknown fields.", err)
	}
	// Version 1 is a historical codec arm, independent of the version emitted
	// by today's generator. Add a new arm rather than replacing this decoder.
	switch wire.Version {
	case 1:
	default:
		return Document{}, failure("decodeRuntime", "The runtime document version has no supported historical codec.", nil)
	}
	if wire.Kind != model.DefinitionRuntimeContract || wire.Version != 1 || wire.Codec != model.DefinitionCanonicalJSON || !wire.Specification.Contract.IsValid() || wire.ID != runtimeID(wire.Specification.Contract) || wire.Specification.Harness != wire.Specification.Contract.Harness() || wire.Specification.Registration.Harness != wire.Specification.Harness || wire.Specification.Registration.Version != wire.Specification.Minimum || len(wire.Specification.Operations) != len(ir.AllOperationKinds()) || len(wire.Specification.Lifecycle) == 0 {
		return Document{}, failure("decodeRuntime", "Runtime document kind, owner identity, codec, version or full catalog is invalid.", fmt.Errorf("kind=%d version=%d codec=%d id=%q want=%q contract=%q valid=%t harness=%q registration=%q operations=%d expected=%d lifecycle=%d", wire.Kind, wire.Version, wire.Codec, wire.ID, runtimeID(wire.Specification.Contract), wire.Specification.Contract, wire.Specification.Contract.IsValid(), wire.Specification.Harness, wire.Specification.Registration.Contract, len(wire.Specification.Operations), len(ir.AllOperationKinds()), len(wire.Specification.Lifecycle)))
	}
	if err := validateRuntimeSpecification(wire.Specification); err != nil {
		return Document{}, failure("decodeRuntime", "The runtime catalog violates its typed owner contract.", err)
	}
	normalized, err := NormalizePayload(EvidenceKind, payload)
	if err != nil {
		return Document{}, err
	}
	second, err := NormalizePayload(EvidenceKind, normalized)
	if err != nil || !bytes.Equal(normalized, second) {
		return Document{}, failure("decodeRuntime", "Journal normalization is not idempotent.", err)
	}
	return Document{kind: wire.Kind, id: wire.ID, version: wire.Version, codec: wire.Codec, body: normalized, content: model.ContentIdentity(sha256.Sum256(normalized))}, nil
}

func validateRuntimeSpecification(spec RuntimeSpecification) error {
	known := false
	for _, contract := range RuntimeContracts() {
		known = known || spec.Contract == contract
	}
	if !known {
		return fmt.Errorf("runtime contract %q is not owned by the versioned runtime codec", spec.Contract)
	}
	// The registration coordinate is versioned profile metadata, not a body
	// comparison with today's compiled content; historical bodies stay readable.
	owner, err := RuntimeSpecificationForContract(spec.Contract)
	if err != nil {
		return err
	}
	if spec.Registration.Contract != owner.Registration.Contract {
		return fmt.Errorf("registration coordinate does not identify runtime %q", spec.Contract)
	}
	minimum, err := pastureruntime.ParseHostVersion(spec.Minimum)
	if err != nil {
		return err
	}
	if spec.HasUpperBound {
		maximum, err := pastureruntime.ParseHostVersion(spec.Maximum)
		if err != nil {
			return err
		}
		if _, err := pastureruntime.NewVersionConstraint(minimum, maximum, spec.IncludesPrereleases); err != nil {
			return err
		}
	} else if spec.Maximum != "" || spec.IncludesPrereleases {
		return fmt.Errorf("floor contract has an upper bound or prerelease flag")
	}
	if spec.NativeTransport != "stdin-json-hooks" && spec.NativeTransport != "plugin-events" {
		return fmt.Errorf("unknown native transport %q", spec.NativeTransport)
	}
	ops := make(map[string]bool)
	for _, op := range spec.Operations {
		if err := validateBinding(op); err != nil {
			return err
		}
		if ops[op.ID] || !op.Class.IsValid() || op.InputSchema == "" || op.OutputSchema == "" || strings.TrimSpace(op.Semantics.Summary) == "" {
			return fmt.Errorf("invalid or duplicate operation descriptor %q", op.ID)
		}
		ops[op.ID] = true
	}
	seenEffects := make(map[string]bool)
	for _, effect := range spec.Effects {
		if _, err := ir.NewEffectID(effect.ID); err != nil {
			return err
		}
		if seenEffects[effect.ID] {
			return fmt.Errorf("duplicate effect %q", effect.ID)
		}
		seenEffects[effect.ID] = true
		if err := validateBinding(effect); err != nil {
			return err
		}
	}
	seenCapabilities := make(map[ir.CapabilityID]bool)
	for _, capability := range spec.Capabilities {
		if !capability.ID.IsValid() || !capability.Version.IsValid() || !capability.Minimum.IsValid() || !capability.Maximum.IsValid() || seenCapabilities[capability.ID] || capability.Binding.ID != string(capability.ID) {
			return fmt.Errorf("invalid or duplicate capability %q", capability.ID)
		}
		seenCapabilities[capability.ID] = true
		min, _ := pastureruntime.ParseHostVersion(string(capability.Minimum))
		max, _ := pastureruntime.ParseHostVersion(string(capability.Maximum))
		version, _ := pastureruntime.ParseHostVersion(string(capability.Version))
		if pastureruntime.ComparePrecedence(min, version) > 0 || pastureruntime.ComparePrecedence(version, max) > 0 {
			return fmt.Errorf("capability %q has an inverted or out-of-range version", capability.ID)
		}
		if err := validateBinding(capability.Binding); err != nil {
			return err
		}
	}
	for _, kind := range ir.AllOperationKinds() {
		id, _ := ir.CoreOperationID(kind)
		if !ops[id.String()] {
			return fmt.Errorf("missing core operation %q", id)
		}
	}
	seen := make(map[string]bool)
	for _, event := range spec.Lifecycle {
		if event.Name == "" || seen[event.Name] || !event.Semantic.IsValid() || !event.Surface.IsValid() || !event.Blocking.IsValid() || !event.Mutation.IsValid() || !event.Order.IsValid() || !event.Reconciliation.IsValid() || !event.Failure.IsValid() || !event.DeclaredFailure.IsValid() || !event.Response.IsValid() || !event.StopLoop.IsValid() || !event.IdentityPolicy.IsValid() {
			return fmt.Errorf("invalid or duplicate lifecycle row %q", event.Name)
		}
		seen[event.Name] = true
		identities := make(map[string]bool)
		for _, id := range event.Identities {
			if !id.Kind.IsValid() || id.Name == "" || identities[id.Name] {
				return fmt.Errorf("invalid identity mapping in %q", event.Name)
			}
			identities[id.Name] = true
		}
		for _, kind := range event.UnresolvedIdentities {
			if !kind.IsValid() {
				return fmt.Errorf("invalid unresolved identity in %q", event.Name)
			}
		}
	}
	registered := make(map[string]bool)
	for _, event := range spec.Registration.Events {
		if !seen[event.NativeName] || registered[event.NativeName] || event.Kind == 0 {
			return fmt.Errorf("invalid or duplicate registration row %q", event.NativeName)
		}
		registered[event.NativeName] = true
	}
	if len(registered) != len(seen) {
		return fmt.Errorf("registration and lifecycle catalogs are incomplete")
	}
	return nil
}

func validateBinding(binding pastureruntime.BindingDescriptor) error {
	if binding.ID == "" || !binding.Class.IsValid() || binding.InputSchema == "" || binding.OutputSchema == "" || strings.TrimSpace(binding.Semantics.Summary) == "" || strings.TrimSpace(binding.Semantics.Result) == "" {
		return fmt.Errorf("incomplete binding %q", binding.ID)
	}
	seen := make(map[string]bool)
	for _, id := range binding.Effects {
		if _, err := ir.NewEffectID(id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate bound effect %q", id)
		}
		seen[id] = true
	}
	switch binding.Class {
	case effects.RuntimeClassNative:
		_, err := pastureruntime.NewNativeCall(binding.NativeCall, binding.Arguments, binding.ResultSemantics, binding.ContextInheritance)
		return err
	case effects.RuntimeClassParentMediated:
		if strings.TrimSpace(binding.Mediator) == "" || strings.TrimSpace(binding.Instruction) == "" {
			return fmt.Errorf("incomplete mediated binding %q", binding.ID)
		}
	case effects.RuntimeClassSemanticInstruction:
		if strings.TrimSpace(binding.Template) == "" {
			return fmt.Errorf("incomplete semantic binding %q", binding.ID)
		}
	case effects.RuntimeClassUnsupported:
		if strings.TrimSpace(binding.UnsupportedReason) == "" {
			return fmt.Errorf("unsupported binding %q has no reason", binding.ID)
		}
	}
	return nil
}

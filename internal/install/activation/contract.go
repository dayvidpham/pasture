// Package activation defines the versioned native-activation contract that binds
// each generated component (harness/extension identity plus its published
// artifact bundle and runtime contract) to exactly one closed activation
// strategy.
//
// Installer mechanics live here, separate from the protocol-semantic runtime IR.
// Values are opaque and validated: an ExhaustiveComponentActivations can be
// built only with exactly one skills, one agents, and one hooks component, and
// LookupComponentActivation accepts only an opaque ComponentDescriptor, never a
// raw string or extension literal. Wrong-harness, missing, and incompatible
// lookups return explicit six-part errors rather than a silent zero value.
package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// ActivationContractID identifies a versioned activation contract. It is a
// distinct string type so it cannot be confused with a RuntimeContractID or a
// bare identifier; construct it with NewActivationContractID.
type ActivationContractID string

// NewActivationContractID validates a non-empty activation contract id.
func NewActivationContractID(value string) (ActivationContractID, error) {
	if value == "" {
		return "", cell.NewFault(
			"activation id construction", "non-empty activation contract id",
			"the activation contract id is empty",
			"internal/install/activation.NewActivationContractID", "identifying an activation contract",
			"the contract cannot be referenced or bound",
			"provide a stable id such as claude-code/activation@2.1.210", nil,
		)
	}
	return ActivationContractID(value), nil
}

// String returns the raw id.
func (id ActivationContractID) String() string { return string(id) }

// ComponentActivation binds one cell to its closed activation strategy.
type ComponentActivation struct {
	cell     cell.Cell
	strategy ActivationStrategy
	valid    bool
}

// NewComponentActivation validates a cell/strategy binding. A nil strategy is
// rejected so a zero activation can never be a public lookup result.
func NewComponentActivation(c cell.Cell, strategy ActivationStrategy) (ComponentActivation, error) {
	if !c.IsValid() {
		return ComponentActivation{}, cell.NewFault(
			"component activation construction", "valid cell",
			"the cell is the invalid zero value",
			"internal/install/activation.NewComponentActivation", "binding a component activation",
			"the activation cannot be addressed",
			"construct the cell with cell.New", nil,
		)
	}
	if strategy == nil {
		return ComponentActivation{}, cell.NewFault(
			"component activation construction", "non-nil strategy",
			fmt.Sprintf("cell %s has no activation strategy", c),
			"internal/install/activation.NewComponentActivation", "binding a component activation",
			"a component with no strategy could not be activated",
			"pass a NativePlugin, DirectFile, or NativePluginPendingTrust strategy", nil,
		)
	}
	return ComponentActivation{cell: c, strategy: strategy, valid: true}, nil
}

// Cell returns the bound cell.
func (a ComponentActivation) Cell() cell.Cell { return a.cell }

// Strategy returns the closed activation strategy.
func (a ComponentActivation) Strategy() ActivationStrategy { return a.strategy }

// IsValid reports whether the activation was validly constructed.
func (a ComponentActivation) IsValid() bool { return a.valid }

// ExhaustiveComponentActivations holds exactly one skills, one agents, and one
// hooks activation for a single harness. It has no public map, so no caller can
// inject an extra, missing, or duplicate axis.
type ExhaustiveComponentActivations struct {
	harness ir.HarnessID
	byAxis  map[string]ComponentActivation
	valid   bool
}

// NewExhaustiveComponentActivations validates that exactly one activation is
// supplied per axis, that all three share one harness, and that each activation
// sits on its own axis.
func NewExhaustiveComponentActivations(skills, agents, hooks ComponentActivation) (ExhaustiveComponentActivations, error) {
	provided := []struct {
		want cell.Extension
		act  ComponentActivation
	}{
		{cell.SkillsAxis(), skills},
		{cell.AgentsAxis(), agents},
		{cell.HooksAxis(), hooks},
	}
	harness := skills.cell.Harness()
	byAxis := make(map[string]ComponentActivation, 3)
	for _, p := range provided {
		if !p.act.IsValid() {
			return ExhaustiveComponentActivations{}, cell.NewFault(
				"exhaustive activations construction", "one valid activation per axis",
				fmt.Sprintf("the %s activation is invalid or missing", p.want),
				"internal/install/activation.NewExhaustiveComponentActivations", "assembling a harness's activations",
				"the harness would have an unbound component",
				fmt.Sprintf("provide a valid activation for the %s axis", p.want), nil,
			)
		}
		if p.act.cell.Extension().String() != p.want.String() {
			return ExhaustiveComponentActivations{}, cell.NewFault(
				"exhaustive activations construction", "each activation on its own axis",
				fmt.Sprintf("the %s slot holds a %s activation", p.want, p.act.cell.Extension()),
				"internal/install/activation.NewExhaustiveComponentActivations", "assembling a harness's activations",
				"a mis-slotted activation would activate the wrong component",
				fmt.Sprintf("pass the %s activation in the %s slot", p.want, p.want), nil,
			)
		}
		if p.act.cell.Harness() != harness {
			return ExhaustiveComponentActivations{}, cell.NewFault(
				"exhaustive activations construction", "one harness across all axes",
				fmt.Sprintf("axis %s is harness %s but skills is harness %s", p.want, p.act.cell.Harness(), harness),
				"internal/install/activation.NewExhaustiveComponentActivations", "assembling a harness's activations",
				"mixing harnesses in one contract would bind the wrong native manager",
				"provide all three activations for the same harness", nil,
			)
		}
		byAxis[p.want.String()] = p.act
	}
	return ExhaustiveComponentActivations{harness: harness, byAxis: byAxis, valid: true}, nil
}

// Harness returns the harness these activations belong to.
func (e ExhaustiveComponentActivations) Harness() ir.HarnessID { return e.harness }

// ComponentDescriptor is an opaque, validated component identity accepted by
// LookupComponentActivation. It can be built only from a valid cell, so raw
// extension literals and strings are not public lookup states.
type ComponentDescriptor struct {
	cell  cell.Cell
	valid bool
}

// NewComponentDescriptor validates and owns a component identity.
func NewComponentDescriptor(c cell.Cell) (ComponentDescriptor, error) {
	if !c.IsValid() {
		return ComponentDescriptor{}, cell.NewFault(
			"component descriptor construction", "valid cell",
			"the cell is the invalid zero value",
			"internal/install/activation.NewComponentDescriptor", "identifying a component for lookup",
			"the lookup key cannot be resolved",
			"construct the cell with cell.New", nil,
		)
	}
	return ComponentDescriptor{cell: c, valid: true}, nil
}

// Cell exposes the descriptor's coordinate.
func (d ComponentDescriptor) Cell() cell.Cell { return d.cell }

// ActivationContract binds one harness's exhaustive component activations under
// a versioned id, host version constraint, and version probe schema.
type ActivationContract struct {
	id           ActivationContractID
	harness      ir.HarnessID
	hostVersions runtime.VersionConstraint
	versionProbe CommandSchema
	components   ExhaustiveComponentActivations
	valid        bool
}

// NewActivationContract validates and assembles an activation contract.
func NewActivationContract(
	id ActivationContractID,
	harness ir.HarnessID,
	hostVersions runtime.VersionConstraint,
	versionProbe CommandSchema,
	components ExhaustiveComponentActivations,
) (ActivationContract, error) {
	if id.String() == "" {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "identified contract",
			"the activation contract id is empty",
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the contract cannot be referenced",
			"construct the id with NewActivationContractID", nil,
		)
	}
	if !harness.IsValid() {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "known harness",
			fmt.Sprintf("harness %q is not recognized", harness),
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the contract cannot bind a native manager",
			"pass one of claude-code, opencode, codex", nil,
		)
	}
	if !components.valid {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "exhaustive component activations",
			"the component activations were not validly assembled",
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the contract would have missing or unbound components",
			"build the activations with NewExhaustiveComponentActivations", nil,
		)
	}
	if components.harness != harness {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "components match the contract harness",
			fmt.Sprintf("components are harness %s but the contract is harness %s", components.harness, harness),
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the contract would bind the wrong harness's native manager",
			"assemble the activations for the same harness as the contract", nil,
		)
	}
	if !hostVersions.IsValid() {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "valid host version constraint",
			"the host version constraint is invalid",
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the installer cannot bound-check the live host version",
			"construct the constraint with runtime.NewExactVersion or runtime.NewVersionConstraint", nil,
		)
	}
	if !versionProbe.IsValid() {
		return ActivationContract{}, cell.NewFault(
			"activation contract construction", "valid version probe schema",
			"the version probe command schema is invalid",
			"internal/install/activation.NewActivationContract", "assembling an activation contract",
			"the installer cannot probe the live host version before mutation",
			"construct the probe with NewCommandSchema, e.g. claude --version", nil,
		)
	}
	return ActivationContract{
		id:           id,
		harness:      harness,
		hostVersions: hostVersions,
		versionProbe: versionProbe,
		components:   components,
		valid:        true,
	}, nil
}

// ID returns the contract id.
func (c ActivationContract) ID() ActivationContractID { return c.id }

// Harness returns the contract harness.
func (c ActivationContract) Harness() ir.HarnessID { return c.harness }

// HostVersions returns the accepted host version constraint.
func (c ActivationContract) HostVersions() runtime.VersionConstraint { return c.hostVersions }

// VersionProbe returns the schema used to probe the live host version.
func (c ActivationContract) VersionProbe() CommandSchema { return c.versionProbe }

// IsValid reports whether the contract was validly constructed.
func (c ActivationContract) IsValid() bool { return c.valid }

// LookupComponentActivation resolves a descriptor against a contract. It returns
// an explicit error for an invalid contract, an invalid descriptor, or a
// wrong-harness component; it never returns a zero activation as a success.
func LookupComponentActivation(contract ActivationContract, component ComponentDescriptor) (ComponentActivation, error) {
	if !contract.valid {
		return ComponentActivation{}, cell.NewFault(
			"component lookup", "valid activation contract",
			"the activation contract is the invalid zero value",
			"internal/install/activation.LookupComponentActivation", "resolving a component activation",
			"no activation can be resolved",
			"construct the contract with NewActivationContract", nil,
		)
	}
	if !component.valid {
		return ComponentActivation{}, cell.NewFault(
			"component lookup", "opaque validated descriptor",
			"the component descriptor is the invalid zero value",
			"internal/install/activation.LookupComponentActivation", "resolving a component activation",
			"a raw string or extension literal is not a valid lookup key",
			"construct the descriptor with NewComponentDescriptor from a valid cell", nil,
		)
	}
	if component.cell.Harness() != contract.harness {
		return ComponentActivation{}, cell.NewFault(
			"component lookup", "component belongs to the contract harness",
			fmt.Sprintf("component %s is harness %s but the contract is harness %s",
				component.cell, component.cell.Harness(), contract.harness),
			"internal/install/activation.LookupComponentActivation", "resolving a component activation",
			"looking a component up in the wrong harness's contract would bind the wrong strategy",
			fmt.Sprintf("look up %s in the %s activation contract", component.cell, component.cell.Harness()), nil,
		)
	}
	act, ok := contract.components.byAxis[component.cell.Extension().String()]
	if !ok || !act.valid {
		return ComponentActivation{}, cell.NewFault(
			"component lookup", "bound component activation",
			fmt.Sprintf("no activation is bound for %s", component.cell),
			"internal/install/activation.LookupComponentActivation", "resolving a component activation",
			"the component is unbound and cannot be activated",
			"ensure the contract's exhaustive activations include this axis", nil,
		)
	}
	return act, nil
}

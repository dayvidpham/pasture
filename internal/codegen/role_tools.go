package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// roleToolSlot is one position in the canonical role-tool order. Exactly one
// field is set. The order is the one home for how a role's typed capabilities
// become a stable native list: a role declares membership in Tools or
// Operations, and this sequence decides where the name appears.
type roleToolSlot struct {
	tool      ir.HarnessTool
	operation ir.OperationKind
}

var roleToolSequence = []roleToolSlot{
	{tool: ir.HarnessToolRead},
	{tool: ir.HarnessToolGlob},
	{tool: ir.HarnessToolGrep},
	{tool: ir.HarnessToolBash},
	{tool: ir.HarnessToolWebFetch},
	{operation: ir.OperationInvokeSkill},
	{operation: ir.OperationDelegateAssignment},
	{tool: ir.HarnessToolEdit},
	{tool: ir.HarnessToolWrite},
	{tool: ir.HarnessToolTask},
	{operation: ir.OperationSendAssignmentMessage},
}

// roleTarget is one generation target together with the runtime identity and
// pinned contract its native names resolve through. The pair is derived from
// the two registries — harnessRegistry for the target set, PinnedContracts for
// the contract — so a target cannot be registered in one place and omitted
// from the other.
type roleTarget struct {
	name     HarnessName
	harness  ir.HarnessID
	contract runtime.RuntimeContract
}

// roleTargets returns every enabled target paired with its pinned contract.
// The enabled harness set is the closed ir vocabulary, harnessRegistry supplies
// the generation target for each, and PinnedContracts supplies the contract, so
// a harness missing from either registry fails here rather than being silently
// excluded from the foreign-name scan.
func roleTargets() ([]roleTarget, error) {
	targetsByName := make(map[ir.HarnessID]HarnessName, len(harnessRegistry))
	for name := range harnessRegistry {
		targetsByName[ir.HarnessID(name)] = name
	}
	contracts := make(map[ir.HarnessID]runtime.RuntimeContract, len(runtime.PinnedContracts()))
	for _, contract := range runtime.PinnedContracts() {
		contracts[contract.Harness()] = contract
	}

	targets := make([]roleTarget, 0, len(contracts))
	for _, harness := range ir.EnabledHarnessIDs() {
		name, registered := targetsByName[harness]
		if !registered {
			return nil, fmt.Errorf(
				"enabled harness %q has no generation target — "+
					"why: role tool names are derived per generation target; "+
					"where: internal/codegen/harness.go harnessRegistry; "+
					"fix: register the target in harnessRegistry or remove the harness from ir.EnabledHarnessIDs",
				harness,
			)
		}
		contract, pinned := contracts[harness]
		if !pinned {
			return nil, fmt.Errorf(
				"enabled harness %q has no pinned runtime contract — "+
					"why: role tool names resolve through the contract's lowering table; "+
					"where: internal/runtime/profiles.go PinnedContracts; "+
					"fix: add the harness's pinned contract or remove it from ir.EnabledHarnessIDs",
				harness,
			)
		}
		targets = append(targets, roleTarget{name: name, harness: harness, contract: contract})
	}
	return targets, nil
}

// roleTargetFor resolves one generation target name to its runtime identity and
// pinned contract.
func roleTargetFor(name HarnessName) (roleTarget, error) {
	targets, err := roleTargets()
	if err != nil {
		return roleTarget{}, err
	}
	for _, target := range targets {
		if target.name == name {
			return target, nil
		}
	}
	return roleTarget{}, fmt.Errorf(
		"target %q has no runtime identity and pinned contract — "+
			"why: role tool names resolve through the target's contract; "+
			"where: internal/codegen/role_tools.go roleTargetFor; "+
			"fix: register the target in harnessRegistry and PinnedContracts",
		name,
	)
}

// roleHasToolSurface reports whether a role emits an agent definition at all.
func roleHasToolSurface(spec RoleSpec) bool {
	return len(spec.Tools) > 0 || len(spec.Operations) > 0
}

// nativeToolName is the ONE harness-tool lowering used by both the role tool
// surface and body-marker resolution. It reports ok=false when the target's
// contract declares no native name for the capability.
func nativeToolName(harness ir.HarnessID, tool ir.HarnessTool) (name string, ok bool, err error) {
	lowering, err := runtime.LookupHarnessTool(harness, tool)
	if err != nil {
		return "", false, err
	}
	if !lowering.Available() {
		return "", false, nil
	}
	return lowering.NativeName(), true, nil
}

// nativeOperationName is the ONE operation lowering used by both the role tool
// surface and body-marker resolution. ok is false when the kind is unsupported
// on the contract or classifies as non-native; a kind with no core descriptor
// is a fault, not an empty result.
func nativeOperationName(contract runtime.RuntimeContract, kind ir.OperationKind) (name string, ok bool, err error) {
	descriptor, registered := runtime.CoreOperationDescriptorFor(kind)
	if !registered {
		return "", false, fmt.Errorf(
			"operation %q has no core descriptor — "+
				"why: only a registered orchestration kind can be lowered; "+
				"where: internal/codegen/role_tools.go nativeOperationName; "+
				"fix: use one of ir.AllOperationKinds",
			kind,
		)
	}
	binding, err := runtime.LookupOperationBinding(contract, descriptor)
	if err != nil {
		// Unsupported on this contract: the operation has no native call.
		return "", false, nil
	}
	call, native := binding.Native()
	if !native {
		return "", false, nil
	}
	return call.CallName(), true, nil
}

// RoleToolNames resolves one role's typed capabilities to the target's native
// tool names, in canonical order. Harness tools come from the harness-tool
// lowering table and operations from the pinned runtime contract, so no native
// name is written here.
func RoleToolNames(targetName HarnessName, spec RoleSpec) ([]string, error) {
	target, err := roleTargetFor(targetName)
	if err != nil {
		return nil, err
	}

	tools := make(map[ir.HarnessTool]struct{}, len(spec.Tools))
	for _, tool := range spec.Tools {
		tools[tool] = struct{}{}
	}
	operations := make(map[ir.OperationKind]struct{}, len(spec.Operations))
	for _, operation := range spec.Operations {
		operations[operation] = struct{}{}
	}

	var names []string
	seen := make(map[string]struct{})
	for _, slot := range roleToolSequence {
		var name string
		if slot.tool != "" {
			if _, wanted := tools[slot.tool]; !wanted {
				continue
			}
			resolved, ok, err := nativeToolName(target.harness, slot.tool)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			name = resolved
		} else {
			if _, wanted := operations[slot.operation]; !wanted {
				continue
			}
			resolved, ok, err := nativeOperationName(target.contract, slot.operation)
			if err != nil {
				return nil, fmt.Errorf(
					"role %q names operation %q with no native lowering — "+
						"why: a role may only declare a registered orchestration kind; "+
						"where: internal/codegen/role_tools.go RoleToolNames; "+
						"fix: use one of ir.AllOperationKinds: %w",
					spec.Id, slot.operation, err,
				)
			}
			if !ok {
				continue
			}
			name = resolved
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

// mustRoleToolNames is the static-data path for schema generation: the
// registries are validated before generation, so a failure here is a
// programming error, matching the panic policy of marshalSection.
func mustRoleToolNames(target HarnessName, spec RoleSpec) []string {
	names, err := RoleToolNames(target, spec)
	if err != nil {
		panic(fmt.Sprintf("codegen.mustRoleToolNames(%s, %s): %v", target, spec.Id, err))
	}
	return names
}

func roleHasPolicy(spec RoleSpec, policy RolePolicy) bool {
	for _, candidate := range spec.Policies {
		if candidate == policy {
			return true
		}
	}
	return false
}

// roleToolSequenceRendersOperation reports whether the canonical role-tool
// order has a slot for one declared operation. A declared operation with no
// slot would be silently dropped from the emitted surface, so validation
// refuses it instead.
func roleToolSequenceRendersOperation(operation ir.OperationKind) bool {
	for _, slot := range roleToolSequence {
		if slot.operation == operation {
			return true
		}
	}
	return false
}

// ValidateRoleToolSurface proves the static role data honours its own typed
// policies and closed sets. It runs as part of RequireOperationalSource, so a
// violation fails the build before any file is written.
func ValidateRoleToolSurface() error {
	for roleID, spec := range RoleSpecs {
		if err := validateRoleSpec(roleID, spec); err != nil {
			return err
		}
	}
	return nil
}

func validateRoleSpec(roleID protocol.RoleId, spec RoleSpec) error {
	for _, policy := range spec.Policies {
		if !policy.IsValid() {
			return fmt.Errorf(
				"role %q declares unregistered policy %q — "+
					"why: the role-policy set is closed; "+
					"where: RoleSpecs[%q].Policies; "+
					"fix: use one of AllRolePolicies",
				roleID, policy, roleID,
			)
		}
	}
	for _, tool := range spec.Tools {
		if !tool.IsValid() {
			return fmt.Errorf(
				"role %q declares unregistered harness tool %q — "+
					"why: the harness-tool set is closed; "+
					"where: RoleSpecs[%q].Tools; "+
					"fix: use one of ir.AllHarnessTools",
				roleID, tool, roleID,
			)
		}
	}
	for _, operation := range spec.Operations {
		if _, registered := runtime.CoreOperationDescriptorFor(operation); !registered {
			return fmt.Errorf(
				"role %q declares unregistered operation kind %q — "+
					"why: a role may only name a registered orchestration kind; "+
					"where: RoleSpecs[%q].Operations; "+
					"fix: use one of ir.AllOperationKinds",
				roleID, operation, roleID,
			)
		}
		if !roleToolSequenceRendersOperation(operation) {
			return fmt.Errorf(
				"role %q declares operation %q with no rendering slot — "+
					"why: roleToolSequence renders only the operations a role surface can express, and this kind would be silently dropped; "+
					"where: RoleSpecs[%q].Operations versus roleToolSequence; "+
					"fix: add a slot for %q to roleToolSequence or remove it from the role",
				roleID, operation, roleID, operation,
			)
		}
	}

	if roleHasPolicy(spec, RolePolicyReadOnly) {
		for _, tool := range spec.Tools {
			if tool == ir.HarnessToolEdit || tool == ir.HarnessToolWrite {
				return fmt.Errorf(
					"role %q is read-only but declares harness tool %q — "+
						"why: a read-only policy and a write capability contradict each other; "+
						"where: RoleSpecs[%q].Policies versus .Tools; "+
						"fix: remove the write tool or remove %q",
					roleID, tool, roleID, RolePolicyReadOnly,
				)
			}
		}
	}
	return nil
}

// targetDeclaredToolNames derives the complete native tool vocabulary of one
// target from its own lowerings: every harness tool plus the contract's native
// operation names. It is the source of the foreign-name scan, so the scan has
// no authored name list to drift.
func targetDeclaredToolNames(target roleTarget) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	for _, tool := range ir.AllHarnessTools() {
		lowering, err := runtime.LookupHarnessTool(target.harness, tool)
		if err != nil {
			return nil, err
		}
		if lowering.Available() {
			names[strings.ToLower(lowering.NativeName())] = struct{}{}
		}
	}
	operations, err := runtime.NativeOperationNames(target.contract)
	if err != nil {
		return nil, err
	}
	for _, name := range operations {
		names[strings.ToLower(name)] = struct{}{}
	}
	return names, nil
}

// foreignToolNames returns the native tool names declared by OTHER targets and
// not by target, de-duplicated and sorted. The target set and the names are
// both derived, so a registry or contract change updates the guard.
func foreignToolNames(targetName HarnessName) ([]string, error) {
	targets, err := roleTargets()
	if err != nil {
		return nil, err
	}
	var own map[string]struct{}
	foreign := make(map[string]struct{})
	for _, target := range targets {
		names, err := targetDeclaredToolNames(target)
		if err != nil {
			return nil, err
		}
		if target.name == targetName {
			own = names
			continue
		}
		for name := range names {
			foreign[name] = struct{}{}
		}
	}
	if own == nil {
		return nil, fmt.Errorf(
			"target %q is not registered — why: its foreign-name set is the difference from the other targets; "+
				"where: internal/codegen/harness.go harnessRegistry; fix: register the target",
			targetName,
		)
	}
	out := make([]string, 0, len(foreign))
	for name := range foreign {
		if _, isOwn := own[name]; isOwn {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

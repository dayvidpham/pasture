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

// canonicalTargets is the closed, ordered set of generation targets the
// foreign-name scan compares. Adding a target here is a deliberate act, in the
// same change that adds its lowering table.
var canonicalTargets = []HarnessName{HarnessClaudeCode, HarnessOpenCode, HarnessCodex}

// roleHasToolSurface reports whether a role emits an agent definition at all.
func roleHasToolSurface(spec RoleSpec) bool {
	return len(spec.Tools) > 0 || len(spec.Operations) > 0
}

// targetHarnessID maps a generation target to its runtime harness identity.
func targetHarnessID(name HarnessName) (ir.HarnessID, error) {
	switch name {
	case HarnessClaudeCode:
		return ir.HarnessClaudeCode, nil
	case HarnessOpenCode:
		return ir.HarnessOpenCode, nil
	case HarnessCodex:
		return ir.HarnessCodex, nil
	default:
		return "", fmt.Errorf(
			"target %q has no runtime harness identity — "+
				"why: role tool names resolve through the pinned runtime contract of the target; "+
				"where: internal/codegen/role_tools.go targetHarnessID; "+
				"fix: register the target in ResolveHarness and add its harness identity here",
			name,
		)
	}
}

// targetContract returns the pinned runtime contract for a generation target.
func targetContract(name HarnessName) (runtime.RuntimeContract, error) {
	switch name {
	case HarnessClaudeCode:
		return runtime.ClaudeCode2_1_261(), nil
	case HarnessOpenCode:
		return runtime.OpenCode2_0_20(), nil
	case HarnessCodex:
		return runtime.Codex0_153_0(), nil
	default:
		return runtime.RuntimeContract{}, fmt.Errorf(
			"target %q has no pinned runtime contract — "+
				"why: role tool names resolve through the contract's lowering table; "+
				"where: internal/codegen/role_tools.go targetContract; "+
				"fix: add the target to ResolveHarness and return its pinned contract here",
			name,
		)
	}
}

// RoleToolNames resolves one role's typed capabilities to the target's native
// tool names, in canonical order. Harness tools come from the harness-tool
// lowering table and operations from the pinned runtime contract, so no native
// name is written here.
func RoleToolNames(target HarnessName, spec RoleSpec) ([]string, error) {
	harness, err := targetHarnessID(target)
	if err != nil {
		return nil, err
	}
	contract, err := targetContract(target)
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
			lowering, err := runtime.LookupHarnessTool(harness, slot.tool)
			if err != nil {
				return nil, err
			}
			if !lowering.Available() {
				continue
			}
			name = lowering.NativeName()
		} else {
			if _, wanted := operations[slot.operation]; !wanted {
				continue
			}
			descriptor, ok := runtime.CoreOperationDescriptorFor(slot.operation)
			if !ok {
				return nil, fmt.Errorf(
					"role %q names operation %q with no core descriptor — "+
						"why: a role may only declare a registered orchestration kind; "+
						"where: internal/codegen/role_tools.go RoleToolNames; "+
						"fix: use one of ir.AllOperationKinds",
					spec.Id, slot.operation,
				)
			}
			binding, err := runtime.LookupOperationBinding(contract, descriptor)
			if err != nil {
				// Unsupported on this contract: the operation contributes no
				// native tool name, exactly as it contributes no binding.
				continue
			}
			call, native := binding.Native()
			if !native {
				continue
			}
			name = call.CallName()
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

func roleOwnsPhase(spec RoleSpec, phase protocol.PhaseId) bool {
	for _, owned := range spec.OwnedPhases {
		if owned == phase {
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
	}

	readOnly := roleHasPolicy(spec, RolePolicyReadOnly)
	if readOnly {
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
	if roleHasPolicy(spec, RolePolicyHandoff) && !roleOwnsPhase(spec, protocol.PhaseHandoff) {
		return fmt.Errorf(
			"role %q declares policy %q but owns no handoff phase — "+
				"why: a handoff policy must be backed by the phase that converts it into work; "+
				"where: RoleSpecs[%q].Policies versus .OwnedPhases; "+
				"fix: add %q to OwnedPhases or remove the policy",
			roleID, RolePolicyHandoff, roleID, protocol.PhaseHandoff,
		)
	}
	return nil
}

// targetDeclaredToolNames derives the complete native tool vocabulary of one
// target from its own lowerings: every harness tool plus every core operation
// that resolves natively. It is the source of the foreign-name scan, so the
// scan has no authored name list to drift.
func targetDeclaredToolNames(target HarnessName) (map[string]struct{}, error) {
	harness, err := targetHarnessID(target)
	if err != nil {
		return nil, err
	}
	contract, err := targetContract(target)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{})
	for _, tool := range ir.AllHarnessTools() {
		lowering, err := runtime.LookupHarnessTool(harness, tool)
		if err != nil {
			return nil, err
		}
		if lowering.Available() {
			names[strings.ToLower(lowering.NativeName())] = struct{}{}
		}
	}
	for _, kind := range ir.AllOperationKinds() {
		descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
		if !ok {
			continue
		}
		binding, err := runtime.LookupOperationBinding(contract, descriptor)
		if err != nil {
			continue
		}
		if call, native := binding.Native(); native {
			names[strings.ToLower(call.CallName())] = struct{}{}
		}
	}
	return names, nil
}

// foreignToolNames returns the native tool names declared by OTHER targets and
// not by target, de-duplicated and sorted. The list is derived: a contract
// change updates it without editing a table here.
func foreignToolNames(target HarnessName) ([]string, error) {
	own, err := targetDeclaredToolNames(target)
	if err != nil {
		return nil, err
	}
	foreign := make(map[string]struct{})
	for _, other := range canonicalTargets {
		if other == target {
			continue
		}
		names, err := targetDeclaredToolNames(other)
		if err != nil {
			return nil, err
		}
		for name := range names {
			if _, isOwn := own[name]; !isOwn {
				foreign[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(foreign))
	for name := range foreign {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

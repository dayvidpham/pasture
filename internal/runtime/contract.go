package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// CoreRuntimeBindings is the exhaustive classification of the closed core
// orchestration vocabulary plus the modeled effects a contract lowers. Every
// core operation kind (see ir.AllOperationKinds) must be bound exactly once, to
// exactly one runtime class, so a contract can never be missing a plan for a
// core operation. Effect descriptors are open-ended and are required only to be
// unique.
type CoreRuntimeBindings struct {
	operations  map[string]boundEntry
	effects     map[string]boundEntry
	constructed bool
}

// NewCoreRuntimeBindings validates that operations cover every core operation
// kind exactly once and that effects are unique, then returns the immutable
// core binding set.
func NewCoreRuntimeBindings(operations []OperationBinding, effectBindings []EffectBinding) (CoreRuntimeBindings, error) {
	required := requiredCoreOperationIDs()
	ops := make(map[string]boundEntry, len(required))
	for index, binding := range operations {
		if !binding.valid() {
			return CoreRuntimeBindings{}, runtimeError(
				fmt.Sprintf("core operation binding %d is zero or invalid", index),
				"a core binding set may contain only constructor-produced bindings",
				"NewCoreRuntimeBindings", "the contract cannot be built",
				"build every binding with a NativeOperationBinding-family constructor", nil,
			)
		}
		if _, ok := required[binding.id()]; !ok {
			return CoreRuntimeBindings{}, runtimeError(
				fmt.Sprintf("operation %q is not a core orchestration operation", binding.id()),
				"core bindings classify exactly the closed core operation set; extension operations bind through BindCapability",
				"NewCoreRuntimeBindings", "the contract would carry an out-of-vocabulary core binding",
				"remove the binding or classify it as a capability contribution", nil,
			)
		}
		if _, duplicate := ops[binding.id()]; duplicate {
			return CoreRuntimeBindings{}, runtimeError(
				fmt.Sprintf("core operation %q is bound twice", binding.id()),
				"one operation must have exactly one runtime classification",
				"NewCoreRuntimeBindings", "the contract's classification would be ambiguous",
				"bind each core operation exactly once", nil,
			)
		}
		ops[binding.id()] = binding.entry
	}
	if len(ops) != len(required) {
		missing := make([]string, 0)
		for id := range required {
			if _, ok := ops[id]; !ok {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		return CoreRuntimeBindings{}, runtimeError(
			fmt.Sprintf("core binding set is missing operations: %s", strings.Join(missing, ", ")),
			"every core orchestration operation must be classified so no operation falls through unclassified",
			"NewCoreRuntimeBindings", "generated artifacts could use an unclassified operation",
			"bind every core operation returned by ir.AllOperationKinds", nil,
		)
	}

	effs := make(map[string]boundEntry, len(effectBindings))
	for index, binding := range effectBindings {
		if !binding.valid() {
			return CoreRuntimeBindings{}, runtimeError(
				fmt.Sprintf("core effect binding %d is zero or invalid", index),
				"a core binding set may contain only constructor-produced bindings",
				"NewCoreRuntimeBindings", "the contract cannot be built",
				"build every binding with a NativeEffectBinding-family constructor", nil,
			)
		}
		if _, duplicate := effs[binding.id()]; duplicate {
			return CoreRuntimeBindings{}, runtimeError(
				fmt.Sprintf("effect %q is bound twice", binding.id()),
				"one effect must have exactly one runtime classification",
				"NewCoreRuntimeBindings", "the contract's classification would be ambiguous",
				"bind each effect exactly once", nil,
			)
		}
		effs[binding.id()] = binding.entry
	}
	return CoreRuntimeBindings{operations: ops, effects: effs, constructed: true}, nil
}

func requiredCoreOperationIDs() map[string]struct{} {
	required := make(map[string]struct{})
	for _, kind := range ir.AllOperationKinds() {
		if id, ok := ir.CoreOperationID(kind); ok {
			required[id.String()] = struct{}{}
		}
	}
	return required
}

func (b CoreRuntimeBindings) IsValid() bool { return b.constructed }

// RuntimeContract is the opaque, version-bounded profile that lowers protocol
// semantic operations, effects, and capabilities to native runtime behavior for
// exactly one harness. The only way to produce a non-zero value is
// NewRuntimeContract, so a contract in hand has already passed identity,
// version, exhaustive-core, and contribution validation.
type RuntimeContract struct {
	id           ir.RuntimeContractID
	versions     VersionConstraint
	core         CoreRuntimeBindings
	capabilities map[ir.CapabilityID]capabilityContribution
	constructed  bool
}

// NewRuntimeContract validates and constructs a runtime contract. harness must
// match the harness encoded in id. Each capability contribution is required at
// most once; duplicate or conflicting contributions fail before construction.
func NewRuntimeContract(
	id ir.RuntimeContractID,
	harness ir.HarnessID,
	versions VersionConstraint,
	core CoreRuntimeBindings,
	capabilities ...RuntimeBindingContribution,
) (RuntimeContract, error) {
	if !id.IsValid() {
		return RuntimeContract{}, runtimeError(
			"runtime contract identity is zero or invalid",
			"a contract needs a constructor-validated version-bounded identity",
			"NewRuntimeContract", "the contract cannot be registered or selected",
			"construct the identity with ir.NewRuntimeContractID", nil,
		)
	}
	if !harness.IsValid() {
		return RuntimeContract{}, runtimeError(
			fmt.Sprintf("runtime contract harness %q is unknown", harness),
			"a contract binds exactly one enabled harness",
			"NewRuntimeContract", "the contract cannot be built",
			"use a HarnessID from ir.EnabledHarnessIDs", nil,
		)
	}
	if id.Harness() != harness {
		return RuntimeContract{}, runtimeError(
			fmt.Sprintf("runtime contract %q is bound to harness %q, not %q", id, id.Harness(), harness),
			"a contract's identity harness and declared harness must agree exactly",
			"NewRuntimeContract", "the contract could lower operations for the wrong harness",
			"construct the identity for the same harness passed to NewRuntimeContract", nil,
		)
	}
	if !versions.IsValid() {
		return RuntimeContract{}, runtimeError(
			fmt.Sprintf("runtime contract %q has an invalid version constraint", id),
			"a version-bounded contract requires a constructor-validated range",
			"NewRuntimeContract", "no host version could be matched",
			"construct the constraint with NewExactVersion or NewVersionConstraint", nil,
		)
	}
	if !core.IsValid() {
		return RuntimeContract{}, runtimeError(
			fmt.Sprintf("runtime contract %q has an invalid core binding set", id),
			"a contract requires an exhaustive core binding set",
			"NewRuntimeContract", "core operations could not be classified",
			"construct the core bindings with NewCoreRuntimeBindings", nil,
		)
	}
	contributions := make(map[ir.CapabilityID]capabilityContribution, len(capabilities))
	for index, contribution := range capabilities {
		concrete, ok := contribution.(capabilityContribution)
		if !ok {
			return RuntimeContract{}, runtimeError(
				fmt.Sprintf("capability contribution %d was not produced by BindCapability", index),
				"only BindCapability produces a valid contribution",
				"NewRuntimeContract", "the contract cannot trust an unknown contribution",
				"build every contribution with BindCapability", nil,
			)
		}
		if existing, duplicate := contributions[concrete.capabilityID]; duplicate {
			if existing.version == concrete.version {
				return RuntimeContract{}, runtimeError(
					fmt.Sprintf("capability %q is contributed twice", concrete.capabilityID),
					"a contract binds each requested capability exactly once",
					"NewRuntimeContract", "the runtime binding would be ambiguous",
					"contribute each capability once", nil,
				)
			}
			return RuntimeContract{}, runtimeError(
				fmt.Sprintf("capability %q has conflicting contributions for versions %q and %q", concrete.capabilityID, existing.version, concrete.version),
				"two contributions for one capability identity conflict; a contract cannot know which to honor",
				"NewRuntimeContract", "the runtime binding would be ambiguous",
				"contribute a single binding per capability identity", nil,
			)
		}
		contributions[concrete.capabilityID] = concrete
	}
	return RuntimeContract{
		id: id, versions: versions, core: core, capabilities: contributions, constructed: true,
	}, nil
}

// ID returns the contract's version-bounded identity.
func (c RuntimeContract) ID() ir.RuntimeContractID { return c.id }

// Harness returns the enabled harness this contract lowers for.
func (c RuntimeContract) Harness() ir.HarnessID { return c.id.Harness() }

// Versions returns the version constraint the contract accepts.
func (c RuntimeContract) Versions() VersionConstraint { return c.versions }

// Supports reports whether the contract accepts version. An unparsable or zero
// host version, an out-of-range version, or a prerelease the constraint does
// not explicitly include is unsupported.
func (c RuntimeContract) Supports(version HostVersion) bool {
	return c.constructed && c.versions.Allows(version)
}

// IsValid reports whether the contract was constructed by NewRuntimeContract.
func (c RuntimeContract) IsValid() bool { return c.constructed }

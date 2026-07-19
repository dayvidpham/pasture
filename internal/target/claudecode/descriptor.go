package claudecode

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Stable published component identities. These are the exact strings a
// downstream installer binds to an activation strategy; they never change
// spelling without a coordinated activation-side update.
const (
	SkillsComponentID = "claude-code/skills"
	AgentsComponentID = "claude-code/agents"
	HooksComponentID  = "claude-code/hooks"
)

// TargetDescriptor is the Claude Code target's published, opaque descriptor. It
// exposes exactly three components (skills, agents, hooks), the RuntimeContractID
// the target was compiled under, and its harness identity. It deliberately does
// not carry an ActivationContractID or any installation state: a downstream
// installer consumes this descriptor and binds each component to a closed
// activation strategy.
type TargetDescriptor struct {
	harness  ir.HarnessID
	contract ir.RuntimeContractID
	skills   Component
	agents   Component
	hooks    Component
	valid    bool
}

// NewTargetDescriptor validates and constructs a Claude Code target descriptor.
// It requires exactly one skills, one agents, and one hooks component, each with
// the matching kind, and a valid RuntimeContractID bound to the Claude Code
// harness. There is no mutable component map: the three slots are the only shape
// a descriptor can take, so an invalid missing/duplicate/wrong-kind combination
// cannot be constructed.
func NewTargetDescriptor(contract ir.RuntimeContractID, skills, agents, hooks Component) (TargetDescriptor, error) {
	if !contract.IsValid() {
		return TargetDescriptor{}, fmt.Errorf(
			"claudecode.NewTargetDescriptor: the runtime contract identity is zero or invalid — " +
				"a target must publish the exact version-bounded contract it was compiled under; " +
				"construct the identity with ir.NewRuntimeContractID or take it from runtime.ClaudeCode2_1_210().ID()",
		)
	}
	if contract.Harness() != ir.HarnessClaudeCode {
		return TargetDescriptor{}, fmt.Errorf(
			"claudecode.NewTargetDescriptor: runtime contract %q is bound to harness %q, not %q — "+
				"the Claude Code target may publish only a Claude Code runtime contract; "+
				"supply a claude-code contract such as runtime.ClaudeCode2_1_210().ID()",
			contract, contract.Harness(), ir.HarnessClaudeCode,
		)
	}
	if err := requireComponent("skills", skills, SkillsKind()); err != nil {
		return TargetDescriptor{}, err
	}
	if err := requireComponent("agents", agents, AgentsKind()); err != nil {
		return TargetDescriptor{}, err
	}
	if err := requireComponent("hooks", hooks, HooksKind()); err != nil {
		return TargetDescriptor{}, err
	}
	return TargetDescriptor{
		harness:  ir.HarnessClaudeCode,
		contract: contract,
		skills:   skills,
		agents:   agents,
		hooks:    hooks,
		valid:    true,
	}, nil
}

func requireComponent(slot string, component Component, want ComponentKind) error {
	if !component.IsValid() {
		return fmt.Errorf(
			"claudecode.NewTargetDescriptor: the %s component is zero or invalid — "+
				"a target descriptor requires exactly one valid %s component; "+
				"construct it with NewComponent",
			slot, slot,
		)
	}
	if component.Kind() != want {
		return fmt.Errorf(
			"claudecode.NewTargetDescriptor: the %s slot received a %q component, not %q — "+
				"each descriptor slot holds exactly its own kind so activation cannot install the wrong tree; "+
				"pass the %s component in the %s slot",
			slot, component.Kind(), want, slot, slot,
		)
	}
	return nil
}

// Descriptor builds the pinned Claude Code target descriptor from the embedded
// generated plugin trees and the runtime.ClaudeCode2_1_210 contract. It is the
// single production entry point: it constructs one immutable content-addressed
// bundle per component and publishes the exact contract identity the artifacts
// were reviewed against. Skills and agents are published default-off alongside
// hooks so no component activates without an explicit selection; hooks are
// additionally side-effecting and must be opted into.
func Descriptor() (TargetDescriptor, error) {
	contract := runtime.ClaudeCode2_1_210()

	skillsBundle, err := bundleForPluginRoot(skillsPluginRoot)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("claudecode.Descriptor: build skills bundle: %w", err)
	}
	agentsBundle, err := bundleForPluginRoot(agentsPluginRoot)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("claudecode.Descriptor: build agents bundle: %w", err)
	}
	hooksBundle, err := bundleForPluginRoot(hooksPluginRoot)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("claudecode.Descriptor: build hooks bundle: %w", err)
	}

	skills, err := newNamedComponent(SkillsKind(), SkillsComponentID, skillsBundle, false)
	if err != nil {
		return TargetDescriptor{}, err
	}
	agents, err := newNamedComponent(AgentsKind(), AgentsComponentID, agentsBundle, false)
	if err != nil {
		return TargetDescriptor{}, err
	}
	// Hooks are published default-off: they run commands on session lifecycle
	// events and enforce git discipline, so the user must opt in explicitly.
	hooks, err := newNamedComponent(HooksKind(), HooksComponentID, hooksBundle, false)
	if err != nil {
		return TargetDescriptor{}, err
	}

	return NewTargetDescriptor(contract.ID(), skills, agents, hooks)
}

func newNamedComponent(kind ComponentKind, rawID string, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	id, err := NewComponentID(rawID)
	if err != nil {
		return Component{}, fmt.Errorf("claudecode.Descriptor: %s component identity: %w", kind, err)
	}
	component, err := NewComponent(kind, id, bundle, defaultEnabled)
	if err != nil {
		return Component{}, fmt.Errorf("claudecode.Descriptor: build %s component: %w", kind, err)
	}
	return component, nil
}

// Harness returns the target's harness identity.
func (d TargetDescriptor) Harness() ir.HarnessID { return d.harness }

// RuntimeContractID returns the exact version-bounded contract the target's
// artifacts were compiled and reviewed against.
func (d TargetDescriptor) RuntimeContractID() ir.RuntimeContractID { return d.contract }

// Skills returns the published skills component.
func (d TargetDescriptor) Skills() Component { return d.skills }

// Agents returns the published agents component.
func (d TargetDescriptor) Agents() Component { return d.agents }

// Hooks returns the published, default-off hooks component.
func (d TargetDescriptor) Hooks() Component { return d.hooks }

// Components returns the three published components in canonical order:
// skills, agents, hooks.
func (d TargetDescriptor) Components() []Component {
	return []Component{d.skills, d.agents, d.hooks}
}

// Component returns the published component for kind, or an actionable error for
// a zero or unknown kind.
func (d TargetDescriptor) Component(kind ComponentKind) (Component, error) {
	switch kind {
	case SkillsKind():
		return d.skills, nil
	case AgentsKind():
		return d.agents, nil
	case HooksKind():
		return d.hooks, nil
	default:
		return Component{}, fmt.Errorf(
			"claudecode.TargetDescriptor.Component: component kind %q is zero or unknown — "+
				"the descriptor publishes only skills, agents, and hooks; "+
				"request one of those kinds",
			kind,
		)
	}
}

// IsValid reports whether the descriptor was produced by NewTargetDescriptor.
func (d TargetDescriptor) IsValid() bool { return d.valid }

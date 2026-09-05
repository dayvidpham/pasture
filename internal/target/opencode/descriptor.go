package opencode

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// TargetDescriptor is the immutable three-cell OpenCode target.
type TargetDescriptor struct {
	contract ir.RuntimeContractID
	skills   Component
	agents   Component
	hooks    Component
	valid    bool
}

func NewTargetDescriptor(contract ir.RuntimeContractID, skills, agents, hooks Component) (TargetDescriptor, error) {
	if !contract.IsValid() || contract.Harness() != ir.HarnessOpenCode {
		return TargetDescriptor{}, fmt.Errorf("opencode.NewTargetDescriptor: runtime contract %q is not a valid OpenCode contract — use runtime.OpenCode1_18_29().ID()", contract)
	}
	for _, input := range []struct {
		name      string
		want      artifact.Extension
		component Component
	}{{"skills", artifact.ExtensionSkills, skills}, {"agents", artifact.ExtensionAgents, agents}, {"hooks", artifact.ExtensionHooks, hooks}} {
		wantID, err := artifact.NewComponentID(artifact.HarnessOpenCode, input.want)
		if err != nil {
			return TargetDescriptor{}, err
		}
		if !input.component.IsValid() || input.component.ID() != wantID {
			return TargetDescriptor{}, fmt.Errorf("opencode.NewTargetDescriptor: %s slot does not contain canonical component %q — pass the independently generated %s component", input.name, wantID, input.name)
		}
	}
	return TargetDescriptor{contract: contract, skills: skills, agents: agents, hooks: hooks, valid: true}, nil
}

// Descriptor constructs the production target entirely from embedded generated
// bytes. Skills and agents default on; hooks remain explicit opt-in.
func Descriptor() (TargetDescriptor, error) {
	skills, err := componentFromAssets(artifact.ExtensionSkills, "assets/skills", true)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("opencode.Descriptor: build skills component: %w", err)
	}
	agents, err := componentFromAssets(artifact.ExtensionAgents, "assets/agents", true)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("opencode.Descriptor: build agents component: %w", err)
	}
	hooks, err := componentFromAssets(artifact.ExtensionHooks, "assets/hooks", false)
	if err != nil {
		return TargetDescriptor{}, fmt.Errorf("opencode.Descriptor: build hooks component: %w", err)
	}
	return NewTargetDescriptor(runtime.OpenCode1_18_29().ID(), skills, agents, hooks)
}

func (d TargetDescriptor) RuntimeContractID() ir.RuntimeContractID { return d.contract }
func (d TargetDescriptor) Skills() Component                       { return d.skills }
func (d TargetDescriptor) Agents() Component                       { return d.agents }
func (d TargetDescriptor) Hooks() Component                        { return d.hooks }
func (d TargetDescriptor) Components() []Component                 { return []Component{d.skills, d.agents, d.hooks} }
func (d TargetDescriptor) IsValid() bool                           { return d.valid }
func (d TargetDescriptor) Component(extension artifact.Extension) (Component, error) {
	switch extension {
	case artifact.ExtensionSkills:
		return d.skills, nil
	case artifact.ExtensionAgents:
		return d.agents, nil
	case artifact.ExtensionHooks:
		return d.hooks, nil
	default:
		return Component{}, fmt.Errorf("opencode.TargetDescriptor.Component: extension %q is unknown — request skills, agents, or hooks", extension)
	}
}

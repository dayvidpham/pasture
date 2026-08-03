// Package claudecode publishes the pinned Claude Code native-output target
// descriptor: a stable target/component identity, one immutable, content-
// addressed artifact.Bundle per installable component (skills, agents, hooks),
// and the RuntimeContractID the target was compiled under. The descriptor is the
// upstream input a downstream installer binds to an activation strategy; this
// package carries no ActivationContractID, performs no installation, and owns no
// native trust state.
package claudecode

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

// ComponentKind is the closed set of installable extension kinds a Claude Code
// target publishes. It is a value type with unexported state so a kind can only
// be one of the three constructor-produced variants, never an arbitrary string.
type ComponentKind struct{ name string }

const (
	skillsKindName = "skills"
	agentsKindName = "agents"
	hooksKindName  = "hooks"
)

// SkillsKind returns the skills component kind.
func SkillsKind() ComponentKind { return ComponentKind{name: skillsKindName} }

// AgentsKind returns the agents component kind.
func AgentsKind() ComponentKind { return ComponentKind{name: agentsKindName} }

// HooksKind returns the hooks component kind.
func HooksKind() ComponentKind { return ComponentKind{name: hooksKindName} }

// ParseComponentKind decodes one of the three closed component kinds.
func ParseComponentKind(value string) (ComponentKind, error) {
	switch value {
	case skillsKindName:
		return SkillsKind(), nil
	case agentsKindName:
		return AgentsKind(), nil
	case hooksKindName:
		return HooksKind(), nil
	default:
		return ComponentKind{}, fmt.Errorf(
			"claudecode.ParseComponentKind: component kind %q is not %q, %q, or %q — "+
				"the Claude Code target publishes exactly these three component kinds; "+
				"use one of them",
			value, skillsKindName, agentsKindName, hooksKindName,
		)
	}
}

func (k ComponentKind) String() string { return k.name }

// IsValid reports whether the kind is one of the three constructor variants.
func (k ComponentKind) IsValid() bool {
	switch k.name {
	case skillsKindName, agentsKindName, hooksKindName:
		return true
	default:
		return false
	}
}

// ComponentID is the opaque, validated stable identity of one published
// component (for example "claude-code/skills"). Downstream activation binds by
// this identity, so it must be a single exact non-empty spelling.
type ComponentID struct{ value string }

// NewComponentID validates and constructs a component identity.
func NewComponentID(value string) (ComponentID, error) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return ComponentID{}, fmt.Errorf(
			"claudecode.NewComponentID: component identity %q is empty or has surrounding whitespace — "+
				"activation binds components by exact identity, so two spellings must not compare unequal; "+
				"supply a non-empty identity without surrounding whitespace",
			value,
		)
	}
	return ComponentID{value: value}, nil
}

func (id ComponentID) String() string { return id.value }

// IsValid reports whether the identity was produced by NewComponentID.
func (id ComponentID) IsValid() bool {
	return id.value != "" && strings.TrimSpace(id.value) == id.value
}

// Component is one published Claude Code extension: its kind, stable identity,
// immutable content-addressed artifact bundle, and whether it is enabled by
// default. Hooks are published default-off because they carry side effects the
// user must opt into; the descriptor states this policy, while a downstream
// installer owns the actual activation transition.
type Component struct {
	kind           ComponentKind
	id             ComponentID
	bundle         artifact.Bundle
	defaultEnabled bool
	valid          bool
}

// NewComponent validates and constructs a published component from a kind, a
// stable identity, an immutable bundle, and a default-enabled policy.
func NewComponent(kind ComponentKind, id ComponentID, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	if !kind.IsValid() {
		return Component{}, fmt.Errorf(
			"claudecode.NewComponent: component kind is zero or invalid — " +
				"a published component must name one of the three closed kinds; " +
				"construct the kind with SkillsKind, AgentsKind, or HooksKind",
		)
	}
	if !id.IsValid() {
		return Component{}, fmt.Errorf(
			"claudecode.NewComponent(%s): component identity is zero or invalid — "+
				"activation cannot bind a component without a stable identity; "+
				"construct the identity with NewComponentID",
			kind,
		)
	}
	if bundle.Manifest().Len() == 0 {
		return Component{}, fmt.Errorf(
			"claudecode.NewComponent(%s, %s): the artifact bundle is empty or unconstructed — "+
				"a published component must carry at least its plugin manifest and one payload file "+
				"so the installed CLI can materialize it outside the source checkout; "+
				"construct the bundle with artifact.NewBundle over the generated component tree",
			kind, id,
		)
	}
	return Component{kind: kind, id: id, bundle: bundle, defaultEnabled: defaultEnabled, valid: true}, nil
}

// Kind returns the component's closed kind.
func (c Component) Kind() ComponentKind { return c.kind }

// ID returns the component's stable identity.
func (c Component) ID() ComponentID { return c.id }

// Bundle returns the component's immutable content-addressed artifact bundle.
func (c Component) Bundle() artifact.Bundle { return c.bundle }

// DefaultEnabled reports whether the component is enabled by default. Hooks are
// published false so their side effects require an explicit opt-in.
func (c Component) DefaultEnabled() bool { return c.defaultEnabled }

// IsValid reports whether the component was produced by NewComponent.
func (c Component) IsValid() bool { return c.valid }

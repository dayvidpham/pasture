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

	"github.com/dayvidpham/pasture/artifact"
)

// Component is one published Claude Code extension: its stable identity,
// immutable content-addressed artifact bundle, and whether it is enabled by
// default. Hooks are published default-off because they carry side effects the
// user must opt into; the descriptor states this policy, while a downstream
// installer owns the actual activation transition.
type Component struct {
	id             artifact.ComponentID
	bundle         artifact.Bundle
	defaultEnabled bool
	valid          bool
}

// NewComponent validates and constructs a published component from the sole
// canonical identity authority and an immutable bundle.
func NewComponent(id artifact.ComponentID, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	if !id.IsValid() {
		return Component{}, fmt.Errorf(
			"claudecode.NewComponent: component identity is zero or invalid — " +
				"activation cannot bind a component without a stable identity; " +
				"construct it with artifact.NewComponentID",
		)
	}
	if id.Harness() != artifact.HarnessClaudeCode {
		return Component{}, fmt.Errorf("claudecode.NewComponent(%s): component belongs to %s, not Claude Code — construct the ID with artifact.HarnessClaudeCode and its exact extension", id, id.Harness())
	}
	if bundle.Manifest().Len() == 0 {
		return Component{}, fmt.Errorf(
			"claudecode.NewComponent(%s): the artifact bundle is empty or unconstructed — "+
				"a published component must carry at least its plugin manifest and one payload file "+
				"so the installed CLI can materialize it outside the source checkout; "+
				"construct the bundle with artifact.NewBundle over the generated component tree",
			id,
		)
	}
	return Component{id: id, bundle: bundle, defaultEnabled: defaultEnabled, valid: true}, nil
}

// ID returns the component's stable identity.
func (c Component) ID() artifact.ComponentID { return c.id }

// Extension returns the canonical extension derived from ID.
func (c Component) Extension() artifact.Extension { return c.id.Extension() }

// Bundle returns the component's immutable content-addressed artifact bundle.
func (c Component) Bundle() artifact.Bundle { return c.bundle }

// DefaultEnabled reports whether the component is enabled by default. Hooks are
// published false so their side effects require an explicit opt-in.
func (c Component) DefaultEnabled() bool { return c.defaultEnabled }

// IsValid reports whether the component was produced by NewComponent.
func (c Component) IsValid() bool { return c.valid }

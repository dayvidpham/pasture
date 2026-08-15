// Package opencode publishes the immutable, independently installable OpenCode
// skills, agents, and lifecycle-hook artifacts.
package opencode

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
)

// Component is one immutable OpenCode installation cell.
type Component struct {
	id             artifact.ComponentID
	bundle         artifact.Bundle
	defaultEnabled bool
	valid          bool
}

// NewComponent validates one OpenCode component and its immutable payload.
func NewComponent(id artifact.ComponentID, bundle artifact.Bundle, defaultEnabled bool) (Component, error) {
	if !id.IsValid() || id.Harness() != artifact.HarnessOpenCode {
		return Component{}, fmt.Errorf("opencode.NewComponent: component %q is not a valid OpenCode identity — construct it with artifact.NewComponentID(artifact.HarnessOpenCode, extension)", id)
	}
	if bundle.Manifest().Len() == 0 {
		return Component{}, fmt.Errorf("opencode.NewComponent(%s): bundle is empty or invalid — regenerate the embedded OpenCode target assets and construct an artifact.Bundle with at least one file", id)
	}
	return Component{id: id, bundle: bundle, defaultEnabled: defaultEnabled, valid: true}, nil
}

func (c Component) ID() artifact.ComponentID      { return c.id }
func (c Component) Extension() artifact.Extension { return c.id.Extension() }
func (c Component) Bundle() artifact.Bundle       { return c.bundle }
func (c Component) DefaultEnabled() bool          { return c.defaultEnabled }
func (c Component) IsValid() bool                 { return c.valid }

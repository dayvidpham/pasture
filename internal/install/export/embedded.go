package export

import (
	"fmt"
	"io/fs"

	"github.com/dayvidpham/pasture/artifact"
	targetclaude "github.com/dayvidpham/pasture/internal/target/claudecode"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
	targetopencode "github.com/dayvidpham/pasture/internal/target/opencode"
)

// EmbeddedBundles is the production bundle source: the exact per-cell bundles
// the three embedded target descriptors declare — the same bundles the
// installer activates. Nothing here re-derives paths, modes, or identities.
func EmbeddedBundles() ([]CellBundle, error) {
	claude, err := targetclaude.Descriptor()
	if err != nil {
		return nil, descriptorFault(artifact.HarnessClaudeCode, err)
	}
	opencode, err := targetopencode.Descriptor()
	if err != nil {
		return nil, descriptorFault(artifact.HarnessOpenCode, err)
	}
	codex, err := targetcodex.Descriptor()
	if err != nil {
		return nil, descriptorFault(artifact.HarnessCodex, err)
	}
	cells := make([]CellBundle, 0, 9)
	for _, id := range artifact.ComponentIDs() {
		var bundle artifact.Bundle
		var componentErr error
		switch id.Harness() {
		case artifact.HarnessClaudeCode:
			component, err := claude.Component(id.Extension())
			bundle, componentErr = component.Bundle(), err
		case artifact.HarnessOpenCode:
			component, err := opencode.Component(id.Extension())
			bundle, componentErr = component.Bundle(), err
		case artifact.HarnessCodex:
			component, err := codex.Component(id.Extension())
			bundle, componentErr = component.Bundle(), err
		default:
			componentErr = fs.ErrInvalid
		}
		if componentErr != nil {
			return nil, archiveFault(
				"embedded target component lookup", "every canonical component resolves to a target bundle",
				fmt.Sprintf("component %s could not be read from its embedded target descriptor: %v", id, componentErr),
				"the export cannot cover the complete installation matrix",
				"rebuild Pasture so every embedded target descriptor declares skills, agents, and hooks", componentErr)
		}
		cells = append(cells, CellBundle{ID: id, Bundle: bundle})
	}
	return cells, nil
}

func descriptorFault(harness artifact.Harness, cause error) error {
	return archiveFault(
		"embedded target descriptor read", "each embedded target descriptor is constructible",
		fmt.Sprintf("the %s target descriptor could not be built: %v", harness, cause),
		"the export cannot be derived from the descriptors the installer trusts",
		"rebuild Pasture so the embedded target assets are complete and valid", cause)
}

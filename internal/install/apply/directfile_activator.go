package apply

import (
	"fmt"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/directfile"
	"github.com/dayvidpham/pasture/internal/install/inventory"
)

// DirectFileActivator activates direct-file cells by materializing their
// published artifact bundle under the strategy's destination root. It is the
// production activator for OpenCode skills/agents/hooks and any other
// direct-file component.
type DirectFileActivator struct {
	source Source
}

// NewDirectFileActivator returns a direct-file activator that records mutations
// under the given control source.
func NewDirectFileActivator(source Source) DirectFileActivator {
	return DirectFileActivator{source: source}
}

// StrategyKind reports the strategy this activator handles.
func (DirectFileActivator) StrategyKind() activation.StrategyKind {
	return activation.DirectFileKindValue()
}

func (a DirectFileActivator) strategy(act activation.ComponentActivation) (activation.DirectFile, error) {
	df, ok := act.Strategy().(activation.DirectFile)
	if !ok {
		return activation.DirectFile{}, cell.NewFault(
			"direct-file activation", "direct-file strategy",
			fmt.Sprintf("cell %s is bound to a %s strategy, not direct-file", act.Cell(), act.Strategy().Kind()),
			"internal/install/apply.DirectFileActivator", "dispatching a direct-file activation",
			"the wrong activator was dispatched for this cell",
			"bind this cell to a direct-file strategy or register the matching activator", nil,
		)
	}
	return df, nil
}

// Ensure materializes the cell's bundle. A pre-existing exact external match
// satisfies desired state and is recorded as external, never adopted.
func (a DirectFileActivator) Ensure(c cell.Cell, act activation.ComponentActivation, prior *inventory.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	var priorLeaves []inventory.Leaf
	if prior != nil {
		priorLeaves = prior.Leaves()
	}
	out, err := directfile.Ensure(df.DestinationRoot(), df.Bundle(), priorLeaves)
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	createdDirs, err := parseCreatedDirs(out.CreatedDirs)
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	record, err := inventory.NewRecord(inventory.RecordInput{
		Cell:        c,
		Source:      a.recordSource(),
		Strategy:    activation.DirectFileKindValue(),
		Managed:     out.Managed, // false for a pure external match
		ArtifactID:  df.Bundle().ID().String(),
		Leaves:      out.Leaves,
		CreatedDirs: createdDirs,
		Observation: inventory.Installed(),
		Trust:       inventory.TrustNotApplicable(),
		LastAction:  "ensure",
		LastOutcome: "completed",
	})
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	diagnostic := ""
	if out.External && !out.Managed {
		diagnostic = "an existing external copy already matches the bundle; it was left in place and not adopted"
	}
	return Outcome{Status: Completed(), Observation: inventory.Installed(), Record: &record, Diagnostic: diagnostic}, nil
}

// Remove unlinks the recorded managed leaves, reclaims the recorded created
// directories that are now empty, and records an absent tombstone. A created
// directory that a foreign file moved into is preserved and surfaced as an
// actionable diagnostic rather than force-removed.
func (a DirectFileActivator) Remove(c cell.Cell, act activation.ComponentActivation, prior inventory.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	removeOut, err := directfile.Remove(df.DestinationRoot(), prior.Leaves(), createdDirStrings(prior.CreatedDirs()))
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	// A tombstone carries no created-dir token: any directory still on disk now
	// holds a foreign entry Pasture does not own, and a re-install re-records the
	// tree it re-creates.
	record, err := inventory.NewRecord(inventory.RecordInput{
		Cell:        c,
		Source:      a.recordSource(),
		Strategy:    activation.DirectFileKindValue(),
		Managed:     true,
		ArtifactID:  prior.ArtifactID(),
		Observation: inventory.Absent(),
		Trust:       inventory.TrustNotApplicable(),
		LastAction:  "remove",
		LastOutcome: "completed",
	})
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	diagnostic := ""
	if len(removeOut.PreservedDirs) > 0 {
		diagnostic = fmt.Sprintf(
			"the managed leaves were removed, but these directories Pasture created were kept because a file it does not manage now lives inside them: %v; remove them by hand once you no longer need that content",
			removeOut.PreservedDirs,
		)
	}
	return Outcome{Status: Completed(), Observation: inventory.Absent(), Record: &record, Diagnostic: diagnostic}, nil
}

// parseCreatedDirs validates the relative created-directory paths reported by the
// directfile layer into the typed paths persisted on the record.
func parseCreatedDirs(dirs []string) ([]artifact.Path, error) {
	out := make([]artifact.Path, 0, len(dirs))
	for _, d := range dirs {
		p, err := artifact.NewPath(d)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// createdDirStrings flattens the typed created-directory paths back to the
// relative strings the directfile layer removes.
func createdDirStrings(dirs []artifact.Path) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, d.String())
	}
	return out
}

func (a DirectFileActivator) recordSource() inventory.Source {
	if a.source == HomeManagerSource() {
		return inventory.HomeManagerSource()
	}
	return inventory.InstallerSource()
}

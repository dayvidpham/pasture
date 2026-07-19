package apply

import (
	"fmt"

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
	record, err := inventory.NewRecord(inventory.RecordInput{
		Cell:        c,
		Source:      a.recordSource(),
		Strategy:    activation.DirectFileKindValue(),
		Managed:     out.Managed, // false for a pure external match
		ArtifactID:  df.Bundle().ID().String(),
		Leaves:      out.Leaves,
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

// Remove unlinks the recorded managed leaves and records an absent tombstone.
func (a DirectFileActivator) Remove(c cell.Cell, act activation.ComponentActivation, prior inventory.Record) (Outcome, error) {
	df, err := a.strategy(act)
	if err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
	if err := directfile.Remove(df.DestinationRoot(), prior.Leaves(), nil); err != nil {
		return Outcome{Observation: inventory.Unknown()}, err
	}
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
	return Outcome{Status: Completed(), Observation: inventory.Absent(), Record: &record}, nil
}

func (a DirectFileActivator) recordSource() inventory.Source {
	if a.source == HomeManagerSource() {
		return inventory.HomeManagerSource()
	}
	return inventory.InstallerSource()
}

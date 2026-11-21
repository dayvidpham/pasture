package apply

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// UnsupportedActivator handles canonical cells whose activation strategy is
// activation.Unsupported. Inspection reports the cell absent, removal is a
// no-op, and any attempt to ensure the cell fails actionably with the bound
// contract reason.
type UnsupportedActivator struct{}

// NewUnsupportedActivator returns the stateless activator for unsupported cells.
func NewUnsupportedActivator() UnsupportedActivator { return UnsupportedActivator{} }

func (UnsupportedActivator) StrategyKind() activation.StrategyKind {
	return activation.UnsupportedKindValue()
}

func (UnsupportedActivator) Inspect(_ context.Context, _ Source, _ registry.Key, act activation.ComponentActivation, _ *registry.Record) (Outcome, error) {
	reason := unsupportedReason(act)
	return Outcome{
		Status:      NoOp(),
		Observation: registry.ObservationAbsent,
		Diagnostic:  reason,
	}, nil
}

func (UnsupportedActivator) Ensure(_ context.Context, _ Source, _ registry.Key, act activation.ComponentActivation, _ *registry.Record) (Outcome, error) {
	reason := unsupportedReason(act)
	return Outcome{
		Status:      Failed(),
		Observation: registry.ObservationAbsent,
		Diagnostic:  reason,
	}, fmt.Errorf("%s is unsupported: %s", act.Cell(), reason)
}

func (UnsupportedActivator) Remove(_ context.Context, _ Source, _ registry.Key, act activation.ComponentActivation, _ registry.Record) (Outcome, error) {
	reason := unsupportedReason(act)
	return Outcome{
		Status:      Failed(),
		Observation: registry.ObservationAbsent,
		Diagnostic:  reason,
	}, fmt.Errorf("%s is unsupported and cannot be removed: %s", act.Cell(), reason)
}

func unsupportedReason(act activation.ComponentActivation) string {
	if u, ok := act.Strategy().(activation.Unsupported); ok && u.Reason() != "" {
		return u.Reason()
	}
	return fmt.Sprintf("component %s is not supported by this harness", act.Cell())
}

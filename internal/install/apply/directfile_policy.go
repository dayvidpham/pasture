package apply

import (
	"fmt"
	"reflect"

	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// DirectFileRequest is the immutable policy view of one direct-file operation.
// Policies receive validated typed identities rather than filesystem paths.
type DirectFileRequest struct {
	operation Operation
	source    Source
	key       registry.Key
	act       activation.ComponentActivation
	prior     *registry.Record
}

func newDirectFileRequest(operation Operation, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) DirectFileRequest {
	var copied *registry.Record
	if prior != nil {
		value := *prior
		copied = &value
	}
	return DirectFileRequest{operation: operation, source: source, key: key, act: act, prior: copied}
}

func (r DirectFileRequest) Operation() Operation                       { return r.operation }
func (r DirectFileRequest) Source() Source                             { return r.source }
func (r DirectFileRequest) Key() registry.Key                          { return r.key }
func (r DirectFileRequest) Cell() cell.Cell                            { return r.act.Cell() }
func (r DirectFileRequest) Activation() activation.ComponentActivation { return r.act }
func (r DirectFileRequest) Prior() (registry.Record, bool) {
	if r.prior == nil {
		return registry.Record{}, false
	}
	return *r.prior, true
}

type DirectFileValidator func(DirectFileRequest) error
type DirectFileDecorator func(DirectFileRequest, Outcome) (Outcome, error)

// DirectFilePolicy is one cell-bound validation and outcome-decoration policy.
// The constructor keeps the cell and functions coupled and rejects zero policy.
type DirectFilePolicy struct {
	cell     cell.Cell
	validate DirectFileValidator
	decorate DirectFileDecorator
}

func NewDirectFilePolicy(c cell.Cell, validate DirectFileValidator, decorate DirectFileDecorator) (DirectFilePolicy, error) {
	if !c.IsValid() || validate == nil || decorate == nil {
		return DirectFilePolicy{}, cell.NewFault("direct-file policy construction", "valid cell, validator, and decorator", fmt.Sprintf("policy cell %s or one of its functions is invalid", c), "internal/install/apply.NewDirectFilePolicy", "binding direct-file policy", "the filesystem boundary could run without its cell-specific checks", "provide one complete policy for a valid DirectFile cell", nil)
	}
	return DirectFilePolicy{cell: c, validate: validate, decorate: decorate}, nil
}

func (p DirectFilePolicy) Cell() cell.Cell { return p.cell }

// PassThroughDirectFile preserves the generic direct-file behavior byte for
// byte while still making the policy binding explicit.
func PassThroughDirectFile(c cell.Cell) (DirectFilePolicy, error) {
	return NewDirectFilePolicy(c, func(DirectFileRequest) error { return nil }, func(_ DirectFileRequest, out Outcome) (Outcome, error) { return out, nil })
}

func (p DirectFilePolicy) apply(request DirectFileRequest, generic Outcome) (Outcome, error) {
	decorated, err := p.decorate(request, generic)
	if err != nil {
		return generic, err
	}
	if err := validateDirectFileDecoration(generic, decorated); err != nil {
		return generic, err
	}
	return decorated, nil
}

func validateDirectFileDecoration(before, after Outcome) error {
	if before.Observation != after.Observation {
		return directFileDecorationFault("observation")
	}
	if (before.Record == nil) != (after.Record == nil) {
		return directFileDecorationFault("record presence")
	}
	if before.Record == nil {
		return nil
	}
	b, a := *before.Record, *after.Record
	if b.Key() != a.Key() || b.Source() != a.Source() || b.Strategy() != a.Strategy() || b.Managed() != a.Managed() || b.ArtifactID() != a.ArtifactID() || b.Version() != a.Version() || b.Selector() != a.Selector() || !reflect.DeepEqual(b.Leaves(), a.Leaves()) || !reflect.DeepEqual(b.CreatedDirs(), a.CreatedDirs()) || !reflect.DeepEqual(b.SharedConfig(), a.SharedConfig()) || b.Observation() != a.Observation() || b.LastOperation() != a.LastOperation() || b.LastOutcome() != a.LastOutcome() {
		return directFileDecorationFault("persisted ownership or operation fields")
	}
	if !a.IsValid() {
		return directFileDecorationFault("record validity")
	}
	return nil
}

func directFileDecorationFault(field string) error {
	return cell.NewFault("direct-file policy decoration", "status, outcome diagnostic, record trust, or record diagnostic changes only", fmt.Sprintf("the policy changed forbidden %s", field), "internal/install/apply.validateDirectFileDecoration", "decorating a live-inspected generic outcome", "untrusted ownership or operation facts would otherwise be persisted", "return the generic outcome with only the allowed presentation or trust fields changed", nil)
}

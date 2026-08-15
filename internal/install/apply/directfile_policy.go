package apply

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dayvidpham/pasture/artifact"
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
	cell      cell.Cell
	strategy  activation.StrategyKind
	artifact  artifact.BundleID
	dest      string
	prior     *registry.Record
}

func newDirectFileRequest(operation Operation, source Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) DirectFileRequest {
	var copied *registry.Record
	if prior != nil {
		value := *prior
		copied = &value
	}
	request := DirectFileRequest{operation: operation, source: source, key: key, cell: act.Cell(), prior: copied}
	if act.Strategy() != nil {
		request.strategy = act.Strategy().Kind()
		if direct, ok := act.Strategy().(activation.DirectFile); ok {
			request.artifact = direct.Bundle().ID()
			request.dest = direct.DestinationRoot()
		}
	}
	return request
}

func (r DirectFileRequest) Operation() Operation                  { return r.operation }
func (r DirectFileRequest) Source() Source                        { return r.source }
func (r DirectFileRequest) Key() registry.Key                     { return r.key }
func (r DirectFileRequest) Cell() cell.Cell                       { return r.cell }
func (r DirectFileRequest) StrategyKind() activation.StrategyKind { return r.strategy }
func (r DirectFileRequest) ArtifactID() artifact.BundleID         { return r.artifact }
func (r DirectFileRequest) DestinationRoot() string               { return r.dest }
func (r DirectFileRequest) Prior() (registry.Record, bool) {
	if r.prior == nil {
		return registry.Record{}, false
	}
	return *r.prior, true
}

type DirectFileValidator func(DirectFileRequest) error

type DirectFileDecorationMode struct {
	kind       uint8
	diagnostic string
}

const (
	directFileDecorationInvalid uint8 = iota
	directFileDecorationPassThrough
	directFileDecorationPendingNativeTrust
)

func PassThroughDecoration() DirectFileDecorationMode {
	return DirectFileDecorationMode{kind: directFileDecorationPassThrough}
}

func PendingNativeTrustDecoration(diagnostic string) (DirectFileDecorationMode, error) {
	if diagnostic == "" {
		return DirectFileDecorationMode{}, cell.NewFault("direct-file decoration construction", "nonempty native trust instructions", "pending-native-trust decoration has no diagnostic", "internal/install/apply.PendingNativeTrustDecoration", "constructing a hook policy", "users would not know how to review native trust", "provide exact host-native trust review instructions", nil)
	}
	return DirectFileDecorationMode{kind: directFileDecorationPendingNativeTrust, diagnostic: diagnostic}, nil
}

// DirectFilePolicy is one cell-bound validation and outcome-decoration policy.
// The constructor keeps the cell and functions coupled and rejects zero policy.
type DirectFilePolicy struct {
	cell     cell.Cell
	validate DirectFileValidator
	mode     DirectFileDecorationMode
}

func NewDirectFilePolicy(c cell.Cell, validate DirectFileValidator, mode DirectFileDecorationMode) (DirectFilePolicy, error) {
	if !c.IsValid() || validate == nil || mode.kind < directFileDecorationPassThrough || mode.kind > directFileDecorationPendingNativeTrust {
		return DirectFilePolicy{}, cell.NewFault("direct-file policy construction", "valid cell, validator, and closed decoration mode", fmt.Sprintf("policy cell %s, validator, or decoration mode is invalid", c), "internal/install/apply.NewDirectFilePolicy", "binding direct-file policy", "the filesystem boundary could run without its cell-specific checks", "provide one complete policy for a valid DirectFile cell", nil)
	}
	if mode.kind == directFileDecorationPendingNativeTrust && c.Extension() != cell.HooksAxis() {
		return DirectFilePolicy{}, cell.NewFault("direct-file policy construction", "pending native trust only for a hooks cell", fmt.Sprintf("policy cell %s is not hooks", c), "internal/install/apply.NewDirectFilePolicy", "binding pending native trust", "a non-hook artifact could claim host trust review", "use pass-through decoration or bind the exact hooks cell", nil)
	}
	return DirectFilePolicy{cell: c, validate: validate, mode: mode}, nil
}

func (p DirectFilePolicy) Cell() cell.Cell { return p.cell }

// PassThroughDirectFile preserves the generic direct-file behavior byte for
// byte while still making the policy binding explicit.
func PassThroughDirectFile(c cell.Cell) (DirectFilePolicy, error) {
	return NewDirectFilePolicy(c, func(DirectFileRequest) error { return nil }, PassThroughDecoration())
}

func PendingNativeTrustDirectFile(c cell.Cell, validate DirectFileValidator, diagnostic string) (DirectFilePolicy, error) {
	mode, err := PendingNativeTrustDecoration(diagnostic)
	if err != nil {
		return DirectFilePolicy{}, err
	}
	return NewDirectFilePolicy(c, validate, mode)
}

func (p DirectFilePolicy) apply(request DirectFileRequest, generic Outcome, actionErr error) (Outcome, error) {
	decorated := generic
	if actionErr != nil {
		decorated.Status = Failed()
	}
	if p.mode.kind == directFileDecorationPendingNativeTrust && decorated.Observation == registry.ObservationInstalled && request.operation != RemoveOp() {
		if decorated.Record == nil {
			return generic, directFileDecorationFault("pending trust without an installed record")
		}
		record, err := rewriteDirectFilePresentation(*decorated.Record, registry.TrustPending, p.mode.diagnostic)
		if err != nil {
			return generic, err
		}
		decorated.Record = &record
		decorated.Diagnostic = p.mode.diagnostic
		if actionErr == nil {
			decorated.Status = InstalledPendingTrust()
		}
	}
	if err := validateDirectFileDecoration(request, generic, decorated, actionErr); err != nil {
		return generic, err
	}
	return decorated, nil
}

func validateDirectFileDecoration(request DirectFileRequest, before, after Outcome, actionErr error) error {
	if !after.Status.IsValid() || after.Status == Unattempted() || after.Status == ManagedDeclaratively() || after.Status == NoOp() {
		return directFileDecorationFault("invalid or semantically unavailable status")
	}
	if !after.Observation.IsValid() {
		return directFileDecorationFault("invalid observation")
	}
	if actionErr != nil && after.Status != Failed() || actionErr == nil && after.Status == Failed() {
		return directFileDecorationFault("status contradicting the generic operation result")
	}
	if actionErr == nil && request.operation == Ensure() && after.Observation != registry.ObservationInstalled {
		return directFileDecorationFault("successful ensure without an installed observation")
	}
	if actionErr == nil && request.operation == RemoveOp() && after.Observation != registry.ObservationAbsent {
		return directFileDecorationFault("successful remove without an absent observation")
	}
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
	if a.Key() != request.key || a.Source() != registrySource(request.source) || a.Strategy() != request.strategy || a.Observation() != after.Observation {
		return directFileDecorationFault("record identity, source, strategy, or observation")
	}
	wantOperation := registry.OperationInspect
	if request.operation == Ensure() {
		wantOperation = registry.OperationEnsure
	} else if request.operation == RemoveOp() {
		wantOperation = registry.OperationRemove
	}
	wantOutcome := registry.OutcomeCompleted
	if actionErr != nil {
		wantOutcome = registry.OutcomeFailed
	}
	if a.LastOperation() != wantOperation || a.LastOutcome() != wantOutcome {
		return directFileDecorationFault("record operation or outcome")
	}
	if a.Trust() == registry.TrustTrusted {
		return directFileDecorationFault("trusted trust elevation")
	}
	if after.Observation == registry.ObservationInstalled && a.Trust() == registry.TrustPending {
		validPendingStatus := actionErr == nil && after.Status == InstalledPendingTrust() || actionErr != nil && after.Status == Failed()
		if request.cell.Extension() != cell.HooksAxis() || !validPendingStatus {
			return directFileDecorationFault("inconsistent installed pending-trust result")
		}
	} else if a.Trust() != registry.TrustNotApplicable {
		return directFileDecorationFault("trust outside the exact installed-hook pending case")
	}
	if after.Observation == registry.ObservationAbsent && after.Status == InstalledPendingTrust() {
		return directFileDecorationFault("pending trust for an absent result")
	}
	return nil
}

func rewriteDirectFilePresentation(record registry.Record, trust registry.Trust, diagnostic string) (registry.Record, error) {
	return registry.NewRecord(registry.RecordInput{Key: record.Key(), Source: record.Source(), Strategy: record.Strategy(), Managed: record.Managed(), ArtifactID: record.ArtifactID(), Version: record.Version(), Selector: record.Selector(), Leaves: record.Leaves(), CreatedDirs: record.CreatedDirs(), SharedConfig: record.SharedConfig(), Observation: record.Observation(), Trust: trust, LastOperation: record.LastOperation(), LastOutcome: record.LastOutcome(), Diagnostic: diagnostic})
}

func joinDirectFileErrors(genericErr, policyErr error) error {
	return errors.Join(genericErr, policyErr)
}

func directFileDecorationFault(field string) error {
	return cell.NewFault("direct-file policy decoration", "status, outcome diagnostic, record trust, or record diagnostic changes only", fmt.Sprintf("the policy changed forbidden %s", field), "internal/install/apply.validateDirectFileDecoration", "decorating a live-inspected generic outcome", "untrusted ownership or operation facts would otherwise be persisted", "return the generic outcome with only the allowed presentation or trust fields changed", nil)
}

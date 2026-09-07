// Package backend maps a legalized consultation into canonical receipt
// evidence and the typed response expected by the native host.
package backend

import (
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
)

// Aliases preserve the backend API while the waist owns the one validator
// and codec that durable readers and evaluators must both use.
type DecisionKind = waist.DecisionKind
type DecisionReason = waist.DecisionReason
type Decision = waist.Decision

const (
	DecisionKindUnset        = waist.DecisionKindUnset
	DecisionProceed          = waist.DecisionProceed
	DecisionDeny             = waist.DecisionDeny
	DecisionRequireHuman     = waist.DecisionRequireHuman
	ReasonUnset              = waist.ReasonUnset
	ReasonLegal              = waist.ReasonLegal
	ReasonUnboundSession     = waist.ReasonUnboundSession
	ReasonStopLoopGuard      = waist.ReasonStopLoopGuard
	ReasonUnknownActor       = waist.ReasonUnknownActor
	ReasonNoActiveAssignment = waist.ReasonNoActiveAssignment
	ReasonRoleForbidsAction  = waist.ReasonRoleForbidsAction
	ReasonPhaseForbidsAction = waist.ReasonPhaseForbidsAction
	ReasonUnenforcedDeny     = waist.ReasonUnenforcedDeny
	ReasonEvaluationFault    = waist.ReasonEvaluationFault
)

func NewDecision(kind DecisionKind, reason DecisionReason) (Decision, error) {
	return waist.NewDecision(kind, reason)
}

// HostResponse is one constructor-validated response to the native host.
type HostResponse struct {
	decision    Decision
	fault       bool
	constructed bool
}

func NewHostResponse(decision Decision) (HostResponse, error) {
	if !decision.IsValid() {
		return HostResponse{}, fmt.Errorf("backend.NewHostResponse: the evaluated decision is invalid; no host response was constructed; supply a verdict returned by NewDecision")
	}
	return HostResponse{decision: decision, constructed: true}, nil
}

// NewEvaluationFaultResponse constructs an unevaluated refusal, never a
// policy Decision. The native encoder must still prove channel support.
func NewEvaluationFaultResponse() HostResponse {
	return HostResponse{fault: true, constructed: true}
}

func (r HostResponse) Value() (Decision, bool) { return r.decision, r.IsValid() && !r.fault }
func (r HostResponse) IsEvaluationFault() bool { return r.IsValid() && r.fault }
func (r HostResponse) Reason() DecisionReason {
	if !r.IsValid() {
		return ReasonUnset
	}
	if r.fault {
		return ReasonEvaluationFault
	}
	return r.decision.Reason()
}
func (r HostResponse) Decision() DecisionKind {
	if !r.IsValid() {
		return DecisionKindUnset
	}
	if r.fault {
		return DecisionDeny
	}
	return r.decision.Kind()
}
func (r HostResponse) IsValid() bool {
	return r.constructed && ((r.fault && r.decision == (Decision{})) || (!r.fault && r.decision.IsValid()))
}

// MarshalJSON is the legacy host port, not the durable decision codec.
func (r HostResponse) MarshalJSON() ([]byte, error) {
	if !r.IsValid() {
		return nil, fmt.Errorf("marshal lifecycle host response: the HostResponse value was not constructed by the backend; no response was emitted; use NewHostResponse, NewEvaluationFaultResponse, or the response returned by BuildConsultation")
	}
	if r.Decision() == DecisionProceed {
		return []byte(`{"decision":"proceed"}`), nil
	}
	return json.Marshal(struct {
		Kind   string `json:"decision"`
		Reason string `json:"reason"`
	}{r.Decision().String(), r.Reason().String()})
}

func (HostResponse) ConsultationResponse() {}

// BuildConsultation retains the genuine current Proceed default. It does not
// claim to have read assignment authority or evaluated future gate policy.
func BuildConsultation(interpreted receipt.Record, legalized legalize.Legalized) (receipt.ConsultationRecord, HostResponse, error) {
	decision, err := NewDecision(DecisionProceed, ReasonLegal)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, err
	}
	return BuildDecidedConsultation(interpreted, legalized, decision)
}

func BuildDecidedConsultation(interpreted receipt.Record, legalized legalize.Legalized, decision Decision) (receipt.ConsultationRecord, HostResponse, error) {
	if !interpreted.IsValid() {
		return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: the interpreted record is invalid; no response was produced; pass the record returned by receipt.NewInterpreted for this gate")
	}
	if !legalized.IsValid() {
		return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: the legalized gate is invalid; no response was produced; pass the terminal returned by legalize.Event for this gate")
	}
	response, err := NewHostResponse(decision)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, err
	}
	record, err := receipt.NewConsultation(interpreted, legalized, response)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, err
	}
	return record, response, nil
}

var _ waist.ConsultationResponse = HostResponse{}

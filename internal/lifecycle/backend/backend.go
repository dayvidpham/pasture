// Package backend maps a legalized consultation into canonical receipt
// evidence and the typed response expected by the native host.
package backend

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
)

// DecisionKind is the closed set of policy verdicts. A kind alone is not a
// Decision: NewDecision validates its reason before a consumer can use it.
type DecisionKind uint8

const (
	DecisionKindUnset DecisionKind = iota
	DecisionProceed
	DecisionDeny
	DecisionRequireHuman
)

// IsValid reports whether the decision is supported by this backend.
func (d DecisionKind) IsValid() bool { return d >= DecisionProceed && d <= DecisionRequireHuman }

// String returns the canonical decision spelling.
func (d DecisionKind) String() string {
	switch d {
	case DecisionProceed:
		return "proceed"
	case DecisionDeny:
		return "deny"
	case DecisionRequireHuman:
		return "require-human"
	default:
		return ""
	}
}

// DecisionReason separates policy facts, record-only continuation reasons,
// and the failure to evaluate. EvaluationFault is never a policy verdict.
type DecisionReason uint8

const (
	ReasonUnset DecisionReason = iota
	ReasonLegal
	ReasonUnboundSession
	ReasonStopLoopGuard
	ReasonUnknownActor
	ReasonNoActiveAssignment
	ReasonRoleForbidsAction
	ReasonPhaseForbidsAction
	ReasonUnenforcedDeny
	ReasonEvaluationFault
)

func (r DecisionReason) IsValid() bool { return r >= ReasonLegal && r <= ReasonEvaluationFault }
func (r DecisionReason) String() string {
	switch r {
	case ReasonLegal:
		return "legal"
	case ReasonUnboundSession:
		return "unbound-session"
	case ReasonStopLoopGuard:
		return "stop-loop-guard"
	case ReasonUnknownActor:
		return "unknown-actor"
	case ReasonNoActiveAssignment:
		return "no-active-assignment"
	case ReasonRoleForbidsAction:
		return "role-forbids-action"
	case ReasonPhaseForbidsAction:
		return "phase-forbids-action"
	case ReasonUnenforcedDeny:
		return "unenforced-deny"
	case ReasonEvaluationFault:
		return "evaluation-fault"
	default:
		return ""
	}
}

// Message is the host-facing explanation; String is the stable record token.
func (r DecisionReason) Message() string {
	switch r {
	case ReasonLegal:
		return "Pasture permits this action."
	case ReasonUnboundSession:
		return "This session has no actor claim; the host continues without an assignment decision."
	case ReasonStopLoopGuard:
		return "The stop-loop guard lets the host continue without another refusal."
	case ReasonUnknownActor:
		return "Pasture denied this action because the session actor is unknown; bind the session to a registered actor."
	case ReasonNoActiveAssignment:
		return "Pasture denied this action because the actor has no active assignment; start an authorized assignment before retrying."
	case ReasonRoleForbidsAction:
		return "Pasture denied this action because the assignment role does not permit it; use an assignment with a permitted role."
	case ReasonPhaseForbidsAction:
		return "Pasture denied this action because the assignment phase does not permit it; complete the required phase transition before retrying."
	case ReasonUnenforcedDeny:
		return "The host continues because this event has no evidenced refusal channel; the policy denial is record-only."
	case ReasonEvaluationFault:
		return "Pasture could not evaluate this event; this is not a policy denial. Read the lifecycle fault diagnostic and repair the failed evaluation before retrying."
	default:
		return ""
	}
}

// Decision is an immutable evaluated verdict. Its zero value is unusable.
type Decision struct {
	kind   DecisionKind
	reason DecisionReason
}

func NewDecision(kind DecisionKind, reason DecisionReason) (Decision, error) {
	d := Decision{kind: kind, reason: reason}
	if !d.IsValid() {
		return Decision{}, fmt.Errorf("backend.NewDecision: kind %d and reason %d do not describe an evaluated policy verdict; no decision was constructed; pair Proceed with a continuation reason or Deny/RequireHuman with a policy refusal reason, and use NewEvaluationFaultResponse for a failed evaluation", kind, reason)
	}
	return d, nil
}
func (d Decision) IsValid() bool {
	switch d.kind {
	case DecisionProceed:
		return d.reason == ReasonLegal || d.reason == ReasonUnboundSession || d.reason == ReasonStopLoopGuard || d.reason == ReasonUnenforcedDeny
	case DecisionDeny, DecisionRequireHuman:
		return d.reason == ReasonUnknownActor || d.reason == ReasonNoActiveAssignment || d.reason == ReasonRoleForbidsAction || d.reason == ReasonPhaseForbidsAction
	default:
		return false
	}
}
func (d Decision) Kind() DecisionKind     { return d.kind }
func (d Decision) Reason() DecisionReason { return d.reason }
func (d Decision) MarshalJSON() ([]byte, error) {
	if !d.IsValid() {
		return nil, fmt.Errorf("backend.Decision.MarshalJSON: no valid evaluated decision was supplied; nothing was serialized; construct the verdict with NewDecision")
	}
	return json.Marshal(struct {
		Kind   string `json:"decision"`
		Reason string `json:"reason"`
	}{d.kind.String(), d.reason.String()})
}

// UnmarshalJSON accepts only the canonical, closed decision record produced
// by MarshalJSON. Unknown/duplicate members, nulls, omitted fields and trailing
// data cannot manufacture a verdict. A failed decode clears the receiver.
func (d *Decision) UnmarshalJSON(raw []byte) error {
	if d == nil {
		return fmt.Errorf("backend.Decision.UnmarshalJSON: destination is nil; no decision can be decoded; supply a writable Decision value")
	}
	*d = Decision{}
	var wire struct {
		Kind   string `json:"decision"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("backend.Decision.UnmarshalJSON: cannot read the decision record; no decision was retained; supply canonical decision/reason JSON: %w", err)
	}
	var kind DecisionKind
	for candidate := DecisionProceed; candidate <= DecisionRequireHuman; candidate++ {
		if candidate.String() == wire.Kind {
			kind = candidate
		}
	}
	var reason DecisionReason
	for candidate := ReasonLegal; candidate <= ReasonEvaluationFault; candidate++ {
		if candidate.String() == wire.Reason {
			reason = candidate
		}
	}
	value, err := NewDecision(kind, reason)
	if err != nil {
		return fmt.Errorf("backend.Decision.UnmarshalJSON: %w", err)
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return fmt.Errorf("backend.Decision.UnmarshalJSON: the record is not canonical closed decision/reason JSON; no decision was retained; remove extra or duplicate members and serialize the constructor-built Decision")
	}
	*d = value
	return nil
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

// NewEvaluationFaultResponse constructs an unevaluated refusal for a caller
// that has selected fail-closed. It is not a policy Decision. The native
// encoder must still prove that the mapped event supports this channel.
func NewEvaluationFaultResponse() HostResponse { return HostResponse{fault: true, constructed: true} }

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

// Decision returns the response decision, or the invalid zero decision when
// the response was not constructed by this package.
func (r HostResponse) Decision() DecisionKind {
	if !r.IsValid() {
		return 0
	}
	if r.fault {
		return DecisionDeny
	}
	return r.decision.Kind()
}

// IsValid reports whether the response was constructed with a valid decision.
func (r HostResponse) IsValid() bool {
	return r.constructed && ((r.fault && r.decision == (Decision{})) || (!r.fault && r.decision.IsValid()))
}

// MarshalJSON returns the canonical compact response port. Proceed keeps its
// legacy bytes, including in consultation.v1. Consumers that need a persisted
// policy reason must serialize Decision, not infer it from these host bytes.
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

// ConsultationResponse implements waist.ConsultationResponse.
func (HostResponse) ConsultationResponse() {}

// BuildConsultation is the sole production entry point for backend mapping.
func BuildConsultation(interpreted receipt.Record, legalized legalize.Legalized) (receipt.ConsultationRecord, HostResponse, error) {
	if !interpreted.IsValid() {
		return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: the interpreted record is invalid because it was not constructed by receipt.NewInterpreted; no record or host response was produced; pass the interpreted record for the same verified gate event")
	}
	if !legalized.IsValid() {
		return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: the legalized value is invalid because it was not returned by legalize.Event for a gate consultation; no record or host response was produced; pass the Legalized terminal from the same gate event")
	}
	decision, err := NewDecision(DecisionProceed, ReasonLegal)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, err
	}
	response, err := NewHostResponse(decision)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, err
	}
	record, err := receipt.NewConsultation(interpreted, legalized, response)
	if err != nil {
		return receipt.ConsultationRecord{}, HostResponse{}, fmt.Errorf("build lifecycle consultation: receipt.NewConsultation rejected the interpreted gate, legalized value, or Proceed response; no record or host response was returned; ensure the interpreted record describes the same gate and preserve constructor-built values: %w", err)
	}
	return record, response, nil
}

var _ waist.ConsultationResponse = HostResponse{}

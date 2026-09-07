package waist

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecisionKind is shared by the evaluator, durable consultation and backend.
type DecisionKind uint8

const (
	DecisionKindUnset DecisionKind = iota
	DecisionProceed
	DecisionDeny
	DecisionRequireHuman
)

func (d DecisionKind) IsValid() bool { return d >= DecisionProceed && d <= DecisionRequireHuman }

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
// and failure to evaluate. EvaluationFault is never a policy verdict.
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
// This is the single constructor/codec authority, including for receipt reads.
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

// UnmarshalJSON accepts only the canonical closed record. A failed direct
// decode clears the receiver instead of leaving an earlier decision usable.
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

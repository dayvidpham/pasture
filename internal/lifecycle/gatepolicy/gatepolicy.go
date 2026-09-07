// Package gatepolicy evaluates assignment policy without reading or writing a
// store. Callers must obtain authority from a complete gateauthority.Snapshot;
// a failed snapshot belongs on the fault path, not in a policy input.
package gatepolicy

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Input contains policy facts, not index completeness or host payload bytes.
// Event identifies the occurrence; the caller supplies its classified action
// and runtime properties. Decide does not reclassify or mutate those facts.
type Input struct {
	Event      model.ContractEventKind
	Action     gateauthority.ActionClass
	Semantic   runtime.EventSemantic
	StopLoop   runtime.StopLoopPolicy
	Capability runtime.ResponseCapability
	Claim      gateauthority.SessionClaim
	Authority  gateauthority.ActorAuthority
}

// RefusalKind identifies a failure to evaluate, never a policy denial.
type RefusalKind uint8

const (
	RefusalUnset RefusalKind = iota
	RefusalAuthorityTruncated
	RefusalPhaseUnknown
	RefusalPolicyUnlisted
)

func (k RefusalKind) IsValid() bool {
	return k >= RefusalAuthorityTruncated && k <= RefusalPolicyUnlisted
}

// Refusal is an immutable fault value. Its zero value is invalid.
type Refusal struct {
	kind RefusalKind
}

func NewRefusal(kind RefusalKind) (Refusal, error) {
	if !kind.IsValid() {
		return Refusal{}, fmt.Errorf("gatepolicy.NewRefusal: unknown refusal kind %d during construction; no fault value was retained; supply a supported RefusalKind", kind)
	}
	return Refusal{kind: kind}, nil
}

func (r Refusal) Kind() RefusalKind { return r.kind }
func (r Refusal) IsValid() bool     { return r.kind.IsValid() }

func (r Refusal) Error() string {
	var cause, fix string
	switch r.kind {
	case RefusalAuthorityTruncated:
		cause = "the authority episode list was truncated"
		fix = "reduce the actor's active assignments before evaluating again"
	case RefusalPhaseUnknown:
		cause = "an assignment episode has no known phase"
		fix = "repair the task phase mapping before evaluating again"
	case RefusalPolicyUnlisted:
		cause = "a role or phase action cell is not listed in the policy tables"
		fix = "update the policy tables and enum mappings together before evaluating again"
	default:
		cause = "the refusal was not constructed with a supported kind"
		fix = "construct the fault with gatepolicy.NewRefusal"
	}
	return "gatepolicy.Decide: evaluation could not finish because " + cause +
		"; no policy decision was made; route this fault through the host fault policy and " + fix
}

// Result holds exactly one valid decision or refusal, or neither when invalid.
// Constructors reject invalid operands by returning the unusable zero value.
type Result struct {
	decision backend.Decision
	refusal  Refusal
}

func Decided(d backend.Decision) Result {
	if !d.IsValid() {
		return Result{}
	}
	return Result{decision: d}
}

func Refused(r Refusal) Result {
	if !r.IsValid() {
		return Result{}
	}
	return Result{refusal: r}
}

func (r Result) Decision() (backend.Decision, bool) {
	if !r.decision.IsValid() || r.refusal.IsValid() {
		return backend.Decision{}, false
	}
	return r.decision, true
}

// Refusal returns a copy. Replacing the pointed-to value cannot alter Result.
func (r Result) Refusal() (*Refusal, bool) {
	if !r.refusal.IsValid() || r.decision.IsValid() {
		return nil, false
	}
	copy := r.refusal
	return &copy, true
}

// Decide evaluates the ordered policy rules. An error means the input runtime
// vocabulary is invalid; a refused Result means authority or tables could not
// support evaluation. Neither is a policy Deny. No rule requests human input.
func Decide(in Input) (Result, error) {
	// Validate the runtime vocabulary before applying policy. Unset capability
	// is not record-only capability and must never grant an enforcement channel.
	if !in.Semantic.IsValid() {
		return Result{}, invalidRuntime("semantic", uint8(in.Semantic))
	}
	if !in.StopLoop.IsValid() {
		return Result{}, invalidRuntime("stop-loop", uint8(in.StopLoop))
	}
	if !in.Capability.IsValid() {
		return Result{}, invalidRuntime("capability", uint8(in.Capability))
	}

	// Rules 1–3 do not consult assignment policy.
	if in.Semantic == runtime.SemanticObservation {
		return decision(backend.DecisionProceed, backend.ReasonLegal)
	}
	if in.StopLoop == runtime.StopLoopConsultWhenInactive {
		return decision(backend.DecisionProceed, backend.ReasonStopLoopGuard)
	}
	if !in.Claim.Bound {
		return decision(backend.DecisionProceed, backend.ReasonUnboundSession)
	}

	// Rules 4–7 choose a verdict or fault in priority order. A denial still
	// passes through the capability downgrade; faults never do.
	if !in.Claim.Known {
		return denied(in.Capability, backend.ReasonUnknownActor)
	}
	if in.Authority.Truncated {
		return refusal(RefusalAuthorityTruncated)
	}
	if len(in.Authority.Episodes) == 0 {
		return denied(in.Capability, backend.ReasonNoActiveAssignment)
	}
	for _, ep := range in.Authority.Episodes {
		if ep.Phase == gateauthority.TaskPhaseUnset {
			return refusal(RefusalPhaseUnknown)
		}
	}

	// Rule 8: never return early on Allow. An unlisted cell in ANY episode
	// makes the evaluation incomplete, even if another episode permits both.
	roleAllowed := false
	bothAllowed := false
	for _, ep := range in.Authority.Episodes {
		role := gateauthority.RoleVerdict(ep.Role, in.Action)
		phase := gateauthority.PhaseVerdict(ep.Phase, in.Action)
		if role == gateauthority.VerdictRefuse || phase == gateauthority.VerdictRefuse {
			return refusal(RefusalPolicyUnlisted)
		}
		if role == gateauthority.VerdictAllow {
			roleAllowed = true
			if phase == gateauthority.VerdictAllow {
				bothAllowed = true
			}
		}
	}
	if bothAllowed {
		return decision(backend.DecisionProceed, backend.ReasonLegal)
	}
	// A role-eligible episode exists, but none of those episodes permits the
	// phase. Otherwise no role permits this action. Order cannot change why.
	if roleAllowed {
		return denied(in.Capability, backend.ReasonPhaseForbidsAction)
	}
	return denied(in.Capability, backend.ReasonRoleForbidsAction)
}

func invalidRuntime(field string, value uint8) error {
	return fmt.Errorf("gatepolicy.Decide: %s value %d is unset or unsupported during input validation; no policy decision was made; supply the classified event's valid runtime properties and route this error through the host fault policy", field, value)
}

func decision(kind backend.DecisionKind, reason backend.DecisionReason) (Result, error) {
	d, err := backend.NewDecision(kind, reason)
	if err != nil {
		return Result{}, err
	}
	return Decided(d), nil
}

func refusal(kind RefusalKind) (Result, error) {
	r, err := NewRefusal(kind)
	if err != nil {
		return Result{}, err
	}
	return Refused(r), nil
}

func denied(capability runtime.ResponseCapability, reason backend.DecisionReason) (Result, error) {
	// Rules 9–10: record-only denial proceeds without changing host bytes.
	if capability == runtime.CapabilityNone {
		return decision(backend.DecisionProceed, backend.ReasonUnenforcedDeny)
	}
	return decision(backend.DecisionDeny, reason)
}

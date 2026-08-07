package gate

import "fmt"

// RefusalReason is the closed set of reasons a durable lifecycle write is
// refused by the gate.
type RefusalReason uint8

const (
	// RefusalUnknownClass: the intent or expected class is not one of the
	// enumerated WriteClass values (typically the zero value).
	RefusalUnknownClass RefusalReason = iota + 1
	// RefusalInvalidIntent: an intent's per-class coordinates are not
	// well-formed, or a commit surface received a zero/unissued warrant.
	RefusalInvalidIntent
	// RefusalClassMismatch: a warrant's class does not match the write class
	// at the commit surface that received it.
	RefusalClassMismatch
)

// String returns the stable spelling of the refusal reason.
func (r RefusalReason) String() string {
	switch r {
	case RefusalUnknownClass:
		return "unknown-class"
	case RefusalInvalidIntent:
		return "invalid-intent"
	case RefusalClassMismatch:
		return "class-mismatch"
	default:
		return "unknown-reason"
	}
}

// Refusal is the distinct typed disposition of a refused Pasture-side durable
// lifecycle write. It is the gate's analogue of the activation gate's withheld
// diagnostic: a first-class, actionable value rather than a bare error string.
//
// Refusal implements error, so it flows on the existing (native []byte, err
// error) lifecycle path where the CLI prints it to stderr, emits no stdout, and
// exits 0. It is deliberately NOT a *pasterrors.StructuredError: it never
// carries a Category and so can never be mapped to exit code 2 (which
// pasterrors.ExitCode reserves for CategoryConnection). Callers discriminate a
// refusal with errors.As(&*gate.Refusal), never by string or exit-code matching.
type Refusal struct {
	class  WriteClass
	reason RefusalReason
	what   string
	why    string
	where  string
	impact string
	fix    string
}

// Class returns the write class the refusal concerns.
func (r *Refusal) Class() WriteClass { return r.class }

// Reason returns the closed-set reason the write was refused.
func (r *Refusal) Reason() RefusalReason { return r.reason }

// Error implements error with a single actionable line describing what went
// wrong, why, where, its impact, and how to fix it.
func (r *Refusal) Error() string {
	return fmt.Sprintf(
		"lifecycle write refused (%s, %s): %s. Why: %s. Where: %s. Impact: %s. Fix: %s",
		r.class, r.reason, r.what, r.why, r.where, r.impact, r.fix,
	)
}

// newRefusal builds a fully-populated actionable Refusal.
func newRefusal(class WriteClass, reason RefusalReason, what, why, where, impact, fix string) *Refusal {
	return &Refusal{class: class, reason: reason, what: what, why: why, where: where, impact: impact, fix: fix}
}

// refuseInvalidIntent builds a RefusalInvalidIntent for a malformed intent
// constructor, filling the shared where/impact text so each constructor only
// states its specific what/why/fix.
func refuseInvalidIntent(class WriteClass, what, why, fix string) *Refusal {
	return newRefusal(class, RefusalInvalidIntent, what, why,
		"constructing a lifecycle write intent (internal/lifecycle/gate)",
		"no intent was constructed and no warrant can be issued",
		fix)
}

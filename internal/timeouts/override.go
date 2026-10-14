package timeouts

import (
	"fmt"
	"time"
)

// Override collects an operator's requested substitutions for the two caller
// windows and the hook-invocation deadline that sits above them. A zero field
// means "keep the base profile's value", so an operator can tune one tier
// without restating the others.
//
// THE ORDERING INVARIANT IS NOT OPTIONAL AND IS NOT CLAMPED. These tiers are not
// three independent numbers: a hook invocation CONTAINS the receipt append it
// performs, so its deadline must sit strictly above both caller windows, and
// the workflow-result wait must sit strictly above all of them. An override is
// therefore handed to New, which refuses an inverted combination with the same
// error it has always returned. Silently clamping a rejected value would leave
// the operator believing a budget the code does not honour.
type Override struct {
	// HookInvocation is the whole-lifecycle-hook deadline the HOST waits on.
	// Zero keeps the base profile's value.
	HookInvocation time.Duration
	// Ingress is one lifecycle receipt append, including its lock retries.
	// Zero keeps the base profile's value.
	Ingress time.Duration
	// StartSlice is how long a slice sub-workflow waits for its start_slice
	// signal. Zero keeps the base profile's value.
	StartSlice time.Duration
}

// IsZero reports whether the override asks for no change at all. It holds for
// the zero Override, which is what an unset environment produces.
func (o Override) IsZero() bool {
	return o.HookInvocation == 0 && o.Ingress == 0 && o.StartSlice == 0
}

// WithOverride returns base with every non-zero field of o substituted and
// re-runs New over the result, so the whole hierarchy is re-validated and an
// inverted combination is refused with New's error rather than clamped.
//
// With the zero Override it returns base unchanged, which is why the default
// production behaviour is untouched when no knob is set: the caller passes
// ProductionProfile and receives the same ordered profile back.
func (base Profile) WithOverride(o Override) (Profile, error) {
	if o.IsZero() {
		return base, nil
	}
	if o.HookInvocation < 0 || o.Ingress < 0 || o.StartSlice < 0 {
		return Profile{}, fmt.Errorf(
			"timeout override durations must be positive, with zero meaning \"keep the base value\": "+
				"hookInvocation=%s ingress=%s startSlice=%s", o.HookInvocation, o.Ingress, o.StartSlice)
	}
	ingress := base.ingress
	if o.Ingress > 0 {
		ingress = o.Ingress
	}
	startSlice := base.startSlice
	if o.StartSlice > 0 {
		startSlice = o.StartSlice
	}
	hookInvocation := base.hookInvocation
	if o.HookInvocation > 0 {
		hookInvocation = o.HookInvocation
	}
	return New(base.kind, base.sqliteBusy, ingress, startSlice, hookInvocation, base.workflowResult)
}

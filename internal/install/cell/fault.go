package cell

import "fmt"

// Fault is the shared six-part actionable error for the Pasture installer
// domain. Every rejected value or boundary reports what went wrong (Rule +
// Reason), where it failed (Where), when it failed (When), what it means for
// the caller (Impact), and how to fix it (Fix), so callers never see an opaque
// "invalid input" message.
type Fault struct {
	Op     string
	Rule   string
	Reason string
	Where  string
	When   string
	Impact string
	Fix    string
	Cause  error
}

// Error renders the six-part diagnostic on a single line.
func (f *Fault) Error() string {
	return fmt.Sprintf(
		"install: %s failed: rule %q did not hold because %s (where: %s; when: %s); impact: %s; fix: %s",
		f.Op, f.Rule, f.Reason, f.Where, f.When, f.Impact, f.Fix,
	)
}

// Unwrap exposes the lower-level cause, if any.
func (f *Fault) Unwrap() error { return f.Cause }

// NewFault builds a six-part installer fault. It is exported so sibling
// installer packages raise identically shaped diagnostics.
func NewFault(op, rule, reason, where, when, impact, fix string, cause error) error {
	return newFault(op, rule, reason, where, when, impact, fix, cause)
}

func newFault(op, rule, reason, where, when, impact, fix string, cause error) error {
	return &Fault{
		Op:     op,
		Rule:   rule,
		Reason: reason,
		Where:  where,
		When:   when,
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

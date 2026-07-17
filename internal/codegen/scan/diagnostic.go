package scan

import "fmt"

// Diagnostic is this package's actionable error contract. Every field is
// required so a caller can act on a failure without reading scanner source:
// what went wrong, why, where it failed, during which phase, what it means
// for the caller, and how to fix it. This mirrors internal/codegen/ir's
// Diagnostic shape (see ir/diagnostic.go) without importing it: scan is an
// independent consumer-facing package and must not fork or re-export #38's
// exported Diagnostic type, but the same actionable-error contract applies
// to every error this package returns.
type Diagnostic struct {
	What   string
	Why    string
	Where  string
	Phase  string
	Impact string
	Fix    string
	Cause  error
}

func (d *Diagnostic) Error() string {
	message := fmt.Sprintf(
		"what: %s; why: %s; where: %s; phase: %s; impact: %s; fix: %s",
		d.What, d.Why, d.Where, d.Phase, d.Impact, d.Fix,
	)
	if d.Cause != nil {
		return message + ": " + d.Cause.Error()
	}
	return message
}

func (d *Diagnostic) Unwrap() error { return d.Cause }

func diagnostic(what, why, where, phase, impact, fix string, cause error) error {
	return &Diagnostic{What: what, Why: why, Where: where, Phase: phase, Impact: impact, Fix: fix, Cause: cause}
}

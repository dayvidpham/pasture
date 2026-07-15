// Package ir defines Pasture's portable, harness-neutral document and
// orchestration intermediate representation.
package ir

import "fmt"

// Diagnostic is the actionable error contract returned by IR construction and
// compilation. Every field is required so callers can report a useful failure
// without reverse-engineering compiler internals.
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
	return &Diagnostic{
		What: what, Why: why, Where: where, Phase: phase, Impact: impact, Fix: fix, Cause: cause,
	}
}

// Package promotion owns Aura's release/distribution surface for the moving
// pasture-stable channel: it derives one aggregate marketplace catalog from an
// exact Pasture snapshot and performs the gated, guarded promotion of the
// pasture-stable ref.
//
// The promotion is release metadata, not installer state: it advances the
// pasture-stable ref only after the static mandatory gates pass at the exact
// Pasture and Aura commits and candidate cleanup succeeds. It then performs one
// guarded ref update against the verified URL and never overwrites a racing
// publisher. The guarded-update mechanics are reused from
// internal/effects (GuardedPushExactCommit + GitRepositoryPusher); this package
// composes them into the promotion workflow and never re-implements the
// verify/push/re-read/prove algorithm.
package promotion

import "strings"

// Error is the six-part actionable error every promotion failure raises. It
// names what went wrong, why, where, when, the caller impact, and the fix, so a
// release operator can resolve a failed promotion without reading the source.
type Error struct {
	What   string // what went wrong
	Why    string // why it happened
	Where  string // where it failed (package/function)
	When   string // when it failed (step/operation)
	Impact string // what it means for the caller
	Fix    string // how to fix it
	Cause  error  // wrapped underlying error, if any
}

// Error renders the six parts as a single actionable line.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.What)
	if e.Why != "" {
		b.WriteString(" — ")
		b.WriteString(e.Why)
	}
	if e.Where != "" {
		b.WriteString(" — at ")
		b.WriteString(e.Where)
	}
	if e.When != "" {
		b.WriteString(" — during ")
		b.WriteString(e.When)
	}
	if e.Impact != "" {
		b.WriteString(" — impact: ")
		b.WriteString(e.Impact)
	}
	if e.Fix != "" {
		b.WriteString(" — fix: ")
		b.WriteString(e.Fix)
	}
	if e.Cause != nil {
		b.WriteString(" — cause: ")
		b.WriteString(e.Cause.Error())
	}
	return b.String()
}

// Unwrap exposes the wrapped cause for errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// fault builds a six-part actionable promotion error.
func fault(what, why, where, when, impact, fix string, cause error) *Error {
	return &Error{
		What:   what,
		Why:    why,
		Where:  where,
		When:   when,
		Impact: impact,
		Fix:    fix,
		Cause:  cause,
	}
}

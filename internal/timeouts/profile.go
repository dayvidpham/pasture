// Package timeouts owns the ordered timeout hierarchy shared by SQLite,
// lifecycle ingress, slice workflows, and callers that wait for a whole
// workflow to finish.
//
// A Profile holds one tier per waiting layer. A SQLite lock wait must end
// before either caller window can expire, and both of those must end before the
// caller stops waiting for the whole workflow. Ingress and StartSlice are
// siblings: neither is inside the other:
//
//	Tier           Field           Production   Bounds
//	innermost      SQLiteBusy      500ms        one SQLite lock wait, set as the
//	                                            DSN busy_timeout
//	caller window  Ingress         1s           one lifecycle receipt append,
//	                                            including its lock retries
//	caller window  StartSlice      2s           how long a slice sub-workflow
//	                                            waits for its start_slice signal
//	outermost      WorkflowResult  30s          how long a caller waits for a
//	                                            whole workflow to report a result
//
// TestProfile (500ms / 2s / 3s / 30s) and DeadlineTestProfile (25ms / 250ms /
// 500ms / 2s) keep the same ordering with different budgets. New rejects any
// profile that inverts it.
//
// Production code must read these values from an injected Profile and must not
// write a duration or a busy_timeout DSN literal of its own.
//
// guard.CheckTimeoutSource enforces a narrow part of that rule: over four listed
// files it reports use of the retired DefaultIngressDeadline identifier and a
// string literal carrying the retired five-second busy_timeout pragma, and
// nothing else.
//
// Two longer retry ceilings live outside this profile on purpose, because they
// bound a retry loop rather than a single wait: busyRetryCeiling in
// internal/audit/migrate.go and dbosRaceRetryCeiling in
// internal/engine/dbosinit.go, both 30s.
package timeouts

import (
	"fmt"
	"time"
)

type Kind uint8

const (
	Production Kind = iota + 1
	Test
	DeadlineTest
)

// Profile is immutable and constructor-validated. The four tiers are ordered
// from innermost to outermost: a SQLite lock wait must complete before either
// the ingress or the start_slice window can expire, and both of those must
// complete before the caller stops waiting for the whole workflow.
type Profile struct {
	kind           Kind
	sqliteBusy     time.Duration
	ingress        time.Duration
	startSlice     time.Duration
	workflowResult time.Duration
}

func New(kind Kind, sqliteBusy, ingress, startSlice, workflowResult time.Duration) (Profile, error) {
	if kind != Production && kind != Test && kind != DeadlineTest {
		return Profile{}, fmt.Errorf("timeout profile kind %d is unknown", kind)
	}
	if sqliteBusy <= 0 || ingress <= 0 || startSlice <= 0 || workflowResult <= 0 {
		return Profile{}, fmt.Errorf("timeout profile durations must all be positive: sqlite=%s ingress=%s startSlice=%s workflowResult=%s", sqliteBusy, ingress, startSlice, workflowResult)
	}
	if sqliteBusy >= ingress || sqliteBusy >= startSlice {
		return Profile{}, fmt.Errorf("timeout profile is inverted: SQLite busy timeout %s must be strictly below ingress %s and start_slice %s", sqliteBusy, ingress, startSlice)
	}
	if workflowResult <= ingress || workflowResult <= startSlice {
		return Profile{}, fmt.Errorf("timeout profile is inverted: the workflow-result wait %s must be strictly above ingress %s and start_slice %s, because a caller that stops waiting first cannot observe the inner windows", workflowResult, ingress, startSlice)
	}
	return Profile{kind: kind, sqliteBusy: sqliteBusy, ingress: ingress, startSlice: startSlice, workflowResult: workflowResult}, nil
}

// ProductionProfile allocates half of ingress to each SQLite lock wait. This
// preserves useful WAL contention absorption while leaving strict headroom
// below the one- and two-second caller windows.
func ProductionProfile() Profile {
	return must(New(Production, 500*time.Millisecond, time.Second, 2*time.Second, 30*time.Second))
}

// TestProfile favors deterministic integration runs: its two-second ingress
// window exceeds the measured 48-writer serialized queue, while a 500 ms inner
// retry remains strictly below both callers.
func TestProfile() Profile {
	return must(New(Test, 500*time.Millisecond, 2*time.Second, 3*time.Second, 30*time.Second))
}

// DeadlineTestProfile is deliberately tight and is used only by tests proving
// deadline breach and crash-safe zero-receipt behavior.
func DeadlineTestProfile() Profile {
	return must(New(DeadlineTest, 25*time.Millisecond, 250*time.Millisecond, 500*time.Millisecond, 2*time.Second))
}

func KnownProfiles() []Profile {
	return []Profile{ProductionProfile(), TestProfile(), DeadlineTestProfile()}
}
func (p Profile) Kind() Kind                { return p.kind }
func (p Profile) SQLiteBusy() time.Duration { return p.sqliteBusy }
func (p Profile) Ingress() time.Duration    { return p.ingress }
func (p Profile) StartSlice() time.Duration { return p.startSlice }

// WorkflowResult is the outermost tier: how long a caller waits for a whole
// durable workflow to report its result. It is above every inner window on
// purpose. A caller that gives up sooner than the inner windows would report a
// timeout for work that was still inside its own budget, which hides the real
// state of the workflow and makes the failure hard to read.
func (p Profile) WorkflowResult() time.Duration { return p.workflowResult }
func (p Profile) IsZero() bool                  { return p.kind == 0 }
func (p Profile) Validate() error {
	_, err := New(p.kind, p.sqliteBusy, p.ingress, p.startSlice, p.workflowResult)
	return err
}

func must(profile Profile, err error) Profile {
	if err != nil {
		panic(err)
	}
	return profile
}

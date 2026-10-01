// Package gateauthority carries the value types the gate decision reads: who
// holds which task episode, in which phase, under which session claim.
//
// It sits at the bottom of the import graph on purpose. It imports task
// identifiers, the harness id and the runtime vocabulary, and nothing of
// pasture's task store. The task store imports THIS package and implements
// Reader; the edge points that way and never back, which is what keeps the
// decision types free of the store.
//
// Every enum here has an Unset zero value that is INVALID, so a value nobody
// set cannot be mistaken for a real one.
package gateauthority

import (
	"context"
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// ─── ActionClass ─────────────────────────────────────────────────────────────

// ActionClass is the closed set of action kinds a gate decides about. It is the
// harness-independent class of what the agent is about to do, so one legality
// rule covers every host that can express the action.
type ActionClass uint8

const (
	// ActionUnset is the invalid zero. A gate that reads it has been handed a
	// value nobody set.
	ActionUnset ActionClass = iota
	ActionToolUse
	ActionPromptSubmit
	ActionStop
	ActionSubagent
	ActionWorktree
	ActionCompact
	ActionConfigChange
	ActionTaskLifecycle
	ActionPermission
	ActionPostHoc
	// ActionObservation is a pure observation: the host is telling pasture that
	// something happened, not asking whether it may happen.
	//
	// An observation that REPORTS A COMPLETED ACTION of a named class takes this
	// arm too, not the class of the action it reports. The reason is the
	// decision order: an observation exits on its semantic before its class is
	// ever read, so giving it the named class would put a value into the tables
	// that nothing can reach, and would read as a rule when it is not one. A
	// post-hoc GATE, which the host does ask, keeps ActionPostHoc.
	ActionObservation
	// actionClassCeiling is the exclusive upper sentinel. It is the ONLY place
	// the arm count lives: a new arm above goes in before it, and every
	// derivation over this enum reads it instead of a hand-written list.
	actionClassCeiling
)

// ActionClasses returns every valid arm in ordinal order. It is derived from
// the sentinel, so an arm added above appears here with no other edit.
func ActionClasses() []ActionClass {
	arms := make([]ActionClass, 0, int(actionClassCeiling)-1)
	for arm := ActionUnset + 1; arm < actionClassCeiling; arm++ {
		arms = append(arms, arm)
	}
	return arms
}

// IsValid reports whether the class is a real arm and not the unset zero.
func (a ActionClass) IsValid() bool { return a > ActionUnset && a < actionClassCeiling }

var actionClassTokens = map[ActionClass]string{
	ActionToolUse:       "tool-use",
	ActionPromptSubmit:  "prompt-submit",
	ActionStop:          "stop",
	ActionSubagent:      "subagent",
	ActionWorktree:      "worktree",
	ActionCompact:       "compact",
	ActionConfigChange:  "config-change",
	ActionTaskLifecycle: "task-lifecycle",
	ActionPermission:    "permission",
	ActionPostHoc:       "post-hoc",
	ActionObservation:   "observation",
}

// String renders the class as its canonical token, or as a named invalid value.
func (a ActionClass) String() string {
	if token, ok := actionClassTokens[a]; ok {
		return token
	}
	if a == ActionUnset {
		return "action-unset"
	}
	return fmt.Sprintf("ActionClass(%d)", uint8(a))
}

// ─── TaskPhase ───────────────────────────────────────────────────────────────

// TaskPhase is the phase of the task an episode governs. It mirrors the task
// store's phase vocabulary, offset by one so the zero value can be invalid: the
// store's own phase enum starts its first arm at zero.
type TaskPhase uint8

const (
	// TaskPhaseUnset is the invalid zero.
	TaskPhaseUnset TaskPhase = iota
	PhaseRequest
	PhaseElicit
	PhasePropose
	PhaseReview
	PhasePlanUAT
	PhaseRatify
	PhaseHandoff
	PhaseImplPlan
	PhaseWorkerSlices
	PhaseCodeReview
	PhaseImplUAT
	PhaseLanding
	PhaseUnscoped
	// taskPhaseCeiling is the exclusive upper sentinel and the only place the
	// arm count lives.
	taskPhaseCeiling
)

// TaskPhases returns every valid arm in ordinal order, derived from the
// sentinel.
func TaskPhases() []TaskPhase {
	arms := make([]TaskPhase, 0, int(taskPhaseCeiling)-1)
	for arm := TaskPhaseUnset + 1; arm < taskPhaseCeiling; arm++ {
		arms = append(arms, arm)
	}
	return arms
}

// IsValid reports whether the phase is a real arm and not the unset zero.
func (p TaskPhase) IsValid() bool { return p > TaskPhaseUnset && p < taskPhaseCeiling }

var taskPhaseTokens = map[TaskPhase]string{
	PhaseRequest:      "request",
	PhaseElicit:       "elicit",
	PhasePropose:      "propose",
	PhaseReview:       "review",
	PhasePlanUAT:      "plan_uat",
	PhaseRatify:       "ratify",
	PhaseHandoff:      "handoff",
	PhaseImplPlan:     "impl_plan",
	PhaseWorkerSlices: "worker_slices",
	PhaseCodeReview:   "code_review",
	PhaseImplUAT:      "impl_uat",
	PhaseLanding:      "landing",
	PhaseUnscoped:     "unscoped",
}

// String renders the phase as its canonical token, or as a named invalid value.
func (p TaskPhase) String() string {
	if token, ok := taskPhaseTokens[p]; ok {
		return token
	}
	if p == TaskPhaseUnset {
		return "phase-unset"
	}
	return fmt.Sprintf("TaskPhase(%d)", uint8(p))
}

// PhaseFromProvenance maps one task-store phase to its gate phase. ok is false
// for a value outside the store's own valid range, so a phase read from a
// record written by a newer binary is refused instead of silently renamed.
//
// The mapping is the ordinal plus one, because the store's first arm is zero
// and this enum keeps zero for Unset. It is written as arithmetic, not as a
// table, so a phase added to the store needs one arm here and nothing else.
func PhaseFromProvenance(p provenance.Phase) (TaskPhase, bool) {
	if !p.IsValid() {
		return TaskPhaseUnset, false
	}
	mapped := TaskPhase(uint8(p) + 1)
	if !mapped.IsValid() {
		return TaskPhaseUnset, false
	}
	return mapped, true
}

// ─── AssignmentRole ──────────────────────────────────────────────────────────

// AssignmentRole is the slot an episode's occupant holds. It mirrors the task
// store's role vocabulary through the canonical payload token, which is what
// the store writes into the material event, so neither package imports the
// other.
type AssignmentRole uint8

const (
	// RoleUnset is the invalid zero.
	RoleUnset AssignmentRole = iota
	RoleOwnerResponsibility
	RoleGoverningSupervisor
	RoleAxisReviewer
	// assignmentRoleCeiling is the exclusive upper sentinel and the only place
	// the arm count lives.
	assignmentRoleCeiling
)

// AssignmentRoles returns every valid arm in ordinal order, derived from the
// sentinel.
func AssignmentRoles() []AssignmentRole {
	arms := make([]AssignmentRole, 0, int(assignmentRoleCeiling)-1)
	for arm := RoleUnset + 1; arm < assignmentRoleCeiling; arm++ {
		arms = append(arms, arm)
	}
	return arms
}

// IsValid reports whether the role is a real arm and not the unset zero.
func (r AssignmentRole) IsValid() bool { return r > RoleUnset && r < assignmentRoleCeiling }

var assignmentRoleTokens = map[AssignmentRole]string{
	RoleOwnerResponsibility: "owner-responsibility",
	RoleGoverningSupervisor: "governing-supervisor",
	RoleAxisReviewer:        "axis-reviewer",
}

// String renders the role as its canonical payload token, or as a named invalid
// value.
func (r AssignmentRole) String() string {
	if token, ok := assignmentRoleTokens[r]; ok {
		return token
	}
	if r == RoleUnset {
		return "role-unset"
	}
	return fmt.Sprintf("AssignmentRole(%d)", uint8(r))
}

// RoleFromToken maps the canonical payload token the task store writes into an
// assignment-start event to this package's role. ok is false for a token this
// build does not know, so a role written by a newer binary is refused rather
// than read as the wrong slot.
//
// This function IS the bridge between the two role enums. It exists so the
// mirror stays checkable without an import in either direction.
func RoleFromToken(token string) (AssignmentRole, bool) {
	for role, known := range assignmentRoleTokens {
		if known == token {
			return role, true
		}
	}
	return RoleUnset, false
}

// ─── Episode, claim, authority ───────────────────────────────────────────────

// Episode is one started assignment episode: who holds what, in which slot, on
// a task in which phase.
type Episode struct {
	Assignment provenance.AssignmentID
	Task       provenance.TaskID
	Role       AssignmentRole
	Phase      TaskPhase
}

// SessionClaim is what a harness session is bound to. Bound false means the
// session was never claimed, which is not an error: the gate proceeds and
// records that the session was unbound. Known reports whether the harness
// session id itself was recognised.
type SessionClaim struct {
	Bound   bool
	Actor   provenance.ActorID
	Session string
	Known   bool
}

// ActorAuthority is every started episode one actor holds at the snapshot
// instant.
type ActorAuthority struct {
	Actor    provenance.ActorID
	Episodes []Episode
}

// ─── Reader and Snapshot ─────────────────────────────────────────────────────

// Reader opens one consistent view of the gate facts.
type Reader interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

// Snapshot is one consistent view of the gate facts.
type Snapshot interface {
	ResolveSession(harness ir.HarnessID, session string) (SessionClaim, error)
	Authority(actor provenance.ActorID) (ActorAuthority, error)
	Close() error
}

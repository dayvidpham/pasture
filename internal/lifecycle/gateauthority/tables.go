package gateauthority

// tables.go publishes the two legality tables the gate decision reads: what a
// role may do, and what a task phase allows. Both are EXHAUSTIVE: every role
// and every phase carries a verdict for every action class.
//
// The tables are BUILT from one rule plus a short exception list, not written
// out cell by cell. The rule is ALLOW. The exception list is the whole of the
// policy a reader has to check, and it is the same text the user approves.
//
// A cell that is not in a table at all is a REFUSAL, never a denial. A missing
// cell means the tables and the enums disagree, which is a fault in pasture,
// and pasture does not answer a fault by blocking the user's work.

import "fmt"

// Verdict is what a table says about one (role or phase, action) pair.
type Verdict uint8

const (
	// VerdictUnset is the invalid zero. A caller that reads it has a value
	// nobody set, which is not the same as a missing cell.
	VerdictUnset Verdict = iota
	// VerdictAllow lets the action through.
	VerdictAllow
	// VerdictDeny stops the action. It is a policy answer and it fails closed.
	VerdictDeny
	// VerdictRefuse means the tables could not answer. It is a FAULT answer and
	// it fails open: the action is not judged and it is not stopped.
	VerdictRefuse
	// verdictCeiling is the exclusive upper sentinel.
	verdictCeiling
)

// Verdicts returns every valid arm in ordinal order, derived from the sentinel.
func Verdicts() []Verdict {
	arms := make([]Verdict, 0, int(verdictCeiling)-1)
	for arm := VerdictUnset + 1; arm < verdictCeiling; arm++ {
		arms = append(arms, arm)
	}
	return arms
}

// IsValid reports whether the verdict is a real arm and not the unset zero.
func (v Verdict) IsValid() bool { return v > VerdictUnset && v < verdictCeiling }

// String renders the verdict as its canonical token, or as a named invalid
// value.
func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDeny:
		return "deny"
	case VerdictRefuse:
		return "refuse"
	case VerdictUnset:
		return "verdict-unset"
	default:
		return fmt.Sprintf("Verdict(%d)", uint8(v))
	}
}

// ─── the policy, as a rule plus exceptions ───────────────────────────────────

// neverDeniedActions are the action classes NO table may deny, whatever the
// role or the phase. They are the classes where a denial would take the user's
// own control away rather than hold an agent to its assignment: ending a turn,
// compacting, answering a permission prompt, submitting a prompt, a record of
// something already done, and a pure observation.
//
// The build refuses to place a denial on one of these, so an exception list
// that names one is a build-time fault and not a shipped rule.
var neverDeniedActions = map[ActionClass]bool{
	ActionStop:         true,
	ActionCompact:      true,
	ActionPermission:   true,
	ActionPostHoc:      true,
	ActionPromptSubmit: true,
	ActionObservation:  true,
}

// roleDenials is the whole role exception list. A role absent from this map
// denies nothing. Owner-responsibility and governing-supervisor hold their
// tasks and deny nothing; an axis reviewer is reading, so it may not change the
// shape of the work it is reading.
var roleDenials = map[AssignmentRole][]ActionClass{
	RoleAxisReviewer: {ActionWorktree, ActionSubagent, ActionConfigChange, ActionTaskLifecycle},
}

// phaseDenials is the whole phase exception list. A phase absent from this map
// denies nothing.
//
// The three approval phases and the landing phase are the points where the work
// is being judged or shipped, so the shape of the work is held still: no new
// worktree, no new subagent, and, while approval is in progress, no task
// lifecycle change either. Landing does not deny a task lifecycle change,
// because closing the work is what landing is.
var phaseDenials = map[TaskPhase][]ActionClass{
	PhasePlanUAT: {ActionWorktree, ActionSubagent, ActionTaskLifecycle},
	PhaseRatify:  {ActionWorktree, ActionSubagent, ActionTaskLifecycle},
	PhaseImplUAT: {ActionWorktree, ActionSubagent, ActionTaskLifecycle},
	PhaseLanding: {ActionWorktree, ActionSubagent},
}

// RoleActionTable is the exhaustive role grid: every role, every action class.
var RoleActionTable map[AssignmentRole]map[ActionClass]Verdict

// PhaseActionTable is the exhaustive phase grid: every phase, every action
// class.
var PhaseActionTable map[TaskPhase]map[ActionClass]Verdict

func init() {
	RoleActionTable = buildGrid(AssignmentRoles(), roleDenials)
	PhaseActionTable = buildGrid(TaskPhases(), phaseDenials)
}

// buildGrid fills one whole grid from the allow rule plus its exception list.
// The rows are the enum's own arms and the columns are the action classes' own
// arms, so an arm added to either enum appears here with no edit, carrying the
// default until someone writes an exception for it.
//
// It panics on an exception the policy may not express: an unknown row, an
// invalid action class, a denial on a never-denied class, or the same class
// listed twice for one row. Every one of those is a fault in this file, found
// at process start rather than at a gate.
func buildGrid[Row comparable](rows []Row, denials map[Row][]ActionClass) map[Row]map[ActionClass]Verdict {
	known := make(map[Row]bool, len(rows))
	for _, row := range rows {
		known[row] = true
	}
	for row := range denials {
		if !known[row] {
			panic(fmt.Sprintf("gateauthority: the legality exception list names row %v, which is not an arm of its enum; remove the exception or add the arm", row))
		}
	}

	grid := make(map[Row]map[ActionClass]Verdict, len(rows))
	for _, row := range rows {
		cells := make(map[ActionClass]Verdict, len(ActionClasses()))
		for _, action := range ActionClasses() {
			cells[action] = VerdictAllow
		}
		seen := make(map[ActionClass]bool, len(denials[row]))
		for _, action := range denials[row] {
			if !action.IsValid() {
				panic(fmt.Sprintf("gateauthority: row %v denies %s, which is not a valid action class", row, action))
			}
			if neverDeniedActions[action] {
				panic(fmt.Sprintf("gateauthority: row %v denies %s, which no table may deny; a denial there would take the user's own control away", row, action))
			}
			if seen[action] {
				panic(fmt.Sprintf("gateauthority: row %v lists %s twice in its exception list", row, action))
			}
			seen[action] = true
			cells[action] = VerdictDeny
		}
		grid[row] = cells
	}
	return grid
}

// ─── lookups ─────────────────────────────────────────────────────────────────

// RoleVerdict answers what the role table says. A pair with no cell REFUSES: a
// gap between the tables and the enums is a fault in pasture, and a fault never
// denies the user's action.
func RoleVerdict(role AssignmentRole, action ActionClass) Verdict {
	return gridVerdict(RoleActionTable, role, action)
}

// PhaseVerdict answers what the phase table says, with the same refusal rule.
func PhaseVerdict(phase TaskPhase, action ActionClass) Verdict {
	return gridVerdict(PhaseActionTable, phase, action)
}

func gridVerdict[Row comparable](grid map[Row]map[ActionClass]Verdict, row Row, action ActionClass) Verdict {
	cells, ok := grid[row]
	if !ok {
		return VerdictRefuse
	}
	verdict, ok := cells[action]
	if !ok || !verdict.IsValid() {
		return VerdictRefuse
	}
	return verdict
}

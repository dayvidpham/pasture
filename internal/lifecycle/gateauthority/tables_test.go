package gateauthority_test

// tables_test.go guards the two legality tables: that they are exhaustive, that
// their content is exactly the published policy, that the classes a table may
// never deny are allowed everywhere, and that a missing cell refuses instead of
// denying.
//
// The grids are regenerated here from the enums' own arm lists, so a role,
// phase or action class added later is covered with no edit to this file.

import (
	"fmt"
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
)

// publishedRoleDenials and publishedPhaseDenials are the policy AS THE USER
// APPROVES IT, restated here on purpose. The production tables are built from
// their own copy of the exception list; this is the second copy, so a change to
// one without the other turns the content check red. That is the point: the
// policy is short, and it is the one thing in this package that must not drift
// quietly.
var publishedRoleDenials = map[gateauthority.AssignmentRole][]gateauthority.ActionClass{
	gateauthority.RoleOwnerResponsibility: nil,
	gateauthority.RoleGoverningSupervisor: nil,
	gateauthority.RoleAxisReviewer: {
		gateauthority.ActionWorktree,
		gateauthority.ActionSubagent,
		gateauthority.ActionConfigChange,
		gateauthority.ActionTaskLifecycle,
	},
}

var publishedPhaseDenials = map[gateauthority.TaskPhase][]gateauthority.ActionClass{
	gateauthority.PhasePlanUAT: {
		gateauthority.ActionWorktree,
		gateauthority.ActionSubagent,
		gateauthority.ActionTaskLifecycle,
	},
	gateauthority.PhaseRatify: {
		gateauthority.ActionWorktree,
		gateauthority.ActionSubagent,
		gateauthority.ActionTaskLifecycle,
	},
	gateauthority.PhaseImplUAT: {
		gateauthority.ActionWorktree,
		gateauthority.ActionSubagent,
		gateauthority.ActionTaskLifecycle,
	},
	gateauthority.PhaseLanding: {
		gateauthority.ActionWorktree,
		gateauthority.ActionSubagent,
	},
}

// neverDenied is the published list of action classes no table may deny.
var neverDenied = []gateauthority.ActionClass{
	gateauthority.ActionStop,
	gateauthority.ActionCompact,
	gateauthority.ActionPermission,
	gateauthority.ActionPostHoc,
	gateauthority.ActionPromptSubmit,
	gateauthority.ActionObservation,
}

// ─── exhaustiveness ──────────────────────────────────────────────────────────

// TestBothTablesAreExhaustive regenerates the whole grid from the enums' own
// arm lists and fails on ANY missing or invalid cell.
//
// RED when: a cell is missing, which is what happens when a role, a phase or an
// action class is added and the tables are not rebuilt from it.
func TestBothTablesAreExhaustive(t *testing.T) {
	actions := gateauthority.ActionClasses()
	roles := gateauthority.AssignmentRoles()
	phases := gateauthority.TaskPhases()

	// NON-VACUITY: a grid check over an empty axis proves nothing.
	if len(actions) == 0 || len(roles) == 0 || len(phases) == 0 {
		t.Fatalf("the derived axes are %d action classes, %d roles, %d phases; want each non-empty, or the grid check covers nothing", len(actions), len(roles), len(phases))
	}
	if got, want := len(gateauthority.RoleActionTable), len(roles); got != want {
		t.Errorf("the role table has %d rows; want %d, one per role arm", got, want)
	}
	if got, want := len(gateauthority.PhaseActionTable), len(phases); got != want {
		t.Errorf("the phase table has %d rows; want %d, one per phase arm", got, want)
	}

	var missing []string
	for _, role := range roles {
		cells, ok := gateauthority.RoleActionTable[role]
		if !ok {
			missing = append(missing, fmt.Sprintf("role %s has no row", role))
			continue
		}
		for _, action := range actions {
			verdict, ok := cells[action]
			if !ok || !verdict.IsValid() {
				missing = append(missing, fmt.Sprintf("role %s x %s", role, action))
			}
		}
	}
	for _, phase := range phases {
		cells, ok := gateauthority.PhaseActionTable[phase]
		if !ok {
			missing = append(missing, fmt.Sprintf("phase %s has no row", phase))
			continue
		}
		for _, action := range actions {
			verdict, ok := cells[action]
			if !ok || !verdict.IsValid() {
				missing = append(missing, fmt.Sprintf("phase %s x %s", phase, action))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d legality cell(s) are missing or hold no valid verdict: %v; every row must carry a verdict for every action class, because a gate reads the cell it lands on and there is no default at read time", len(missing), missing)
	}
}

// ─── published content ───────────────────────────────────────────────────────

// TestTableContentIsExactlyThePublishedPolicy proves every cell reads what the
// approved policy says: the listed exceptions deny, and every other cell
// allows.
//
// RED when: an exception is changed, dropped or added anywhere.
func TestTableContentIsExactlyThePublishedPolicy(t *testing.T) {
	for _, role := range gateauthority.AssignmentRoles() {
		denied := denialSet(publishedRoleDenials[role])
		for _, action := range gateauthority.ActionClasses() {
			want := gateauthority.VerdictAllow
			if denied[action] {
				want = gateauthority.VerdictDeny
			}
			if got := gateauthority.RoleVerdict(role, action); got != want {
				t.Errorf("role %s x %s reads %s; want %s, because the approved policy denies exactly %v for this role", role, action, got, want, publishedRoleDenials[role])
			}
		}
	}
	for _, phase := range gateauthority.TaskPhases() {
		denied := denialSet(publishedPhaseDenials[phase])
		for _, action := range gateauthority.ActionClasses() {
			want := gateauthority.VerdictAllow
			if denied[action] {
				want = gateauthority.VerdictDeny
			}
			if got := gateauthority.PhaseVerdict(phase, action); got != want {
				t.Errorf("phase %s x %s reads %s; want %s, because the approved policy denies exactly %v for this phase", phase, action, got, want, publishedPhaseDenials[phase])
			}
		}
	}
}

func denialSet(actions []gateauthority.ActionClass) map[gateauthority.ActionClass]bool {
	set := make(map[gateauthority.ActionClass]bool, len(actions))
	for _, action := range actions {
		set[action] = true
	}
	return set
}

// TestTheNeverDeniedClassesAreAllowedInEveryRow proves the classes where a
// denial would take the user's own control away are allowed in every row of
// both tables.
//
// RED when: any row denies one of them.
func TestTheNeverDeniedClassesAreAllowedInEveryRow(t *testing.T) {
	if len(neverDenied) == 0 {
		t.Fatalf("the never-denied list is empty, so this check covers nothing")
	}
	for _, action := range neverDenied {
		if !action.IsValid() {
			t.Fatalf("the never-denied list holds %s, which is not a valid action class", action)
		}
		for _, role := range gateauthority.AssignmentRoles() {
			if got := gateauthority.RoleVerdict(role, action); got != gateauthority.VerdictAllow {
				t.Errorf("role %s x %s reads %s; want allow, because denying it would take the user's own control away rather than hold an agent to its assignment", role, action, got)
			}
		}
		for _, phase := range gateauthority.TaskPhases() {
			if got := gateauthority.PhaseVerdict(phase, action); got != gateauthority.VerdictAllow {
				t.Errorf("phase %s x %s reads %s; want allow, because denying it would take the user's own control away rather than hold an agent to its assignment", phase, action, got)
			}
		}
	}
}

// ─── a missing cell refuses ──────────────────────────────────────────────────

// TestAMissingCellRefusesAndNeverDenies proves the one behaviour that decides
// what a user meets when the tables and the enums disagree: a gap is a FAULT,
// and a fault does not stop the user's action.
//
// The subtests mutate the published tables and restore them, so they must not
// run beside another case in this package. The package has no parallel case.
//
// RED when: a missing row, a missing cell, an unset row key or an unset action
// reads as a denial instead of a refusal.
func TestAMissingCellRefusesAndNeverDenies(t *testing.T) {
	t.Run("a removed cell", func(t *testing.T) {
		cells := gateauthority.RoleActionTable[gateauthority.RoleAxisReviewer]
		restore := cells[gateauthority.ActionWorktree]
		delete(cells, gateauthority.ActionWorktree)
		t.Cleanup(func() { cells[gateauthority.ActionWorktree] = restore })

		if restore != gateauthority.VerdictDeny {
			t.Fatalf("the cell this case removes read %s before removal; want deny, or the case is not removing the cell it means to", restore)
		}
		if got := gateauthority.RoleVerdict(gateauthority.RoleAxisReviewer, gateauthority.ActionWorktree); got != gateauthority.VerdictRefuse {
			t.Fatalf("a removed cell reads %s; want refuse, because a gap between the tables and the enums is a fault in pasture and a fault must not stop the user's action", got)
		}
	})

	t.Run("a removed row", func(t *testing.T) {
		restore := gateauthority.PhaseActionTable[gateauthority.PhaseLanding]
		delete(gateauthority.PhaseActionTable, gateauthority.PhaseLanding)
		t.Cleanup(func() { gateauthority.PhaseActionTable[gateauthority.PhaseLanding] = restore })

		if got := gateauthority.PhaseVerdict(gateauthority.PhaseLanding, gateauthority.ActionWorktree); got != gateauthority.VerdictRefuse {
			t.Fatalf("a removed row reads %s; want refuse", got)
		}
	})

	t.Run("the unset row keys and the unset action", func(t *testing.T) {
		if got := gateauthority.RoleVerdict(gateauthority.RoleUnset, gateauthority.ActionToolUse); got != gateauthority.VerdictRefuse {
			t.Errorf("the unset role reads %s for a real action; want refuse", got)
		}
		if got := gateauthority.PhaseVerdict(gateauthority.TaskPhaseUnset, gateauthority.ActionToolUse); got != gateauthority.VerdictRefuse {
			t.Errorf("the unset phase reads %s for a real action; want refuse", got)
		}
		if got := gateauthority.RoleVerdict(gateauthority.RoleAxisReviewer, gateauthority.ActionUnset); got != gateauthority.VerdictRefuse {
			t.Errorf("a real role reads %s for the unset action; want refuse", got)
		}
	})
}

// ─── the verdict enum ────────────────────────────────────────────────────────

// TestVerdictZeroIsInvalidAndEveryArmRenders proves the verdict enum obeys the
// same rule as the others: the zero is invalid, and every arm has a token.
//
// RED when: the zero reports itself valid, or an arm renders as a bare number.
func TestVerdictZeroIsInvalidAndEveryArmRenders(t *testing.T) {
	if gateauthority.VerdictUnset.IsValid() {
		t.Errorf("VerdictUnset reports itself valid; want invalid, because a verdict nobody set must not pass for an answer")
	}
	arms := gateauthority.Verdicts()
	if len(arms) == 0 {
		t.Fatalf("the derived verdict arm list is empty, so this check covers nothing")
	}
	seen := map[string]gateauthority.Verdict{}
	for _, arm := range arms {
		token := arm.String()
		if !arm.IsValid() {
			t.Errorf("derived verdict %d reports itself invalid", uint8(arm))
		}
		if first, taken := seen[token]; taken {
			t.Errorf("verdicts %d and %d both render %q; want one token each", uint8(first), uint8(arm), token)
		}
		seen[token] = arm
	}
}

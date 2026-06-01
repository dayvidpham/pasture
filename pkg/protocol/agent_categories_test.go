package protocol_test

import (
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// Membership and arity tests for the AutomatonRole and PastureRole enums.
// These guard against accidental enum drift (UAT-1 binding: AutomatonRole has
// exactly 6 values; AutomatonRoleDerivation is DROPPED; ConsensusReached and
// CreateFollowup are first-class).

func TestAutomatonRole_AllAutomatonRoles_HasExactly6Values(t *testing.T) {
	t.Parallel()

	const expected = 6
	if got := len(protocol.AllAutomatonRoles); got != expected {
		t.Fatalf("AllAutomatonRoles has %d values, want %d (UAT-1 binding: 6 values total — None, ConstraintChecker, TransitionGate, HookHandler, ConsensusReached, CreateFollowup)", got, expected)
	}
}

func TestAutomatonRole_AllAutomatonRoles_ContainsExpectedValues(t *testing.T) {
	t.Parallel()

	want := map[protocol.AutomatonRole]bool{
		protocol.AutomatonRoleNone:              false,
		protocol.AutomatonRoleConstraintChecker: false,
		protocol.AutomatonRoleTransitionGate:    false,
		protocol.AutomatonRoleHookHandler:       false,
		protocol.AutomatonRoleConsensusReached:  false,
		protocol.AutomatonRoleCreateFollowup:    false,
	}
	for _, r := range protocol.AllAutomatonRoles {
		if _, ok := want[r]; !ok {
			t.Errorf("AllAutomatonRoles contains unexpected value %q", r)
			continue
		}
		want[r] = true
	}
	for r, seen := range want {
		if !seen {
			t.Errorf("AllAutomatonRoles missing expected value %q", r)
		}
	}
}

func TestAutomatonRole_DerivationIsDropped(t *testing.T) {
	t.Parallel()

	// UAT-1 binding: the earlier AutomatonRoleDerivation catch-all has been
	// dropped in favor of two first-class enum values. If any code adds it
	// back, this test fails to ensure the regression is caught immediately.
	for _, r := range protocol.AllAutomatonRoles {
		if r.String() == "Derivation" {
			t.Fatalf("AutomatonRole 'Derivation' is in AllAutomatonRoles but UAT-1 dropped it (replaced by ConsensusReached + CreateFollowup)")
		}
	}
	// Also check IsValid does NOT accept "Derivation".
	if protocol.AutomatonRole("Derivation").IsValid() {
		t.Errorf(`AutomatonRole("Derivation").IsValid() = true, want false (UAT-1 binding)`)
	}
}

func TestAutomatonRole_IsValid_AcceptsAllKnownValues(t *testing.T) {
	t.Parallel()

	for _, r := range protocol.AllAutomatonRoles {
		if !r.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", r)
		}
	}
}

func TestAutomatonRole_IsValid_RejectsUnknownValues(t *testing.T) {
	t.Parallel()

	// Empty string and an unrelated string MUST NOT be considered valid.
	cases := []protocol.AutomatonRole{
		"",
		"Unknown",
		"constraintchecker", // case-sensitive — wire format is "ConstraintChecker"
		"Derivation",        // UAT-1: explicitly dropped
		"ResearcherNote",    // confusion test
	}
	for _, c := range cases {
		if c.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", c)
		}
	}
}

func TestAutomatonRole_String_ReturnsWireFormat(t *testing.T) {
	t.Parallel()

	cases := map[protocol.AutomatonRole]string{
		protocol.AutomatonRoleNone:              "None",
		protocol.AutomatonRoleConstraintChecker: "ConstraintChecker",
		protocol.AutomatonRoleTransitionGate:    "TransitionGate",
		protocol.AutomatonRoleHookHandler:       "HookHandler",
		protocol.AutomatonRoleConsensusReached:  "ConsensusReached",
		protocol.AutomatonRoleCreateFollowup:    "CreateFollowup",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("AutomatonRole(%v).String() = %q, want %q", r, got, want)
		}
	}
}

// ─── PastureRole ─────────────────────────────────────────────────────────────

func TestPastureRole_AllPastureRoles_HasExactly5Values(t *testing.T) {
	t.Parallel()

	const expected = 5
	if got := len(protocol.AllPastureRoles); got != expected {
		t.Fatalf("AllPastureRoles has %d values, want %d (None + Architect + Supervisor + Worker + Reviewer)", got, expected)
	}
}

func TestPastureRole_AllPastureRoles_ContainsExpectedValues(t *testing.T) {
	t.Parallel()

	want := map[protocol.PastureRole]bool{
		protocol.PastureRoleNone:       false,
		protocol.PastureRoleArchitect:  false,
		protocol.PastureRoleSupervisor: false,
		protocol.PastureRoleWorker:     false,
		protocol.PastureRoleReviewer:   false,
	}
	for _, r := range protocol.AllPastureRoles {
		if _, ok := want[r]; !ok {
			t.Errorf("AllPastureRoles contains unexpected value %q", r)
			continue
		}
		want[r] = true
	}
	for r, seen := range want {
		if !seen {
			t.Errorf("AllPastureRoles missing expected value %q", r)
		}
	}
}

func TestPastureRole_IsValid_AcceptsAllKnownValues(t *testing.T) {
	t.Parallel()

	for _, r := range protocol.AllPastureRoles {
		if !r.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", r)
		}
	}
}

func TestPastureRole_IsValid_RejectsUnknownValues(t *testing.T) {
	t.Parallel()

	cases := []protocol.PastureRole{
		"",
		"Unknown",
		"architect", // case-sensitive
		"Automaton", // not a pasture role
	}
	for _, c := range cases {
		if c.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", c)
		}
	}
}

func TestPastureRole_String_ReturnsWireFormat(t *testing.T) {
	t.Parallel()

	cases := map[protocol.PastureRole]string{
		protocol.PastureRoleNone:       "None",
		protocol.PastureRoleArchitect:  "Architect",
		protocol.PastureRoleSupervisor: "Supervisor",
		protocol.PastureRoleWorker:     "Worker",
		protocol.PastureRoleReviewer:   "Reviewer",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("PastureRole(%v).String() = %q, want %q", r, got, want)
		}
	}
}

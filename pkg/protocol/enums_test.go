package protocol_test

import (
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── VoteType ────────────────────────────────────────────────────────────────

func TestVoteType_IsValid(t *testing.T) {
	t.Parallel()

	valid := []protocol.VoteType{protocol.VoteAccept, protocol.VoteRevise}
	for _, v := range valid {
		v := v
		t.Run("valid_"+string(v), func(t *testing.T) {
			t.Parallel()
			if !v.IsValid() {
				t.Errorf("VoteType(%q).IsValid() = false, want true", v)
			}
		})
	}

	invalid := []protocol.VoteType{"", "accept", "ACCEPT_ALL", "revise"}
	for _, v := range invalid {
		v := v
		t.Run("invalid_"+string(v), func(t *testing.T) {
			t.Parallel()
			if v.IsValid() {
				t.Errorf("VoteType(%q).IsValid() = true, want false", v)
			}
		})
	}
}

func TestAllVoteTypes_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(protocol.AllVoteTypes); got != 2 {
		t.Errorf("len(AllVoteTypes) = %d, want 2", got)
	}
	for _, v := range protocol.AllVoteTypes {
		if !v.IsValid() {
			t.Errorf("AllVoteTypes contains invalid VoteType %q", v)
		}
	}
}

// ─── ReviewAxis ──────────────────────────────────────────────────────────────

func TestReviewAxis_IsValid(t *testing.T) {
	t.Parallel()

	valid := []protocol.ReviewAxis{protocol.AxisCorrectness, protocol.AxisTestQuality, protocol.AxisElegance}
	for _, a := range valid {
		a := a
		t.Run("valid_"+string(a), func(t *testing.T) {
			t.Parallel()
			if !a.IsValid() {
				t.Errorf("ReviewAxis(%q).IsValid() = false, want true", a)
			}
		})
	}

	invalid := []protocol.ReviewAxis{"", "A", "B", "C", "Correctness", "TEST_QUALITY"}
	for _, a := range invalid {
		a := a
		t.Run("invalid_"+string(a), func(t *testing.T) {
			t.Parallel()
			if a.IsValid() {
				t.Errorf("ReviewAxis(%q).IsValid() = true, want false", a)
			}
		})
	}
}

func TestAllReviewAxes_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(protocol.AllReviewAxes); got != 3 {
		t.Errorf("len(AllReviewAxes) = %d, want 3", got)
	}
	for _, a := range protocol.AllReviewAxes {
		if !a.IsValid() {
			t.Errorf("AllReviewAxes contains invalid ReviewAxis %q", a)
		}
	}
}

// ─── SeverityLevel ──────────────────────────────────────────────────────────

func TestSeverityLevel_IsValid(t *testing.T) {
	t.Parallel()

	valid := []protocol.SeverityLevel{protocol.SeverityBlocker, protocol.SeverityImportant, protocol.SeverityMinor}
	for _, s := range valid {
		s := s
		t.Run("valid_"+string(s), func(t *testing.T) {
			t.Parallel()
			if !s.IsValid() {
				t.Errorf("SeverityLevel(%q).IsValid() = false, want true", s)
			}
		})
	}

	invalid := []protocol.SeverityLevel{"", "BLOCKER", "Important", "trivial", "critical"}
	for _, s := range invalid {
		s := s
		t.Run("invalid_"+string(s), func(t *testing.T) {
			t.Parallel()
			if s.IsValid() {
				t.Errorf("SeverityLevel(%q).IsValid() = true, want false", s)
			}
		})
	}
}

func TestAllSeverityLevels_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(protocol.AllSeverityLevels); got != 3 {
		t.Errorf("len(AllSeverityLevels) = %d, want 3", got)
	}
	for _, s := range protocol.AllSeverityLevels {
		if !s.IsValid() {
			t.Errorf("AllSeverityLevels contains invalid SeverityLevel %q", s)
		}
	}
}

// ─── RoleId ──────────────────────────────────────────────────────────────────

func TestRoleId_IsValid(t *testing.T) {
	t.Parallel()

	valid := []protocol.RoleId{
		protocol.RoleEpoch,
		protocol.RoleArchitect,
		protocol.RoleReviewer,
		protocol.RoleSupervisor,
		protocol.RoleWorker,
	}
	for _, r := range valid {
		r := r
		t.Run("valid_"+string(r), func(t *testing.T) {
			t.Parallel()
			if !r.IsValid() {
				t.Errorf("RoleId(%q).IsValid() = false, want true", r)
			}
		})
	}

	invalid := []protocol.RoleId{"", "EPOCH", "Architect", "lead", "manager"}
	for _, r := range invalid {
		r := r
		t.Run("invalid_"+string(r), func(t *testing.T) {
			t.Parallel()
			if r.IsValid() {
				t.Errorf("RoleId(%q).IsValid() = true, want false", r)
			}
		})
	}
}

func TestAllRoleIds_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(protocol.AllRoleIds); got != 5 {
		t.Errorf("len(AllRoleIds) = %d, want 5", got)
	}
	for _, r := range protocol.AllRoleIds {
		if !r.IsValid() {
			t.Errorf("AllRoleIds contains invalid RoleId %q", r)
		}
	}
}

package types_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/types"
)

// ─── VoteType ────────────────────────────────────────────────────────────────

func TestVoteType_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.VoteType{types.VoteAccept, types.VoteRevise}
	for _, v := range valid {
		v := v
		t.Run("valid_"+string(v), func(t *testing.T) {
			t.Parallel()
			if !v.IsValid() {
				t.Errorf("VoteType(%q).IsValid() = false, want true", v)
			}
		})
	}

	invalid := []types.VoteType{"", "accept", "ACCEPT_ALL", "revise"}
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
	if got := len(types.AllVoteTypes); got != 2 {
		t.Errorf("len(AllVoteTypes) = %d, want 2", got)
	}
	for _, v := range types.AllVoteTypes {
		if !v.IsValid() {
			t.Errorf("AllVoteTypes contains invalid VoteType %q", v)
		}
	}
}

// ─── ReviewAxis ──────────────────────────────────────────────────────────────

func TestReviewAxis_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.ReviewAxis{types.AxisCorrectness, types.AxisTestQuality, types.AxisElegance}
	for _, a := range valid {
		a := a
		t.Run("valid_"+string(a), func(t *testing.T) {
			t.Parallel()
			if !a.IsValid() {
				t.Errorf("ReviewAxis(%q).IsValid() = false, want true", a)
			}
		})
	}

	invalid := []types.ReviewAxis{"", "A", "B", "C", "Correctness", "TEST_QUALITY"}
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
	if got := len(types.AllReviewAxes); got != 3 {
		t.Errorf("len(AllReviewAxes) = %d, want 3", got)
	}
	for _, a := range types.AllReviewAxes {
		if !a.IsValid() {
			t.Errorf("AllReviewAxes contains invalid ReviewAxis %q", a)
		}
	}
}

// ─── OutputFormat ────────────────────────────────────────────────────────────

func TestOutputFormat_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.OutputFormat{types.OutputJSON, types.OutputText}
	for _, f := range valid {
		f := f
		t.Run("valid_"+string(f), func(t *testing.T) {
			t.Parallel()
			if !f.IsValid() {
				t.Errorf("OutputFormat(%q).IsValid() = false, want true", f)
			}
		})
	}

	invalid := []types.OutputFormat{"", "JSON", "TEXT", "xml", "yaml"}
	for _, f := range invalid {
		f := f
		t.Run("invalid_"+string(f), func(t *testing.T) {
			t.Parallel()
			if f.IsValid() {
				t.Errorf("OutputFormat(%q).IsValid() = true, want false", f)
			}
		})
	}
}

func TestAllOutputFormats_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(types.AllOutputFormats); got != 2 {
		t.Errorf("len(AllOutputFormats) = %d, want 2", got)
	}
	for _, f := range types.AllOutputFormats {
		if !f.IsValid() {
			t.Errorf("AllOutputFormats contains invalid OutputFormat %q", f)
		}
	}
}

// ─── AuditTrailBackend ───────────────────────────────────────────────────────

func TestAuditTrailBackend_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.AuditTrailBackend{types.BackendMemory, types.BackendSqlite}
	for _, b := range valid {
		b := b
		t.Run("valid_"+string(b), func(t *testing.T) {
			t.Parallel()
			if !b.IsValid() {
				t.Errorf("AuditTrailBackend(%q).IsValid() = false, want true", b)
			}
		})
	}

	invalid := []types.AuditTrailBackend{"", "none", "Memory", "SQLite", "postgres"}
	for _, b := range invalid {
		b := b
		t.Run("invalid_"+string(b), func(t *testing.T) {
			t.Parallel()
			if b.IsValid() {
				t.Errorf("AuditTrailBackend(%q).IsValid() = true, want false", b)
			}
		})
	}
}

func TestAllAuditTrailBackends_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(types.AllAuditTrailBackends); got != 2 {
		t.Errorf("len(AllAuditTrailBackends) = %d, want 2", got)
	}
	for _, b := range types.AllAuditTrailBackends {
		if !b.IsValid() {
			t.Errorf("AllAuditTrailBackends contains invalid AuditTrailBackend %q", b)
		}
	}
}

// ─── BumpKind ────────────────────────────────────────────────────────────────

func TestBumpKind_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.BumpKind{types.BumpMajor, types.BumpMinor, types.BumpPatch}
	for _, k := range valid {
		k := k
		t.Run("valid_"+string(k), func(t *testing.T) {
			t.Parallel()
			if !k.IsValid() {
				t.Errorf("BumpKind(%q).IsValid() = false, want true", k)
			}
		})
	}

	invalid := []types.BumpKind{"", "MAJOR", "Minor", "prerelease", "1"}
	for _, k := range invalid {
		k := k
		t.Run("invalid_"+string(k), func(t *testing.T) {
			t.Parallel()
			if k.IsValid() {
				t.Errorf("BumpKind(%q).IsValid() = true, want false", k)
			}
		})
	}
}

func TestAllBumpKinds_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(types.AllBumpKinds); got != 3 {
		t.Errorf("len(AllBumpKinds) = %d, want 3", got)
	}
	for _, k := range types.AllBumpKinds {
		if !k.IsValid() {
			t.Errorf("AllBumpKinds contains invalid BumpKind %q", k)
		}
	}
}

// ─── Domain ──────────────────────────────────────────────────────────────────

func TestDomain_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.Domain{types.DomainUser, types.DomainPlan, types.DomainImpl}
	for _, d := range valid {
		d := d
		t.Run("valid_"+string(d), func(t *testing.T) {
			t.Parallel()
			if !d.IsValid() {
				t.Errorf("Domain(%q).IsValid() = false, want true", d)
			}
		})
	}

	invalid := []types.Domain{"", "User", "PLAN", "implementation"}
	for _, d := range invalid {
		d := d
		t.Run("invalid_"+string(d), func(t *testing.T) {
			t.Parallel()
			if d.IsValid() {
				t.Errorf("Domain(%q).IsValid() = true, want false", d)
			}
		})
	}
}

func TestAllDomains_Completeness(t *testing.T) {
	t.Parallel()
	if got := len(types.AllDomains); got != 3 {
		t.Errorf("len(AllDomains) = %d, want 3", got)
	}
	for _, d := range types.AllDomains {
		if !d.IsValid() {
			t.Errorf("AllDomains contains invalid Domain %q", d)
		}
	}
}

// ─── RoleId ──────────────────────────────────────────────────────────────────

func TestRoleId_IsValid(t *testing.T) {
	t.Parallel()

	valid := []types.RoleId{
		types.RoleEpoch,
		types.RoleArchitect,
		types.RoleReviewer,
		types.RoleSupervisor,
		types.RoleWorker,
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

	invalid := []types.RoleId{"", "EPOCH", "Architect", "lead", "manager"}
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
	if got := len(types.AllRoleIds); got != 5 {
		t.Errorf("len(AllRoleIds) = %d, want 5", got)
	}
	for _, r := range types.AllRoleIds {
		if !r.IsValid() {
			t.Errorf("AllRoleIds contains invalid RoleId %q", r)
		}
	}
}

package types_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/types"
)

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

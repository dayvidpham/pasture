package tasks_test

// pool_test.go — Tests for the PASTURE_DB_POOL_SIZE env knob (ResolveDBPoolSize).
//
// These tests mutate process-wide environment state, so:
//   - Do NOT call t.Parallel inside any of these tests.
//   - Do NOT use t.Setenv (it calls t.Helper + os.Setenv without the
//     testutil.SetEnv restore/cleanup semantics we rely on).
//   - Use testutil.SetEnv / testutil.UnsetEnv for all env mutations — they
//     install a t.Cleanup that restores the previous value.
//
// Tests are run with: go test -run PoolSize ./internal/tasks/

import (
	"errors"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// TestPoolSize_Unset_ResolvesToDefault verifies that when PASTURE_DB_POOL_SIZE
// is not set, ResolveDBPoolSize returns DefaultDBPoolSize (1).
func TestPoolSize_Unset_ResolvesToDefault(t *testing.T) {
	testutil.UnsetEnv(t, tasks.DBPoolSizeEnv)

	got, err := tasks.ResolveDBPoolSize()
	if err != nil {
		t.Fatalf("ResolveDBPoolSize() unexpected error: %v", err)
	}
	if got != tasks.DefaultDBPoolSize {
		t.Errorf("ResolveDBPoolSize() = %d, want %d (DefaultDBPoolSize)", got, tasks.DefaultDBPoolSize)
	}
}

// TestPoolSize_ValidEnv_Resolves verifies that a valid positive-integer value
// in PASTURE_DB_POOL_SIZE is resolved and returned without error.
func TestPoolSize_ValidEnv_Resolves(t *testing.T) {
	testutil.SetEnv(t, tasks.DBPoolSizeEnv, "4")

	got, err := tasks.ResolveDBPoolSize()
	if err != nil {
		t.Fatalf("ResolveDBPoolSize() unexpected error: %v", err)
	}
	if got != 4 {
		t.Errorf("ResolveDBPoolSize() = %d, want 4", got)
	}
}

// TestPoolSize_NonInteger_ReturnsStructuredError verifies that a non-integer
// value in PASTURE_DB_POOL_SIZE returns a *StructuredError with
// CategoryValidation.
func TestPoolSize_NonInteger_ReturnsStructuredError(t *testing.T) {
	testutil.SetEnv(t, tasks.DBPoolSizeEnv, "not-an-int")

	_, err := tasks.ResolveDBPoolSize()
	if err == nil {
		t.Fatal("ResolveDBPoolSize() expected error for non-integer env value; got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("ResolveDBPoolSize() expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("StructuredError.Category = %q, want %q (CategoryValidation)", se.Category, pasterrors.CategoryValidation)
	}
}

// TestPoolSize_Zero_ReturnsStructuredError verifies that a zero value in
// PASTURE_DB_POOL_SIZE returns a *StructuredError (pool must be >= 1).
func TestPoolSize_Zero_ReturnsStructuredError(t *testing.T) {
	testutil.SetEnv(t, tasks.DBPoolSizeEnv, "0")

	_, err := tasks.ResolveDBPoolSize()
	if err == nil {
		t.Fatal("ResolveDBPoolSize() expected error for zero env value; got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("ResolveDBPoolSize() expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("StructuredError.Category = %q, want %q (CategoryValidation)", se.Category, pasterrors.CategoryValidation)
	}
}

// TestPoolSize_Negative_ReturnsStructuredError verifies that a negative value
// in PASTURE_DB_POOL_SIZE returns a *StructuredError (pool must be >= 1).
func TestPoolSize_Negative_ReturnsStructuredError(t *testing.T) {
	testutil.SetEnv(t, tasks.DBPoolSizeEnv, "-1")

	_, err := tasks.ResolveDBPoolSize()
	if err == nil {
		t.Fatal("ResolveDBPoolSize() expected error for negative env value; got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("ResolveDBPoolSize() expected *StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("StructuredError.Category = %q, want %q (CategoryValidation)", se.Category, pasterrors.CategoryValidation)
	}
}

// TestPoolSize_DefaultIsOne confirms that DefaultDBPoolSize is exactly 1, which
// is the proven-safe pool size (zero escaped SQLITE_BUSY in the
// cross-subsystem race test).
func TestPoolSize_DefaultIsOne(t *testing.T) {
	if tasks.DefaultDBPoolSize != 1 {
		t.Errorf("DefaultDBPoolSize = %d, want 1 (production-safe default)", tasks.DefaultDBPoolSize)
	}
}

package tasks_test

// tracker_busy_classifier_test.go — Direct unit tests for isBusyOrLockedErr.
//
// These tests run under plain `go test ./internal/tasks/` (no -race or CGO
// required) and verify that isBusyOrLockedErr correctly classifies wrapped
// StructuredErrors by walking their Unwrap chain to reach the driver Cause —
// not just the top-level "category: what" string.
//
// Two cases are mandatory (per the fix handoff):
//   (a) StructuredError wrapping a SQLITE_BUSY/LOCKED cause → true
//   (b) StructuredError wrapping a non-busy cause (e.g. FK constraint) → false
//       (proves the classifier does not mask fatal storage errors as busy)

import (
	"fmt"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// TestIsBusyOrLockedErr exercises isBusyOrLockedErr with StructuredErrors whose
// Cause holds the real driver message. The classifier must walk Unwrap() to
// reach the cause; checking only the top-level Error() string ("category: what")
// would miss it.
func TestIsBusyOrLockedErr(t *testing.T) {
	t.Run("wrapped SQLITE_BUSY cause returns true", func(t *testing.T) {
		// Simulate what modernc.org/sqlite emits when the busy_timeout
		// expires: the driver error is wrapped inside a StructuredError
		// whose .Error() is "storage error: <what>" — not the raw driver text.
		driverErr := fmt.Errorf("database is locked (SQLITE_BUSY)")
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "A concurrent write timed out waiting for the database lock.",
			Why:      "SQLite's busy_timeout expired before the lock was released.",
			Where:    "internal/tasks/tracker_busy_classifier_test.go (test fixture)",
			Impact:   "The write was not committed.",
			Fix:      "Reduce write concurrency or increase busy_timeout.",
			Cause:    driverErr,
		}
		if !isBusyOrLockedErr(se) {
			t.Errorf("isBusyOrLockedErr(%q) = false; want true — must walk Unwrap to find the SQLITE_BUSY cause", se.Error())
		}
	})

	t.Run("wrapped SQLITE_LOCKED cause returns true", func(t *testing.T) {
		driverErr := fmt.Errorf("database table is locked (SQLITE_LOCKED)")
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "A read was blocked by a table lock.",
			Cause:    driverErr,
		}
		if !isBusyOrLockedErr(se) {
			t.Errorf("isBusyOrLockedErr(%q) = false; want true — must walk Unwrap to find the SQLITE_LOCKED cause", se.Error())
		}
	})

	t.Run("wrapped non-busy storage error returns false", func(t *testing.T) {
		// A FOREIGN KEY constraint failure is a fatal storage error; the
		// classifier must NOT misread it as a transient busy/locked error.
		driverErr := fmt.Errorf("FOREIGN KEY constraint failed")
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "A write violated a referential integrity constraint.",
			Why:      "The referenced row does not exist in the parent table.",
			Cause:    driverErr,
		}
		if isBusyOrLockedErr(se) {
			t.Errorf("isBusyOrLockedErr(%q) = true; want false — FK constraint must not be masked as busy", se.Error())
		}
	})

	t.Run("nil error returns false", func(t *testing.T) {
		if isBusyOrLockedErr(nil) {
			t.Errorf("isBusyOrLockedErr(nil) = true; want false")
		}
	})

	t.Run("bare database is locked error returns true", func(t *testing.T) {
		// Bare driver error (no StructuredError wrapping) must still match —
		// the fast-path substring check on the top-level Error() handles this.
		bareErr := fmt.Errorf("database is locked")
		if !isBusyOrLockedErr(bareErr) {
			t.Errorf("isBusyOrLockedErr(%q) = false; want true for bare locked message", bareErr.Error())
		}
	})
}

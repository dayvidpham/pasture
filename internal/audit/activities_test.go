package audit_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
)

// TestInitTrail_NilResetsState verifies that calling InitTrail(nil) is
// safe and resets the singleton (used in test teardown).
func TestInitTrail_NilResetsState(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	audit.InitTrail(trail)
	t.Cleanup(func() { audit.InitTrail(nil) })

	// A second call with nil should not panic.
	audit.InitTrail(nil)
}

// TestInitTrail_ReplacesImpl verifies that InitTrail can replace an
// existing Trail implementation (e.g. swapping memory → sqlite in tests).
func TestInitTrail_ReplacesImpl(t *testing.T) {
	trail1 := audit.NewInMemoryAuditTrail()
	trail2 := audit.NewInMemoryAuditTrail()

	audit.InitTrail(trail1)
	audit.InitTrail(trail2)
	t.Cleanup(func() { audit.InitTrail(nil) })
	// No panic = success; singleton accepts replacement.
}

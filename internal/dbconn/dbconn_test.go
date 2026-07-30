package dbconn

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

func TestDSNUsesInjectedProfileBusyTimeout(t *testing.T) {
	t.Parallel()
	if got := SharedDSNWithProfile("db", timeouts.TestProfile()); !strings.Contains(got, "busy_timeout(25)") {
		t.Fatalf("test DSN=%q, want injected 25ms", got)
	}
	if got := SharedDSNWithProfile("db", timeouts.ProductionProfile()); !strings.Contains(got, "busy_timeout(500)") {
		t.Fatalf("production DSN=%q, want injected 500ms", got)
	}
}

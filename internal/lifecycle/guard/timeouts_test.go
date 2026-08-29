package guard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKnownTimeoutProfilesPreserveOrdering(t *testing.T) {
	t.Parallel()
	if err := ValidateKnownTimeoutProfiles(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionTimeoutSitesUseInjectedProfile(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	// Every production file that opens a SQLite handle or waits on one belongs
	// in this list. internal/audit/sqlite.go and internal/engine/engine.go open
	// the handles the durable engine writes through; internal/acceptance/
	// snapshot.go builds its own read-only connection string and carried a
	// hard-coded five-second retry until it was moved onto the profile.
	for _, relative := range []string{
		"internal/dbconn/dbconn.go",
		"internal/engine/slice.go",
		"internal/engine/engine.go",
		"internal/audit/sqlite.go",
		"internal/acceptance/snapshot.go",
		"internal/lifecycle/receipt/clock.go",
		"internal/lifecycle/receipt/journal.go",
	} {
		path := filepath.Join(root, relative)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if findings := CheckTimeoutSource(path, source); len(findings) != 0 {
			t.Fatalf("%s: %v", relative, findings)
		}
	}
}

func TestTimeoutSourceGuardBites(t *testing.T) {
	t.Parallel()
	source := []byte("package bad\nconst DefaultIngressDeadline = 1\nconst dsn = `file:x?_pragma=busy_timeout(5000)`\n")
	if findings := CheckTimeoutSource("bad.go", source); len(findings) != 2 {
		t.Fatalf("findings=%v, want two", findings)
	}
}

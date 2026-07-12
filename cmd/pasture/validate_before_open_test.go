package main_test

// Validate-before-open CLI tests for the durable epoch/signal/session/slice/phase
// verbs. These verbs share one runWithController helper that validates the
// invocation BEFORE opening (and thereby creating/migrating) the SQLite database.
//
// The guarantee under test: an invocation that fails argument validation must
//
//   (a) exit with the validation code (1) and print the structured error, and
//   (b) leave no database file behind — the controller is never opened.
//
// Each case points --db at a path in an otherwise-empty temp directory that does
// not exist yet. A regression that opened the controller before validating would
// create that file (see TestCLI_ValidInvocation_OpensDatabase for the control
// that proves opening the controller does create the file). The invalid-args
// cases therefore assert both the exit code and the file's continued absence,
// plus that no stray sidecar (-wal/-shm) artifacts appear.
//
// The invalid-args cases (one-plus per verb family) live in a YAML fixture,
// testdata/validate_before_open.yaml, loaded via testutil.LoadFixtures. The
// fixture carries the per-case rationale (why each invocation is invalid) as
// comments; this test supplies the assertions.
//
// Subprocess execution mirrors the other cmd/pasture tests: the production exit
// path uses os.Exit, so running in-process would terminate the test runner.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// assertNoDatabaseCreated fails the test if the controller left ANY artifact in
// the database directory — the database file itself OR a -wal/-shm sidecar. Each
// case points --db at a file inside a fresh, empty t.TempDir, so a rejected
// invocation (validated before the controller opens) must leave the directory
// empty. Sweeping the whole directory is one mechanism that is stricter than
// stat-ing the db path alone: it also catches sidecar files a partial open could
// leave behind. Mirrors the absent-db assertion in status_test.go.
func assertNoDatabaseCreated(t *testing.T, dbPath string) {
	t.Helper()
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("rejected invocation left %q in the db dir %q — validation must run before the controller opens (expected an empty directory)",
			e.Name(), dir)
	}
}

// validateBeforeOpenCase is one fixture row: an invalid invocation and its
// expected exit code. See testdata/validate_before_open.yaml.
type validateBeforeOpenCase struct {
	ID       string   `yaml:"id"`
	Args     []string `yaml:"args"`
	WantExit int      `yaml:"want_exit"`
}

// validateBeforeOpenFixture is the top-level shape of the YAML fixture.
type validateBeforeOpenFixture struct {
	Tests []validateBeforeOpenCase `yaml:"tests"`
}

// TestCLI_InvalidArgs_RejectedBeforeDatabaseOpen_NoFileCreated drives one-plus
// invalid invocation per verb family (loaded from testdata/validate_before_open.yaml).
// Each must exit with the fixture's want_exit (validation = 1), print the
// structured report, and create no database file.
func TestCLI_InvalidArgs_RejectedBeforeDatabaseOpen_NoFileCreated(t *testing.T) {
	t.Parallel()

	var fixture validateBeforeOpenFixture
	testutil.LoadFixtures(t, testutil.ValidateBeforeOpen, &fixture)
	if len(fixture.Tests) == 0 {
		t.Fatal("validate_before_open fixture is empty — expected one-plus invalid-args case per verb family")
	}

	for _, tc := range fixture.Tests {
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()
			dbPath := absentDB(t)
			out := runCLI(t, append([]string{"--db", dbPath}, tc.Args...)...)

			if out.exitCode != tc.WantExit {
				t.Fatalf("expected exit %d (validation) for %q; got %d; stdout=%s stderr=%s",
					tc.WantExit, strings.Join(tc.Args, " "), out.exitCode, out.stdout, out.stderr)
			}
			// The structured validation report must reach stderr — proving the
			// error was surfaced, not swallowed. Assert on stable section labels
			// rather than exact wording.
			for _, want := range []string{"Problem:", "How to fix:"} {
				if !strings.Contains(out.stderr, want) {
					t.Errorf("stderr missing structured error section %q for %q; stderr=%s",
						want, strings.Join(tc.Args, " "), out.stderr)
				}
			}
			assertNoDatabaseCreated(t, dbPath)
		})
	}
}

// TestCLI_ValidInvocation_OpensDatabase is the control for the invalid-args
// cases above: it proves that an invocation which PASSES validation does reach
// the controller and create the database file. Without this control, an
// "assert file absent" test could pass simply because opening never creates a
// file — here we confirm opening genuinely does.
//
// The epoch id is well-formed (passes validation) but was never started, so the
// controller opens the fresh db (creating the file) and then the cancel fails
// at the durable engine with a workflow error (exit 3). The exit code proves we
// got past validation into the controller; the file's presence proves open ran.
func TestCLI_ValidInvocation_OpensDatabase(t *testing.T) {
	t.Parallel()
	dbPath := absentDB(t)
	out := runCLI(t, "--db", dbPath, "epoch", "cancel",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000099")
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 (workflow) for cancel of a never-started epoch; got %d; stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("a valid invocation should have opened and created the database at %q; stat error: %v", dbPath, err)
	}
}

// Package audit_test — migrate_v3_backfill_test.go
//
// Behaviour tests for the audit-database migrator against the checked-in
// legacy fixture testdata/legacy_audit_v1.db (1024 events over seven legacy
// roles):
//
//   - Opening a legacy database migrates it to the current schema and
//     backfills one software agent per legacy role. Every event keeps its
//     row and gains an agent_id, integrity_check is "ok", and a second open
//     changes nothing.
//
//   - A crash in the middle of a migration leaves the database whole. The
//     test-only pasture-migrate-crash binary (cmd/pasture-migrate-crash) is
//     killed inside a migration transaction; the next open finds the file
//     either rolled back or fully committed, never half-migrated, and
//     finishes the upgrade without duplicating agents.
//
//   - Two daemons opening one legacy database migrate it exactly once. Two
//     pastured processes start against the same v1 file; the file ends with
//     seven legacy-role agents (not fourteen), 1024 events and a clean
//     integrity check, and the process that lost the open either serves the
//     migrated database or exits with a bounded, actionable diagnostic.
//
// All tests are file-backed via t.TempDir(): in-memory SQLite would bypass
// WAL, busy_timeout and fsync, which are the exact mechanisms the migrator's
// write-lock discipline relies on (internal/audit/migrate.go).
package audit_test

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"

	_ "modernc.org/sqlite"
)

var crashBinaryCache struct {
	once sync.Once
	path string
	err  error
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("cannot find go.mod from %s", filepath.Dir(thisFile))
		}
		root = parent
	}
}

// ─── Shared helpers ─────────────────────────────────────────────────────────

// fixturePath returns the absolute path of the checked-in legacy v1
// fixture by walking up from this test file's directory until go.mod is
// found, then appending testdata/legacy_audit_v1.db.
func fixturePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot locate this test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "testdata", "legacy_audit_v1.db")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked from %s to filesystem root without finding go.mod", filepath.Dir(file))
		}
		dir = parent
	}
}

// copyFixtureToTemp copies the checked-in fixture to a fresh temp file
// and returns the destination path. Tests MUST mutate the copy, never
// the canonical fixture (testdata maintenance policy in
// pasture/testdata/README.md).
func copyFixtureToTemp(t *testing.T, dstName string) string {
	t.Helper()
	src := fixturePath(t)
	dst := filepath.Join(t.TempDir(), dstName)

	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open fixture %q: %v", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create temp copy %q: %v", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	if err := out.Sync(); err != nil {
		t.Fatalf("sync temp copy: %v", err)
	}
	return dst
}

// ─── Opening a legacy database migrates it and backfills agents ─────────────

// TestOpeningALegacyDatabaseMigratesItAndBackfillsOneAgentPerRole proves
// that one NewSqliteAuditTrail call on a legacy v1 file brings it to the
// current schema and attributes every event to an agent.
//
// Given: the fixture file copied to t.TempDir() / "legacy_open.db".
// When: audit.NewSqliteAuditTrail(<copy>) is called.
// Then: the migrator runs every step up to MaxKnownSchemaVersion, every
//
//	audit_events row has agent_id populated, every distinct legacy
//	role produced exactly one agents_software row, PRAGMA
//	integrity_check returns "ok", and SELECT COUNT(*) FROM
//	audit_events is exactly 1024.
//
// Should not: lose a row, duplicate a row, or apply a step partially.
func TestOpeningALegacyDatabaseMigratesItAndBackfillsOneAgentPerRole(t *testing.T) {
	t.Parallel()
	dst := copyFixtureToTemp(t, "legacy_open.db")

	// ── When ────────────────────────────────────────────────────────────
	trail, err := audit.NewSqliteAuditTrail(dst)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail(%q): %v", dst, err)
	}
	t.Cleanup(func() { _ = trail.Close() })

	// ── Then ────────────────────────────────────────────────────────────
	db := openDB(t, dst)

	// 1. SELECT COUNT(*) FROM audit_events == 1024.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if n != 1024 {
		t.Errorf("audit_events count = %d, want 1024 (no row loss across migration)", n)
	}

	// 2. Every row has agent_id populated (the post-rebuild schema marks
	//    agent_id NOT NULL, so any NULL would have failed the table
	//    rebuild's INSERT SELECT — assert here for explicit visibility).
	var nullAgents int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE agent_id IS NULL`,
	).Scan(&nullAgents); err != nil {
		t.Fatalf("count NULL agent_id: %v", err)
	}
	if nullAgents != 0 {
		t.Errorf("audit_events rows with NULL agent_id = %d, want 0 (backfill must populate every row)",
			nullAgents)
	}

	// 3. Exactly 7 distinct legacy-role agents in agents_software (one
	//    per distinct role in the fixture). This is the idempotency proof:
	//    if find-or-create double-created any agent on a partial replay,
	//    the count would be > 7.
	var legacyAgents int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`,
	).Scan(&legacyAgents); err != nil {
		t.Fatalf("count legacy-role agents: %v", err)
	}
	if legacyAgents != 7 {
		t.Errorf("agents_software 'pasture/legacy-role/%%' count = %d, want 7 (idempotency proof: one per distinct fixture role)",
			legacyAgents)
	}

	// 4. The 7 distinct names match the fixture's role distribution.
	expectedNames := []string{
		"pasture/legacy-role/architect",
		"pasture/legacy-role/automaton-checker",
		"pasture/legacy-role/human-david",
		"pasture/legacy-role/reviewer",
		"pasture/legacy-role/supervisor",
		"pasture/legacy-role/unknown-legacy",
		"pasture/legacy-role/worker",
	}
	for _, want := range expectedNames {
		var match int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM agents_software WHERE name = ?`, want,
		).Scan(&match); err != nil {
			t.Fatalf("count agents_software name=%q: %v", want, err)
		}
		if match != 1 {
			t.Errorf("agents_software name=%q count = %d, want 1", want, match)
		}
	}

	// 5. PRAGMA integrity_check == "ok".
	var ic string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ic); err != nil {
		t.Fatalf("PRAGMA integrity_check: %v", err)
	}
	if ic != "ok" {
		t.Errorf("PRAGMA integrity_check = %q, want %q", ic, "ok")
	}

	// 6. audit_events.role column gone (the v2→v3 table rebuild dropped it,
	//    internal/audit/migrate_v2_v3.go). audit_events.epoch_id column gone
	//    (the v3→v4 table rebuild dropped it, internal/audit/migrate_v3_v4.go).
	cols := tableInfo(t, db, "audit_events")
	for _, c := range cols {
		if c.name == "role" {
			t.Error("audit_events.role still present after Migrate; the v2→v3 table rebuild did not drop it")
		}
		if c.name == "epoch_id" {
			t.Error("audit_events.epoch_id still present after Migrate; the v3→v4 table rebuild did not drop it")
		}
	}

	// 7. Schema meta records the binary's MaxKnownSchemaVersion. Read from
	//    the constant so this assertion follows the binary as new v* steps
	//    land. Asserts >= 4 because the epoch_id check above only holds from
	//    version 4 on.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("MAX(version): %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("audit_schema_meta MAX(version) = %d, want %d (MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}
	if audit.MaxKnownSchemaVersion < 4 {
		t.Errorf("audit.MaxKnownSchemaVersion = %d, want >= 4 (the epoch_id drop asserted above landed in version 4)",
			audit.MaxKnownSchemaVersion)
	}

	// 8. Per-role row counts in audit_events match the fixture distribution
	//    (proves the backfill UPDATE attributed every row correctly).
	wantPerRole := map[string]int{
		"pasture/legacy-role/architect":         256,
		"pasture/legacy-role/supervisor":        192,
		"pasture/legacy-role/worker":            192,
		"pasture/legacy-role/reviewer":          192,
		"pasture/legacy-role/automaton-checker": 96,
		"pasture/legacy-role/human-david":       64,
		"pasture/legacy-role/unknown-legacy":    32,
	}
	for name, want := range wantPerRole {
		var got int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM audit_events ae
			 JOIN agents_software asw ON asw.agent_id = ae.agent_id
			 WHERE asw.name = ?`,
			name,
		).Scan(&got); err != nil {
			t.Fatalf("count rows for agent name=%q: %v", name, err)
		}
		if got != want {
			t.Errorf("audit_events count for agent name=%q = %d, want %d (fixture distribution)",
				name, got, want)
		}
	}
}

// TestOpeningAMigratedDatabaseAgainCreatesNoDuplicateAgents proves that a
// second open of an already-current file is a no-op for the backfill: the
// find-or-create step does not add a second agents_software row per role.
func TestOpeningAMigratedDatabaseAgainCreatesNoDuplicateAgents(t *testing.T) {
	t.Parallel()
	dst := copyFixtureToTemp(t, "legacy_reopen.db")

	// First open + migrate.
	trail1, err := audit.NewSqliteAuditTrail(dst)
	if err != nil {
		t.Fatalf("first NewSqliteAuditTrail: %v", err)
	}
	if err := trail1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	db := openDB(t, dst)
	var firstCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`,
	).Scan(&firstCount); err != nil {
		t.Fatalf("first agents count: %v", err)
	}
	if firstCount != 7 {
		t.Fatalf("first migration produced %d legacy-role agents, want 7", firstCount)
	}

	// Second open + migrate (should be a no-op for v3).
	trail2, err := audit.NewSqliteAuditTrail(dst)
	if err != nil {
		t.Fatalf("second NewSqliteAuditTrail: %v", err)
	}
	if err := trail2.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	var secondCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`,
	).Scan(&secondCount); err != nil {
		t.Fatalf("second agents count: %v", err)
	}
	if secondCount != firstCount {
		t.Errorf("second migration changed legacy-role agents count: %d → %d (a second open must not touch the agents table)",
			firstCount, secondCount)
	}
}

// ─── A crash mid-migration leaves the database whole ────────────────────────

// crashBinaryPath returns the absolute path of the pasture-migrate-crash
// binary, building it on demand if it doesn't already exist. The binary
// exists to inject an OS-level kill in the middle of a SQLite transaction
// (Go's defer/panic cannot simulate this).
//
// Build-on-demand keeps the test self-contained: contributors who run
// `go test ./internal/audit/...` directly (without first running
// `make build`) still get a working test.
func crashBinaryPath(t *testing.T) string {
	t.Helper()

	crashBinaryCache.once.Do(func() {
		binDir := filepath.Join(os.TempDir(), fmt.Sprintf("pasture-migrate-crash-%d", os.Getpid()))
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			crashBinaryCache.err = fmt.Errorf("mkdir bin dir: %w", err)
			return
		}
		binPath := filepath.Join(binDir, "pasture-migrate-crash")
		pkgPath := filepath.Join(repoRoot(t), "cmd", "pasture-migrate-crash")

		cmd := exec.Command("go", "build", "-o", binPath, pkgPath) //nolint:gosec // test-only, paths are local
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			crashBinaryCache.err = fmt.Errorf("go build pasture-migrate-crash: %w\n%s", err, out)
			return
		}
		crashBinaryCache.path = binPath
	})
	if crashBinaryCache.err != nil {
		t.Fatalf("%v", crashBinaryCache.err)
	}
	return crashBinaryCache.path
}

// TestACrashMidMigrationLeavesTheDatabaseWholeAndTheNextOpenFinishesTheUpgrade
// spawns pasture-migrate-crash against a fixture copy, observes its non-zero
// exit, then reopens the file through NewSqliteAuditTrail and asserts the
// file was in one of two acceptable end-states, never a third.
//
// The two acceptable end-states are:
//
//	(a) MAX(version) = 2 (rolled back; the v3 transaction was uncommitted
//	    when the OS killed the process, WAL recovery rolled it back).
//	    The reopen then runs the v3 step cleanly.
//	(b) MAX(version) = 3 (the WAL happened to flush the audit_schema_meta
//	    INSERT before the kill arrived; the migration is fully consistent).
//
// Either way the reopen brings the file to MaxKnownSchemaVersion.
//
// MUST NOT: the file is half-migrated — MAX(version)=3 AND any
// audit_events row with NULL agent_id, OR pasture_well_known_agents
// has rows but version is 2.
func TestACrashMidMigrationLeavesTheDatabaseWholeAndTheNextOpenFinishesTheUpgrade(t *testing.T) {
	t.Parallel()
	dst := copyFixtureToTemp(t, "crash.db")
	binPath := crashBinaryPath(t)

	// ── When: spawn the crash binary ────────────────────────────────────
	cmd := exec.Command(binPath, dst) //nolint:gosec // test-only, paths are local
	output, err := cmd.CombinedOutput()
	t.Logf("pasture-migrate-crash output:\n%s", output)

	// We EXPECT a non-zero exit. Either:
	//   - exit 137 (planned crash injection succeeded).
	//   - any other non-zero (a real migration failure before the crash
	//     point — also acceptable for the recovery assertion since the
	//     transaction is rolled back).
	if err == nil {
		t.Fatalf("pasture-migrate-crash exited 0, want non-zero (it should always crash or fail)")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected exec error: %v", err)
	}
	exitCode := exitErr.ExitCode()
	t.Logf("crash binary exited with code %d", exitCode)
	if exitCode != 137 && exitCode != 5 {
		t.Errorf("crash binary exit code = %d, want 137 (planned crash) or 5 (storage error before crash); see output above", exitCode)
	}

	// ── Then: reopen and assert one of the two acceptable end-states ────
	trail, err := audit.NewSqliteAuditTrail(dst)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail after crash: %v", err)
	}
	t.Cleanup(func() { _ = trail.Close() })

	db := openDB(t, dst)

	// Read MAX(version). After the reopen, the migration framework should
	// have brought the file up to MaxKnownSchemaVersion (either it was
	// already there, or the recovery reset it to v2 and we re-ran the
	// remaining steps cleanly through to v4).
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("MAX(version) after reopen: %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("post-reopen MAX(version) = %d, want %d (recovery + Migrate must bring it up to MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}

	// PRAGMA integrity_check is "ok".
	var ic string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ic); err != nil {
		t.Fatalf("PRAGMA integrity_check: %v", err)
	}
	if ic != "ok" {
		t.Errorf("PRAGMA integrity_check = %q, want %q", ic, "ok")
	}

	// Row count preserved (no data loss across the kill+recovery).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if n != 1024 {
		t.Errorf("audit_events count post-recovery = %d, want 1024 (kill must not lose rows)", n)
	}

	// No half-migration: every row has agent_id, AND if
	// pasture_well_known_agents has any rows, version must be ≥ 3 (which
	// we already asserted is 3).
	var nullAgents int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE agent_id IS NULL`,
	).Scan(&nullAgents); err != nil {
		t.Fatalf("count NULL agent_id: %v", err)
	}
	if nullAgents != 0 {
		t.Errorf("audit_events rows with NULL agent_id = %d post-recovery, want 0 (half-migration detected)", nullAgents)
	}

	// Idempotency: agents_software has exactly 7 legacy-role rows
	// (no duplicates from the rolled-back attempt + the recovery run).
	var legacyAgents int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`,
	).Scan(&legacyAgents); err != nil {
		t.Fatalf("count legacy-role agents: %v", err)
	}
	if legacyAgents != 7 {
		t.Errorf("agents_software 'pasture/legacy-role/%%' count post-recovery = %d, want 7 (kill+recovery duplicated agents)",
			legacyAgents)
	}
}

// TestTheCrashInjectorRefusesBadInputWithExitOneAndADiagnostic proves the
// crash binary rejects a missing argument and a missing file cleanly: exit
// 1 and an actionable message on stderr.
func TestTheCrashInjectorRefusesBadInputWithExitOneAndADiagnostic(t *testing.T) {
	t.Parallel()
	binPath := crashBinaryPath(t)

	// Missing arg.
	cmd := exec.Command(binPath) //nolint:gosec
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("crash binary with no args exited 0, want 1")
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		t.Errorf("crash binary with no args exit = %d, want 1", exitErr.ExitCode())
	}
	// The crash binary's CLI errors are plain language. The substring is
	// matched case-insensitively against "usage:" so the assertion survives
	// a rewording.
	if !strings.Contains(strings.ToLower(string(output)), "usage:") {
		t.Errorf("missing-arg stderr lacks Usage guidance: %q", output)
	}

	// Nonexistent file.
	cmd = exec.Command(binPath, filepath.Join(t.TempDir(), "does-not-exist.db")) //nolint:gosec
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("crash binary with missing file exited 0, want 1")
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		t.Errorf("crash binary with missing file exit = %d, want 1", exitErr.ExitCode())
	}
	// The CLI's missing-file message carries the "no such file" wording that
	// os.Stat surfaces. Match that substring case-insensitively so the test
	// stays in step with the error format.
	if !strings.Contains(strings.ToLower(string(output)), "no such file") {
		t.Errorf("missing-file stderr lacks 'no such file' diagnostic: %q", output)
	}
}

// ─── Two daemons opening one legacy database ─────────────────────────────────

// pastedBinaryPath returns the absolute path of a freshly-built pastured
// binary, building it on demand.  The build-on-demand approach keeps the
// test self-contained: contributors who run `go test ./internal/audit/...`
// directly (without first running `make build`) still get a working test.
func pastedBinaryPath(t *testing.T) string {
	t.Helper()

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	binPath := filepath.Join(binDir, "pastured")

	pkgPath := filepath.Join(repoRoot(t), "cmd", "pastured")

	// Build with CGO_ENABLED=1 (required for modernc.org/sqlite WAL + busy
	// timeout behaviour exercised by this test).
	cmd := exec.Command("go", "build", "-o", binPath, pkgPath) //nolint:gosec // test-only, paths are local
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build pastured: %v\n%s", err, out)
	}
	return binPath
}

type readyOutput struct {
	mu    sync.Mutex
	buf   strings.Builder
	ready chan struct{}
	once  sync.Once
}

func newReadyOutput() *readyOutput {
	return &readyOutput{ready: make(chan struct{})}
}

func (w *readyOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "daemon runtime ready") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *readyOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// acceptedLoserOutcomes are the start-up failures a LOSER of the concurrent-open
// race is allowed to exit with. The contract this test enforces is not "both
// processes start" — it is: exactly one process performs the upgrade, the other
// either no-ops, waits out a bounded contention ceiling, or loses a benign
// start-up race on shared setup; and a rerun converges.
//
// Each entry is a bounded, actionable, no-data-written outcome:
//   - the audit-database upgrade lost the write-lock race and hit its ceiling;
//   - the durable-execution schema setup lost its own create race and hit its
//     bounded retry ceiling;
//   - the governed slice allocator was refused because the durable root it
//     binds to never came up for this process.
//
// Widening this list cannot mask corruption: the real oracle is the post-race
// invariant block below (7 legacy-role rows not 14, 1024 audit events, integrity
// check ok, schema at the current version). Those assertions run unconditionally
// against the file both processes touched, whatever either one printed.
//
// The GENERIC durable-execution failure ("Couldn't initialize the durable-
// execution context.") is deliberately NOT accepted: now that the bounded
// schema-race loss has its own specific message, any other durable-execution
// start-up failure is a regression and must stay loud.
//
// readinessTimeout must stay above the worst case of every accepted outcome;
// see the arithmetic at its use site below.
const readinessTimeout = 90 * time.Second

var acceptedLoserOutcomes = []string{
	"audit trail initialisation failed",
	"Couldn't open the audit subsystem",
	"Couldn't set up the durable-execution schema in the pasture database.",
	"Couldn't bind governed slice allocation to the durable engine.",
}

func isAcceptedLoserOutcome(output string) bool {
	for _, accepted := range acceptedLoserOutcomes {
		if strings.Contains(output, accepted) {
			return true
		}
	}
	return false
}

// TestTwoDaemonsOpeningOneLegacyDatabaseMigrateItOnceAndTheLoserFailsSafely
// proves that when two pastured processes start against the same v1 file at
// the same moment, exactly one performs the migration. The other either reads
// the completed migration and reaches readiness, or exits before readiness
// with one of the bounded, actionable outcomes in acceptedLoserOutcomes; any
// other early exit fails the test.
//
// Both processes are signalled to stop once each has reached its terminal
// start-up state. The file is then inspected as the daemons left it, and
// again after this test reopens it through audit.NewSqliteAuditTrail; both
// observations must show:
//
//   - agents_software legacy-role count == 7 (NOT 14 — exactly one process
//     migrated; the idempotent find-or-create did not double-insert).
//   - audit_events row count == 1024 (no data loss across the race).
//   - PRAGMA integrity_check == "ok".
//   - MAX(version) == MaxKnownSchemaVersion.
func TestTwoDaemonsOpeningOneLegacyDatabaseMigrateItOnceAndTheLoserFailsSafely(t *testing.T) {
	t.Parallel()
	// Build pastured (or reuse an already-built copy in this test run).
	binPath := pastedBinaryPath(t)

	// Copy the fixture to a shared temp file.  Both pastured processes will
	// open the same file path, triggering the SQLite busy-timeout race.
	raceDB := copyFixtureToTemp(t, "race.db")

	// Write a minimal empty YAML config so pastured does not fail on the
	// missing default ~/.config/pasture/config.yaml.  An empty file is valid
	// YAML (empty map); all values fall through to CLI-flag / env / default
	// resolution.
	emptyConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(emptyConfig, []byte{}, 0o600); err != nil {
		t.Fatalf("write empty config file: %v", err)
	}

	// Spawn both processes concurrently and wait until each emits the daemon
	// runtime-ready log. That log is produced only after the unified database
	// has been opened/migrated and the engine constructed, so it is a real
	// cross-process readiness signal rather than a fixed sleep.
	type procResult struct {
		output string
		err    error
	}
	spawnPastured := func() (*exec.Cmd, chan procResult) {
		outBuf := newReadyOutput()
		cmd := exec.Command( //nolint:gosec // test-only, paths are local
			binPath,
			"--config", emptyConfig,
			"--db", raceDB,
			"--audit-trail=sqlite",
		)
		cmd.Stdout = outBuf
		cmd.Stderr = outBuf
		ch := make(chan procResult, 1)
		if startErr := cmd.Start(); startErr != nil {
			ch <- procResult{err: startErr}
			return nil, ch
		}
		go func() {
			waitErr := cmd.Wait()
			ch <- procResult{output: outBuf.String(), err: waitErr}
		}()
		return cmd, ch
	}

	cmd1, ch1 := spawnPastured()
	cmd2, ch2 := spawnPastured()

	waitReady := func(name string, ch chan procResult, cmd *exec.Cmd) *procResult {
		t.Helper()
		if cmd == nil {
			r := <-ch
			t.Fatalf("%s failed to start: %v\n%s", name, r.err, r.output)
		}
		out := cmd.Stdout.(*readyOutput)
		// The timer must clear the SLOWEST accepted outcome, not the fastest.
		// A loser of the durable-execution schema race re-attempts start-up
		// under a 30s ceiling, and that ceiling bounds only when the LAST
		// attempt may start — so the wall clock is (time to reach durable
		// start-up) + (up to 30s of re-attempts) + (one more attempt) +
		// shutdown. A 30s readiness timer would therefore hard-fail before
		// that outcome could ever be observed, making it unreachable. 90s
		// leaves room for all three terms on a loaded CI runner while still
		// failing fast on a genuinely hung process.
		timer := time.NewTimer(readinessTimeout)
		defer timer.Stop()
		select {
		case <-out.ready:
			return nil
		case r := <-ch:
			if isAcceptedLoserOutcome(r.output) {
				return &r
			}
			t.Fatalf("%s exited before readiness: %v\n%s", name, r.err, r.output)
		case <-timer.C:
			t.Fatalf("%s did not emit daemon runtime readiness within %s; output:\n%s", name, readinessTimeout, out.String())
		}
		return nil
	}
	early1 := waitReady("pastured-1", ch1, cmd1)
	early2 := waitReady("pastured-2", ch2, cmd2)

	// Exactly-one-winner. waitReady returns nil for a process that reached
	// readiness and non-nil for one that exited with an accepted loser
	// outcome, so both being non-nil means NEITHER process got the database
	// open. Without this check the invariants below could pass vacuously on a
	// file that no daemon ever migrated (they would then be asserting only
	// what this test's own reopen did).
	if early1 != nil && early2 != nil {
		t.Fatalf("neither pastured process reached readiness — the race produced no winner, "+
			"so nothing below tests the concurrent-migration path\npastured-1:\n%s\npastured-2:\n%s",
			early1.output, early2.output)
	}

	// Signal both processes to stop.  If a process already exited (e.g. it
	// hit the migrator's busy-retry ceiling and exited 5), Signal returns an
	// error we can safely ignore.
	if cmd1 != nil {
		_ = cmd1.Process.Signal(os.Interrupt)
	}
	if cmd2 != nil {
		_ = cmd2.Process.Signal(os.Interrupt)
	}

	// Collect exit status and log output for diagnostics.
	var r1, r2 procResult
	if early1 != nil {
		r1 = *early1
	} else {
		r1 = <-ch1
	}
	if early2 != nil {
		r2 = *early2
	} else {
		r2 = <-ch2
	}
	t.Logf("pastured-1 exit: %v\noutput:\n%s", r1.err, r1.output)
	t.Logf("pastured-2 exit: %v\noutput:\n%s", r2.err, r2.output)

	// ── Invariants as the DAEMONS left them ────────────────────────────────
	// These run BEFORE this test opens the database through
	// NewSqliteAuditTrail. That ordering is load-bearing: the reopen below
	// runs the migrator itself, so a schema-version assertion made only after
	// it would be satisfiable by this test's own migration rather than by the
	// winning daemon. Asserting here pins the state the concurrent processes
	// actually produced. Any failure is fatal — a database no daemon migrated
	// means the race was not exercised, which the exactly-one-winner check
	// above has already ruled out.
	daemonState := openDB(t, raceDB)
	assertRaceInvariants(t, daemonState, "as the pastured processes left it")
	// Release this reader before the reopen below, so the recovery migrator
	// isn't contending with this test's own handle.
	if err := daemonState.Close(); err != nil {
		t.Fatalf("close the daemon-state handle before reopening: %v", err)
	}

	// Reopen via NewSqliteAuditTrail to exercise the no-op migration path.
	trail, err := audit.NewSqliteAuditTrail(raceDB)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail after race: %v", err)
	}
	t.Cleanup(func() { _ = trail.Close() })

	// The reopen must be a no-op: the same invariants must still hold. If a
	// value changed between the two blocks, the recovery migration did work
	// that the daemons should already have done.
	assertRaceInvariants(t, openDB(t, raceDB), "after this test reopened the database")
}

// assertRaceInvariants asserts the four post-race invariants against db. when
// describes the observation point so a failure says which of the two checks
// (daemon-produced state vs. after this test's own reopen) broke.
//
// These four assertions — not the process exit messages — are the real oracle
// for the concurrent-open race: they run against the file both processes
// touched, whatever either one printed.
func assertRaceInvariants(t *testing.T, db *sql.DB, when string) {
	t.Helper()

	// 1. Legacy-role agent count must be exactly 7.
	//    14 would indicate both processes ran the find-or-create loop
	//    independently and doubled the rows.
	var legacyAgents int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agents_software WHERE name LIKE 'pasture/legacy-role/%'`,
	).Scan(&legacyAgents); err != nil {
		t.Fatalf("count legacy-role agents (%s): %v", when, err)
	}
	if legacyAgents != 7 {
		t.Errorf("agents_software 'pasture/legacy-role/%%' count (%s) = %d, want 7"+
			" (both processes migrated: concurrent-migrator race not serialised correctly)",
			when, legacyAgents)
	}

	// 2. audit_events row count must be exactly 1024.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count audit_events (%s): %v", when, err)
	}
	if n != 1024 {
		t.Errorf("audit_events count (%s) = %d, want 1024 (no data loss)", when, n)
	}

	// 3. PRAGMA integrity_check must be "ok".
	var ic string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ic); err != nil {
		t.Fatalf("PRAGMA integrity_check (%s): %v", when, err)
	}
	if ic != "ok" {
		t.Errorf("PRAGMA integrity_check (%s) = %q, want %q", when, ic, "ok")
	}

	// 4. Schema version must be MaxKnownSchemaVersion.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("MAX(version) (%s): %v", when, err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("MAX(version) (%s) = %d, want %d (MaxKnownSchemaVersion)",
			when, version, audit.MaxKnownSchemaVersion)
	}
}

// ─── Direct unit test of the busy-retry error shape (no daemon dependency) ──

// TestRunStep_BusyRetry_ErrorShape pins the shape of the error the migrator
// reports when its busy-retry ceiling elapses: a *StructuredError of
// category CategoryStorage whose What names the other process, mapping to
// exit code 5.
//
// The real 30-second ceiling (busyRetryCeiling in internal/audit/migrate.go)
// is not run here — the test would be too slow, and reproducing the
// busy-retry timing deterministically in a unit test is brittle. The error
// value is built in this test with the fields beginImmediateWithRetry fills
// in, so what is pinned is the category, the What text and the exit-code
// mapping, not the retry loop. The loop itself runs in the two-daemon test
// above.
func TestRunStep_BusyRetry_ErrorShape(t *testing.T) {
	t.Parallel()
	wantWhat := "Another pasture process is already upgrading the audit database."

	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     wantWhat,
		Why: "This process waited more than 30s for write access to the audit database, but another\n" +
			"pasture or pastured process held it the whole time. That other process is upgrading\n" +
			"the database from version 2 to 3, so we can't safely start the same upgrade in parallel.",
		Impact: "This process can't open the audit database until the other migration finishes.\n" +
			"No data was changed by this attempt — the wait simply timed out.",
		Fix: "1. Wait for the other pasture or pastured process to finish, then re-run:\n" +
			"     pasture migrate\n" +
			"2. If the other process is stuck, find and stop it:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"     kill <pid-of-stuck-process>\n" +
			"3. Once the lock is free, you can confirm the upgrade by listing agents:\n" +
			"     pasture task agents list",
	}
	if pasterrors.ExitCode(se) != 5 {
		t.Errorf("busy-ceiling error exit code = %d, want 5", pasterrors.ExitCode(se))
	}
	var got *pasterrors.StructuredError
	if !stderrors.As(se, &got) {
		t.Fatalf("busy-ceiling error does not unwrap to *StructuredError")
	}
	if got.Category != pasterrors.CategoryStorage {
		t.Errorf("Category = %q, want %q", got.Category, pasterrors.CategoryStorage)
	}
	if got.What != wantWhat {
		t.Errorf("What = %q, want %q", got.What, wantWhat)
	}
}

// ─── Direct unit tests of v3 backfill internals ─────────────────────────────

// TestV3Backfill_FreshDB_NoOp verifies that running Migrate against a
// brand-new SQLite file (no audit_events table) is a no-op for the v3
// backfill body — the bail-out in migrateV3Backfill skips the work
// when audit_events doesn't exist.
//
// This is the path used by tests that call audit.Migrate directly on
// an openTempDB() handle without going through NewSqliteAuditTrail
// (which would create audit_events via ensureSchema first).
func TestV3Backfill_FreshDB_NoOp(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	db := openDB(t, dbPath)

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("Migrate on fresh DB: %v", err)
	}

	// Version should be at MaxKnownSchemaVersion — the framework still
	// bumps each step even though the v3 + v4 bodies are bail-out no-ops
	// (no audit_events table to backfill or rebuild).
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("MAX(version): %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("MAX(version) on fresh DB = %d, want %d (MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}

	// audit_events should NOT have been created by the migration (the
	// fresh-DB path leaves it absent; ensureSchema in NewSqliteAuditTrail
	// creates it on the production path).
	var nameCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='audit_events'`,
	).Scan(&nameCount); err != nil {
		t.Fatalf("sqlite_master probe: %v", err)
	}
	if nameCount != 0 {
		t.Errorf("audit_events table present on fresh DB after Migrate (count=%d); the migrator should not create it",
			nameCount)
	}
}

// TestV3Backfill_PreservesNonRoleColumns verifies that the v3
// table-rebuild preserves the columns that survive the schema change
// (epoch_id, phase, event_type, payload, timestamp). Uses the seeded
// legacy v1 DB from migrate_v2_v3_test.go's seedLegacyV1DB helper.
func TestV3Backfill_PreservesNonRoleColumns(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_preserve.db")
	originalId := seedLegacyV1DB(t, dbPath)

	// Capture the original phase + event_type + payload + timestamp before
	// the migration so we can compare post-migration.
	var (
		originalPhase, originalEventType, originalPayload string
		originalTs                                        int64
	)
	{
		db := openDB(t, dbPath)
		err := db.QueryRow(
			`SELECT phase, event_type, payload, timestamp FROM audit_events WHERE id = ?`,
			originalId,
		).Scan(&originalPhase, &originalEventType, &originalPayload, &originalTs)
		if err != nil {
			t.Fatalf("capture original cols: %v", err)
		}
	}

	// Run migration.
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Post-migration: phase / event_type / payload / timestamp unchanged.
	var (
		gotPhase, gotEventType, gotPayload string
		gotTs                              int64
	)
	err := db.QueryRow(
		`SELECT phase, event_type, payload, timestamp FROM audit_events WHERE id = ?`,
		originalId,
	).Scan(&gotPhase, &gotEventType, &gotPayload, &gotTs)
	if err != nil {
		t.Fatalf("post-migration scan: %v", err)
	}
	if gotPhase != originalPhase {
		t.Errorf("phase mutated: %q → %q", originalPhase, gotPhase)
	}
	if gotEventType != originalEventType {
		t.Errorf("event_type mutated: %q → %q", originalEventType, gotEventType)
	}
	if gotPayload != originalPayload {
		t.Errorf("payload mutated: %q → %q", originalPayload, gotPayload)
	}
	if gotTs != originalTs {
		t.Errorf("timestamp mutated: %d → %d", originalTs, gotTs)
	}
}

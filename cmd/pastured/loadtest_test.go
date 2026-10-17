// loadtest_test.go is the disposable-store load-test harness for the
// load-test epic
// (https://github.com/dayvidpham/pasture/01a13ee7-9b63-7cdc-951b-06cda2af55eb).
//
// WHAT IT MEASURES. Five metrics on a DISPOSABLE store at a stated
// size/shape: (1) read p50/p95 — in-process tracker List over every task, the
// production `pasture task list` read without the CLI frame; (2)
// borrowed-open p50/p95 — steady OpenTaskTracker+Close on the UNCHANGED store
// (one warm-up open first; the cold replay it pays is reported, not banded),
// the open every hook pays; (3) hook faults/second at C=18 through the real
// daemon lane; (4) hook serial-probe p95 at C=18, with the burst-queue ratio
// against the same-run serial served baseline; (5) sustained committed
// events/second at C=18.
//
// WHAT IT DOES NOT CLAIM. It measures ONE machine at ONE concurrency for a
// few seconds; it is not a scaling study. The numbers it prints are what it
// observed, with the date, revision and exact command attached (Rule 1).
//
// SHAPE. By default the disposable store is a consistent copy of the
// operator's live store (sqlite3 .backup, which takes only read locks on the
// source; byte copy when sqlite3 is absent, documented in the report), taken
// into the test's temp dir. When no source store exists (CI) it builds a
// synthetic store with PASTURE_LOADTEST_TASKS tasks and says so. The harness
// NEVER measures the live store: requireDisposableDB refuses any path outside
// the sandbox and any path resolving to the live store, and every child gets
// an explicit --db plus a PASTURE_DB_PATH that points inside the sandbox, so
// even a child that dropped its flag could not reach operator state.
//
// BANDS. Absolute bands are per-shape (copy vs synthetic) and
// reference-hardware (this host): they hold off CI and are scaled by
// loadtestCIFactor on shared CI runners, where the lane acceptance row
// measured 4-5x end-to-end variance. The relative burst bound (burst p95 vs
// same-run serial served median) and the zero-fault bound hold everywhere,
// because they divide the runner and the store size out. Under -race the two
// in-process bands (read, steady open) report without failing: the detector
// instruments the measuring binary ~20x (measured), while the measured
// product (daemon, hooks) runs uninstrumented in child processes. Do not
// widen a constant to fit a slow runner: that would dilute the
// reference-host tripwire without adding signal the relative bound does
// not carry.
//
// KNOBS (all optional, documented defaults):
//
//	PASTURE_LOADTEST_SOURCE            source store to copy (default: the live
//	                                   DefaultDBPath when that file exists)
//	PASTURE_LOADTEST_FORCE_SYNTHETIC=1 build the synthetic shape even when a
//	                                   live store exists (tools/loadtest.sh
//	                                   --synthetic; what CI runs)
//	PASTURE_LOADTEST_TASKS             synthetic task count, default 600
//	PASTURE_LOADTEST_WORKERS           concurrent hook workers, default 18
//	PASTURE_LOADTEST_BURST_SECS        burst window, default 3
//	PASTURE_LOADTEST_THROUGHPUT_SECS   sustained window, default 10
//	PASTURE_LOADTEST_READ_ITERS        read repetitions, default 30
//	PASTURE_LOADTEST_OPEN_ITERS        open repetitions, default 20
//	PASTURE_LOADTEST_REPORT_ONLY=1     print without failing (exploration)
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// Load-test bands. DERIVATION: measured 2026-10-16 on the reference host by
// the harness author; REVISION: the commit landing this file; COMMAND:
// `bash tools/loadtest.sh` (copy mode: sqlite3 .backup of the live store,
// 94,359,552 bytes, 1059 tasks, schema 10) and `bash tools/loadtest.sh
// --synthetic` (synthetic mode, 600 tasks). Observed reference numbers,
// copy mode: read p50 ~7 ms / p95 ~8 ms, steady open p50 ~5 ms / p95 ~5 ms
// with one ~5.5 s cold-replay first open, served hook ~300-600 ms serial
// and burst probe p95 ~613-630 ms at 0 faults/s, sustained ~29-30 events/s
// hook-cost-bound (offered == committed: the lane is not saturated, each
// hook process costs hundreds of ms end to end). Synthetic mode: read
// ~1.4 ms, served hook ~65 ms serial, burst probe p95 ~73 ms at
// 0 faults/s, sustained ~112/s hook-cost-bound. The recorded campaign
// baselines (~35 ms probe, ~250/s) were taken on a near-empty bootstrap
// store; the bands below describe the measured shapes instead. Each
// absolute band carries a wide
// margin over its observed figure so ordinary host jitter cannot trip it,
// while the historical regression classes (read 6,000 ms, open 5.56 s,
// probe p95 2,003 ms at 7.47 faults/s) exceed every band by an order of
// magnitude or more.
//
// DIVISION OF LABOR. Absolute bands catch catastrophe on any shape. The
// tight lane tripwire is the relative burst bound below, which divides out
// store size and runner speed: a healthy batched lane serves a probe behind
// at most ~workers/batchRecords turns of queueing plus its own serial cost
// (healthy ratio ~1-2, measured 2.04 copy / 1.11 synthetic); a batching
// collapse serialises the burst into ~workers turns (ratio ~10+). The 5x
// bound sits between the healthy mechanism and the collapse it must catch.
const (
	// loadtestReadP95Band caps the List p95 per shape.
	loadtestReadP95BandCopy      = 250 * time.Millisecond
	loadtestReadP95BandSynthetic = 100 * time.Millisecond
	// loadtestOpenP95Band caps the STEADY borrowed-open p95 per shape; the
	// first open after a store change replays projections (seconds on a
	// large store) and is reported separately, not banded here.
	loadtestOpenP95Band = 50 * time.Millisecond
	// loadtestHookP95Band caps the C=18 serial-probe p95 per shape, off CI.
	loadtestHookP95BandCopy      = 1500 * time.Millisecond
	loadtestHookP95BandSynthetic = 250 * time.Millisecond
	// loadtestBurstQueueBound caps burst probe p95 vs the same-run serial
	// served median, in every shape and every environment.
	loadtestBurstQueueBound = 5.0
	// loadtestThroughputFloor floors sustained committed events/s per shape,
	// off CI. DERIVATION (2026-10-16, same revision/command as above):
	// synthetic 600-task shape sustained 111.9/s hook-cost-bound
	// (offered == committed: the lane is not saturated, each hook costs
	// ~65 ms serial, ~160 ms under C=18 load); copy shape sustained
	// ~29-30/s. The floors sit ~30% (synthetic) and ~3x (copy) below the
	// observed rates. They are COLLAPSE tripwires (a dead lane commits
	// ~0/s), not batching-loss tripwires: on a populated store the batched
	// and per-record lanes are indistinguishable (both hook-bound), so
	// lost batching is caught by the burst-queue bound instead — an
	// unbatched burst serialises the probe behind ~workers turns
	// (ratio ~10+), far above the 5.0 bound.
	loadtestThroughputFloorCopy      = 10.0
	loadtestThroughputFloorSynthetic = 80.0
	// loadtestCIFactor scales the absolute bands on shared CI runners
	// (GITHUB_ACTIONS), where the lane acceptance row measured 4-5x
	// end-to-end variance against the reference host. Upper bands multiply,
	// the throughput floor divides. The relative burst bound and the
	// zero-fault bound hold unscaled everywhere.
	loadtestCIFactor = 5.0
	// loadtestSerialLaneRuns sizes the same-run serial served baseline.
	loadtestSerialLaneRuns = 10
)

// loadtestConfig carries the harness knobs after env parsing.
type loadtestConfig struct {
	source         string
	syntheticTasks int
	workers        int
	burstWindow    time.Duration
	sustainedRun   time.Duration
	readIters      int
	openIters      int
	reportOnly     bool
	ci             bool
}

// loadtestEnvInt reads an optional positive-int knob, failing loudly on a
// non-numeric value so a typo cannot silently rescale the harness.
func loadtestEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer, got %q: run scripts/loadtest.sh with documented knobs", key, os.Getenv(key))
	}
	return n
}

// loadtestConfigFromEnv resolves every knob with its documented default.
func loadtestConfigFromEnv(t *testing.T) loadtestConfig {
	t.Helper()
	return loadtestConfig{
		source:         strings.TrimSpace(os.Getenv("PASTURE_LOADTEST_SOURCE")),
		syntheticTasks: loadtestEnvInt(t, "PASTURE_LOADTEST_TASKS", 600),
		workers:        loadtestEnvInt(t, "PASTURE_LOADTEST_WORKERS", 18),
		burstWindow:    time.Duration(loadtestEnvInt(t, "PASTURE_LOADTEST_BURST_SECS", 3)) * time.Second,
		sustainedRun:   time.Duration(loadtestEnvInt(t, "PASTURE_LOADTEST_THROUGHPUT_SECS", 10)) * time.Second,
		readIters:      loadtestEnvInt(t, "PASTURE_LOADTEST_READ_ITERS", 30),
		openIters:      loadtestEnvInt(t, "PASTURE_LOADTEST_OPEN_ITERS", 20),
		reportOnly:     os.Getenv("PASTURE_LOADTEST_REPORT_ONLY") == "1",
		ci:             os.Getenv("GITHUB_ACTIONS") != "",
	}
}

// liveStorePath resolves the operator's live store exactly the way production
// does, so the harness can refuse it. It is used ONLY for refusal and as the
// default copy source; it is never opened for measurement.
func liveStorePath() string {
	return tasks.DefaultDBPath()
}

// sameFile reports whether a and b resolve to the same filesystem path,
// tolerating missing files and symlinked temp dirs.
func sameFile(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	if evalA, err := filepath.EvalSymlinks(absA); err == nil {
		absA = evalA
	}
	if evalB, err := filepath.EvalSymlinks(absB); err == nil {
		absB = evalB
	}
	return absA == absB
}

// requireDisposableDB is the harness's proof it never measures the live
// store. It fails the test when dbPath escapes the sandbox directory the
// harness created, or when dbPath IS the operator's live store. The check is
// structural (the harness builds the path itself) AND asserted, so a future
// refactor that pointed the harness at an ambient path goes red here instead
// of measuring operator state. The decision lives in disposableDBError so
// the refusal itself is unit-tested (loadtest_guard_test.go); this wrapper
// only turns the verdict into a test failure.
func requireDisposableDB(t *testing.T, dbPath, sandbox string) {
	t.Helper()
	if err := disposableDBError(dbPath, sandbox); err != nil {
		t.Fatalf("refusing to measure: %v", err)
	}
}

// disposableDBError verifies that dbPath names a disposable store inside
// sandbox and is not the operator's live store.
func disposableDBError(dbPath, sandbox string) error {
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("resolve the harness store path %q: %w", dbPath, err)
	}
	absSandbox, err := filepath.Abs(sandbox)
	if err != nil {
		return fmt.Errorf("resolve the harness sandbox %q: %w", sandbox, err)
	}
	if eval, err := filepath.EvalSymlinks(absSandbox); err == nil {
		absSandbox = eval
	}
	if eval, err := filepath.EvalSymlinks(absDB); err == nil {
		absDB = eval
	}
	rel, err := filepath.Rel(absSandbox, absDB)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("the store %q is not under the disposable sandbox %q", absDB, absSandbox)
	}
	if sameFile(absDB, liveStorePath()) {
		return fmt.Errorf("the store %q is the operator's live store; the harness measures disposable copies only", absDB)
	}
	return nil
}

// loadtestShape describes the disposable store for the report.
type loadtestShape struct {
	method    string
	synthetic bool
	bytes     int64
	tasks     int
	schema    int
	evidence  int
}

// prepareDisposableStore builds the measured store inside the sandbox: a
// consistent copy of source when it names an existing file (defaulting to the
// live store), otherwise a synthetic store of cfg.syntheticTasks tasks. It
// returns the store path and its measured shape. The source is only ever
// READ (sqlite3 .backup takes read locks; the fallback copies the main-file
// bytes, which in WAL mode are a consistent last-checkpoint snapshot).
func prepareDisposableStore(t *testing.T, cfg loadtestConfig, sandbox string) (string, loadtestShape) {
	t.Helper()
	dbPath := filepath.Join(sandbox, "loadtest.db")
	source := cfg.source
	if source == "" && os.Getenv("PASTURE_TEST_PHASE") == "" && os.Getenv("PASTURE_LOADTEST_FORCE_SYNTHETIC") != "1" {
		if live := liveStorePath(); live != "" {
			if fi, err := os.Stat(live); err == nil && !fi.IsDir() {
				source = live
			}
		}
	}
	var shape loadtestShape
	if source != "" {
		if fi, err := os.Stat(source); err != nil || fi.IsDir() {
			t.Fatalf("the load-test source %q is not a readable file: %v", source, err)
		}
		if sameFile(source, dbPath) {
			t.Fatalf("the load-test source %q resolves to the disposable store itself; refusing a self-copy", source)
		}
		shape.method = copyStoreReadOnly(t, source, dbPath)
	} else {
		shape.method = fmt.Sprintf("synthetic build of %d tasks (no source store; set PASTURE_LOADTEST_SOURCE to copy a realistic shape)", cfg.syntheticTasks)
		shape.synthetic = true
		buildSyntheticStore(t, dbPath, cfg.syntheticTasks)
	}
	requireDisposableDB(t, dbPath, sandbox)
	shape.bytes, shape.tasks, shape.schema, shape.evidence = inspectStore(t, dbPath)
	return dbPath, shape
}

// copyStoreReadOnly takes a consistent copy of source into dst without
// modifying the source, preferring sqlite3's .backup (read locks, includes
// WAL content) and falling back to a main-file byte copy. It returns the
// method for the report.
func copyStoreReadOnly(t *testing.T, source, dst string) string {
	t.Helper()
	if sqlite3, err := exec.LookPath("sqlite3"); err == nil {
		backup := fmt.Sprintf(".backup main '%s'", strings.ReplaceAll(dst, "'", "''"))
		cmd := exec.Command(sqlite3, source, backup)
		if output, err := cmd.CombinedOutput(); err == nil {
			return fmt.Sprintf("sqlite3 .backup of %s", source)
		} else {
			t.Logf("sqlite3 .backup of %s failed (%v: %s); falling back to a main-file byte copy", source, err, strings.TrimSpace(string(output)))
		}
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("byte-copy the source store %s: %v", source, err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatalf("write the disposable store %s: %v", dst, err)
	}
	return fmt.Sprintf("main-file byte copy of %s (WAL frames after the last checkpoint are NOT in the measured shape)", source)
}

// buildSyntheticStore creates a fresh store with n tasks through the
// production open path, so the measured shape carries real journal history.
func buildSyntheticStore(t *testing.T, dbPath string, n int) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open the synthetic store: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := tracker.Create("file://loadtest-synthetic", fmt.Sprintf("synthetic task %d", i), "load-test synthetic shape",
			provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
			_ = tracker.Close()
			t.Fatalf("synthetic task %d: %v", i, err)
		}
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close the synthetic store: %v", err)
	}
}

// inspectStore reads the measured shape off the disposable store with a
// throwaway SQL handle: file bytes, task rows, audit schema version, and
// committed lifecycle occurrence evidence rows.
func inspectStore(t *testing.T, dbPath string) (bytes int64, tasks, schema, evidence int) {
	t.Helper()
	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat the disposable store: %v", err)
	}
	bytes = fi.Size()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open the disposable store for inspection: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&tasks); err != nil {
		t.Fatalf("count tasks in the disposable store: %v", err)
	}
	if err := db.QueryRow("SELECT version FROM audit_schema_meta ORDER BY version DESC LIMIT 1").Scan(&schema); err != nil {
		t.Fatalf("read the schema version of the disposable store: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM journal_evidence WHERE evidence_kind IN ('pasture.lifecycle.occurrence.v1','pasture.lifecycle.occurrence.v2')").Scan(&evidence); err != nil {
		t.Fatalf("count occurrence evidence in the disposable store: %v", err)
	}
	return bytes, tasks, schema, evidence
}

// percentile returns the min, p50, p95 and max of sorted latencies, using
// the same p95 index convention as the lane acceptance row.
func percentile(sorted []time.Duration) (min, p50, p95, max time.Duration) {
	min = sorted[0]
	p50 = sorted[len(sorted)/2]
	p95 = sorted[(len(sorted)*95)/100]
	max = sorted[len(sorted)-1]
	return min, p50, p95, max
}

// loadtestRevision reports the tree revision under test, best-effort.
func loadtestRevision() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	if output, err := cmd.Output(); err == nil {
		if rev := strings.TrimSpace(string(output)); rev != "" {
			return rev
		}
	}
	return "unknown (not a git checkout or git failed)"
}

// countOccurrenceEvidence counts committed lifecycle occurrence rows with a
// throwaway handle, so the counting never holds a lock across the measured
// window.
func countOccurrenceEvidence(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open the disposable store for evidence counting: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM journal_evidence WHERE evidence_kind IN ('pasture.lifecycle.occurrence.v1','pasture.lifecycle.occurrence.v2')").Scan(&n); err != nil {
		t.Fatalf("count occurrence evidence: %v", err)
	}
	return n
}

// TestDisposableStoreLoadHarnessIsTheRegressionTripwire is the load-test
// harness: five metrics on a disposable store, each checked against a stated
// band, with the date, revision and exact command attached to every number.
//
// SECTIONS, in order: borrowed-open on the unchanged store, read on the
// unchanged store, the serial served baseline with no burst load, the C=18
// hook burst through the real daemon lane (faults/s + serial-probe p95 +
// burst-queue ratio), sustained committed events/s, and ONE informational
// direct-path probe. The write sections run LAST because hooks commit to the
// disposable store; the open/read sections measure the store exactly as
// copied/built.
//
// FAILURE MODE. A band breach is t.Errorf, not Fatalf: every section still
// runs, so one red metric never hides the other four. PASTURE_LOADTEST_
// REPORT_ONLY=1 prints without failing (exploration, never CI).
func TestDisposableStoreLoadHarnessIsTheRegressionTripwire(t *testing.T) {
	timing := performanceTimingEnabled(t)
	if testing.Short() {
		t.Skip("the load-test harness builds two binaries, copies a store and starts a daemon; -short skips it")
	}
	cfg := loadtestConfigFromEnv(t)
	// The script hands down its mktemp sandbox (so --keep preserves the
	// exact measured store); a bare `go test` run uses a temp dir.
	sandbox := strings.TrimSpace(os.Getenv("PASTURE_LOADTEST_SANDBOX"))
	if sandbox == "" {
		sandbox = t.TempDir()
	} else if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatalf("create the handed-down sandbox %q: %v", sandbox, err)
	}
	dbPath, shape := prepareDisposableStore(t, cfg, sandbox)

	date := time.Now().UTC().Format(time.RFC3339)
	revision := loadtestRevision()
	command := strings.TrimSpace(os.Getenv("PASTURE_LOADTEST_COMMAND"))
	if command == "" {
		command = "go test -count=1 -run TestDisposableStoreLoadHarnessIsTheRegressionTripwire ./cmd/pastured/ (plus PASTURE_LOADTEST_* env; scripts/loadtest.sh sets PASTURE_LOADTEST_COMMAND)"
	}

	readP95Band := loadtestReadP95BandCopy
	hookP95Band := loadtestHookP95BandCopy
	throughputFloor := loadtestThroughputFloorCopy
	if shape.synthetic {
		readP95Band = loadtestReadP95BandSynthetic
		hookP95Band = loadtestHookP95BandSynthetic
		throughputFloor = loadtestThroughputFloorSynthetic
	}
	openP95Band := loadtestOpenP95Band
	shapeNote := "copy shape"
	if shape.synthetic {
		shapeNote = "synthetic shape"
	}
	ciNote := "off-CI bands"
	if cfg.ci {
		readP95Band = time.Duration(float64(readP95Band) * loadtestCIFactor)
		openP95Band = time.Duration(float64(openP95Band) * loadtestCIFactor)
		hookP95Band = time.Duration(float64(hookP95Band) * loadtestCIFactor)
		throughputFloor /= loadtestCIFactor
		ciNote = fmt.Sprintf("CI bands (GITHUB_ACTIONS, factor %.0f)", loadtestCIFactor)
	}
	check := func(format string, args ...any) {
		if cfg.reportOnly {
			t.Logf("REPORT-ONLY, would fail: "+format, args...)
			return
		}
		t.Errorf(format, args...)
	}
	// checkInProcess guards the two bands measured by THIS binary's own
	// loops (read, steady open). Under -race the detector instruments those
	// loops ~20x slower (measured 2026-10-16 on the reference host: open p95
	// 6 ms -> 142 ms, read p95 9 ms -> 138 ms), so the bands would assert on
	// instrumentation overhead, not on product behaviour. They report
	// without failing under race; every child-process metric (faults, probe,
	// queued ratio, throughput) stays banded because the daemon and hooks
	// are separate uninstrumented binaries. The non-race run — CI and
	// tools/loadtest.sh — bands everything.
	checkInProcess := func(format string, args ...any) {
		if !timing || underRaceDetector {
			t.Logf("RACE-BUILD, reported not banded: "+format, args...)
			return
		}
		check(format, args...)
	}

	// SECTION 1: borrowed-open on the unchanged store. The FIRST open after a
	// store change replays projections (seconds on a large store: the 5 s
	// direct-hook deadline exists because of it), so one warm-up open runs
	// first and is REPORTED, not banded: this section pins the O(1)
	// steady-state open the epic's baseline describes, on the store exactly
	// as copied/built.
	warmStarted := time.Now()
	warmTracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("warm-up open of the disposable store: %v", err)
	}
	openCold := time.Since(warmStarted)
	if err := warmTracker.Close(); err != nil {
		t.Fatalf("close after the warm-up open: %v", err)
	}
	opens := make([]time.Duration, 0, cfg.openIters)
	for i := 0; i < cfg.openIters; i++ {
		started := time.Now()
		tracker, err := tasks.OpenTaskTracker(dbPath)
		if err != nil {
			t.Fatalf("borrowed open %d of the disposable store: %v", i, err)
		}
		opens = append(opens, time.Since(started))
		if err := tracker.Close(); err != nil {
			t.Fatalf("close after borrowed open %d: %v", i, err)
		}
	}
	sort.Slice(opens, func(i, j int) bool { return opens[i] < opens[j] })
	openMin, openP50, openP95, openMax := percentile(opens)
	if openP95 > openP95Band {
		checkInProcess("borrowed-open p95 was %s over %d steady opens of the unchanged disposable store, above the %s band (%s, %s)", openP95, cfg.openIters, openP95Band, ciNote, shapeNote)
	}

	// SECTION 2: read on the unchanged store.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open the disposable store for the read section: %v", err)
	}
	reads := make([]time.Duration, 0, cfg.readIters)
	for i := 0; i < cfg.readIters; i++ {
		started := time.Now()
		ts, err := tracker.List(provenance.ListFilter{})
		if err != nil {
			_ = tracker.Close()
			t.Fatalf("read %d of the disposable store: %v", i, err)
		}
		reads = append(reads, time.Since(started))
		if i == 0 && len(ts) != shape.tasks {
			_ = tracker.Close()
			t.Fatalf("the read section listed %d tasks but the measured shape has %d; the store moved under the harness", len(ts), shape.tasks)
		}
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close after the read section: %v", err)
	}
	sort.Slice(reads, func(i, j int) bool { return reads[i] < reads[j] })
	readMin, readP50, readP95, readMax := percentile(reads)
	if readP95 > readP95Band {
		checkInProcess("read p95 was %s over %d Lists of %d tasks, above the %s band (%s, %s)", readP95, cfg.readIters, shape.tasks, readP95Band, ciNote, shapeNote)
	}

	// SECTION 3+4: the daemon lane. Built binaries (CGO-free, the shipped
	// shape), a real pastured on the disposable store, the same fixture and
	// hook argv as the lane acceptance row.
	tmp := t.TempDir()
	daemonBinary := filepath.Join(tmp, "pastured")
	buildPasturedBinary(t, daemonBinary, "")
	hookBinary := filepath.Join(tmp, "pasture")
	buildPastureHookBinary(t, hookBinary)

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("audit_trail: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fixturePath := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read the session-start fixture: %v", err)
	}

	daemon := exec.Command(daemonBinary, "--config", configPath, "--db", dbPath)
	daemon.Env = append(daemon.Environ(), "PASTURE_WRITE_LANE=auto")
	laneRuntime := laneTestRuntimeDir(t)
	isolateLaneChildEnv(t, daemon, laneRuntime)
	// Belt and braces behind the explicit --db flags: any child that
	// consulted the ambient store path lands in the sandbox, never live.
	daemon.Env = append(daemon.Env, "PASTURE_DB_PATH="+dbPath)
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start pastured on the disposable store: %v", err)
	}
	defer func() {
		_ = daemon.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = daemon.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = daemon.Process.Kill()
			<-done
		}
	}()
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	awaitLogLine(t, lines, "lifecycle write lane listening", 90*time.Second)
	go func() {
		for range lines {
		}
	}()

	faultRecord := filepath.Join(filepath.Dir(dbPath), "lifecycle-faults.jsonl")
	baselineFaults := countFaultLines(t, faultRecord)

	runHook := func(lane string) (time.Duration, error) {
		started := time.Now()
		cmd := exec.Command(hookBinary, "hook", "lifecycle",
			"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261",
			"--db", dbPath)
		cmd.Env = append(cmd.Environ(), "PASTURE_WRITE_LANE="+lane)
		isolateLaneChildEnv(t, cmd, laneRuntime)
		cmd.Env = append(cmd.Env, "PASTURE_DB_PATH="+dbPath)
		cmd.Stdin = bytes.NewReader(fixture)
		var hookStderr bytes.Buffer
		cmd.Stderr = &hookStderr
		if runErr := cmd.Run(); runErr != nil {
			return time.Since(started), fmt.Errorf("%w: %s", runErr, strings.TrimSpace(hookStderr.String()))
		}
		return time.Since(started), nil
	}

	// Warm the one-time activation cost before the measured windows, exactly
	// as the lane acceptance row does: first delivery ensures owner refs
	// through race-collapsing activations, which is amortized infrastructure,
	// not steady-state lane cost. Its latency is reported: it is the
	// per-hook served-cost witness (warm-up vs burst probe shows whether
	// queueing or per-hook work dominates).
	warmHookStarted := time.Now()
	if _, err := runHook("auto"); err != nil {
		t.Fatalf("warm the definition activations through the served lane: %v", err)
	}
	warmHook := time.Since(warmHookStarted)

	// SECTION 3a: the serial served baseline in the SAME run, with no burst
	// load. It anchors the relative burst bound: dividing the burst probe by
	// the unloaded served cost isolates lane queueing/collapse from store
	// size and runner speed, which move both numbers together. (The lane
	// acceptance row anchors against the direct path instead, but the direct
	// path replays projections per open and cannot serve as a baseline on a
	// large store — section 5 shows that directly.)
	serialLatencies := make([]time.Duration, 0, loadtestSerialLaneRuns)
	for i := 0; i < loadtestSerialLaneRuns; i++ {
		elapsed, err := runHook("auto")
		if err != nil {
			t.Fatalf("serial served baseline hook %d failed: %v", i, err)
		}
		serialLatencies = append(serialLatencies, elapsed)
	}
	sort.Slice(serialLatencies, func(i, j int) bool { return serialLatencies[i] < serialLatencies[j] })
	serialMin, _, serialMedian, serialMax := percentile(serialLatencies)

	// SECTION 3: C=18 burst with a serial probe.
	windowStarted := time.Now()
	var wg sync.WaitGroup
	workerErr := make(chan error, cfg.workers)
	for i := 0; i < cfg.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runHook("auto"); err != nil {
				workerErr <- err
			}
		}()
	}
	probeLatencies := []time.Duration{}
	deadline := time.Now().Add(cfg.burstWindow)
	for time.Now().Before(deadline) {
		elapsed, err := runHook("auto")
		if err != nil {
			workerErr <- err
			break
		}
		probeLatencies = append(probeLatencies, elapsed)
	}
	wg.Wait()
	close(workerErr)
	for err := range workerErr {
		t.Errorf("a hook invocation failed against a healthy daemon lane: %v", err)
	}
	burstWindow := time.Since(windowStarted)
	burstFaults := countFaultLines(t, faultRecord) - baselineFaults
	if len(probeLatencies) == 0 {
		t.Fatal("the serial probe ran no invocations")
	}
	sort.Slice(probeLatencies, func(i, j int) bool { return probeLatencies[i] < probeLatencies[j] })
	probeMin, probeP50, probeP95, probeMax := percentile(probeLatencies)
	burstFaultsPerSecond := float64(burstFaults) / burstWindow.Seconds()
	if burstFaultsPerSecond != 0 {
		check("hook faults/second was %.3f in the C=%d burst (%d appended faults), above the zero-fault band", burstFaultsPerSecond, cfg.workers, burstFaults)
	}
	if timing && probeP95 > hookP95Band {
		check("hook serial-probe p95 was %s over %d runs at C=%d, above the %s band (%s, %s)", probeP95, len(probeLatencies), cfg.workers, hookP95Band, ciNote, shapeNote)
	}
	queued := float64(probeP95) / float64(serialMedian)
	if timing && queued > loadtestBurstQueueBound {
		check("lane burst p95 was %.2fx the same-run serial served median (%s vs %s), above the %.1f burst-queue bound: the burst queued far beyond one batched turn",
			queued, probeP95, serialMedian, loadtestBurstQueueBound)
	}

	// SECTION 4: sustained committed events/second.
	evidenceBefore := countOccurrenceEvidence(t, dbPath)
	sustainedStart := time.Now()
	var offerWg sync.WaitGroup
	offered := make(chan int, cfg.workers)
	sustainedErr := make(chan error, cfg.workers)
	sustainedDeadline := time.Now().Add(cfg.sustainedRun)
	for i := 0; i < cfg.workers; i++ {
		offerWg.Add(1)
		go func() {
			defer offerWg.Done()
			n := 0
			for time.Now().Before(sustainedDeadline) {
				if _, err := runHook("auto"); err != nil {
					sustainedErr <- err
					break
				}
				n++
			}
			offered <- n
		}()
	}
	offerWg.Wait()
	close(offered)
	close(sustainedErr)
	for err := range sustainedErr {
		t.Errorf("a sustained-load hook invocation failed: %v", err)
	}
	sustainedWindow := time.Since(sustainedStart)
	totalOffered := 0
	for n := range offered {
		totalOffered += n
	}
	evidenceAfter := countOccurrenceEvidence(t, dbPath)
	committed := evidenceAfter - evidenceBefore
	if committed != totalOffered {
		t.Errorf("sustained receipts: committed %d of %d offered; every successful hook must have durable evidence", committed, totalOffered)
	}
	eventsPerSecond := float64(committed) / sustainedWindow.Seconds()
	if timing && eventsPerSecond < throughputFloor {
		check("sustained events/second was %.1f at C=%d (%d committed of %d offered in %s), below the %.0f floor (%s, %s)", eventsPerSecond, cfg.workers, committed, totalOffered, sustainedWindow.Round(time.Millisecond), throughputFloor, ciNote, shapeNote)
	}
	sustainedFaults := countFaultLines(t, faultRecord) - baselineFaults - burstFaults
	if sustainedFaults != 0 {
		check("the sustained run appended %d fault lines; the zero-fault band holds for the whole harness, not just the burst", sustainedFaults)
	}

	// SECTION 5: ONE direct-path probe, informational only. The direct path
	// re-opens the store per invocation, and the open replays projections
	// whenever the snapshot is stale; on a large store that replay alone
	// exceeds the 5 s hook-invocation deadline, so the direct path faults
	// and the daemon lane is REQUIRED, not optional. A band here would assert
	// on store size, not on code, so this probe only witnesses the fact (and
	// its fault line, if any, lands in the disposable sandbox). Fail-open
	// faults exit 0, so the verdict comes from the fault record, not the
	// exit code.
	directFaultsBefore := countFaultLines(t, faultRecord)
	directElapsed, directErr := runHook("direct")
	directNote := "served"
	if directErr != nil {
		directNote = fmt.Sprintf("process error: %v", directErr)
	} else if countFaultLines(t, faultRecord) > directFaultsBefore {
		directNote = "fault (expected on stores whose open replay exceeds the hook deadline)"
	}

	t.Logf("load harness: date=%s revision=%s command=%q", date, revision, command)
	t.Logf("load harness: shape=%s bytes=%d tasks=%d schema=%d occurrenceEvidence=%d",
		shape.method, shape.bytes, shape.tasks, shape.schema, shape.evidence)
	t.Logf("load harness: read iters=%d min=%s p50=%s p95=%s max=%s band=%s | open cold=%s steady iters=%d min=%s p50=%s p95=%s max=%s band=%s (%s, %s)",
		cfg.readIters, readMin, readP50, readP95, readMax, readP95Band, openCold, cfg.openIters, openMin, openP50, openP95, openMax, openP95Band, ciNote, shapeNote)
	t.Logf("load harness: warmHook=%s serial runs=%d min=%s median=%s max=%s | burst workers=%d window=%s probeRuns=%d min=%s p50=%s p95=%s max=%s queued=%.2f faultsPerSecond=%.3f band(p95=%s,queued<=%.1f,faults=0)",
		warmHook, len(serialLatencies), serialMin, serialMedian, serialMax, cfg.workers, burstWindow.Round(time.Millisecond), len(probeLatencies), probeMin, probeP50, probeP95, probeMax, queued, burstFaultsPerSecond, hookP95Band, loadtestBurstQueueBound)
	t.Logf("load harness: sustained workers=%d window=%s offered=%d committed=%d eventsPerSecond=%.1f floor=%.0f sustainedFaults=%d",
		cfg.workers, sustainedWindow.Round(time.Millisecond), totalOffered, committed, eventsPerSecond, throughputFloor, sustainedFaults)
	t.Logf("load harness: direct single probe %s in %s", directNote, directElapsed.Round(time.Millisecond))
}

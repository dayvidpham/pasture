//go:build recovery

// Package engine_test — recovery_test.go
//
// The PERMANENT two-process kill-9 recovery test. It is build-tagged `recovery`
// so it never runs in the normal suite (it forks subprocesses and SIGKILLs
// them); run it explicitly:
//
//	CGO_ENABLED=0 go test -tags recovery ./internal/engine/ -run Recovery -v
//
// It graduates the de-risk spike into a regression test that exercises the REAL
// shipped derivation: the dedup key is protocol.DedupKey(...), not a raw-tuple
// shortcut, so an epoch-drop bug in the hashing path would surface here.
package engine_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// moduleRoot returns the directory containing go.mod for this module.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Fatal("not inside a Go module")
	}
	return filepath.Dir(gomod)
}

// buildProbe compiles the recovery probe with CGO_ENABLED=0 and the given
// -ldflags, returning the output binary path. Distinct ldflags change the
// binary hash (the recompiled-binary tier).
func buildProbe(t *testing.T, root, ldflags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "probe")
	args := []string{"build", "-tags", "recovery"}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", bin, "./cmd/pasture-recovery-probe")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return bin
}

func buildPastureCLI(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pasture")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/pasture")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build pasture CLI: %v\n%s", err, out)
	}
	return bin
}

func armProbeReadySignal(t *testing.T) chan os.Signal {
	t.Helper()
	ready := make(chan os.Signal, 1)
	signal.Notify(ready, syscall.SIGUSR1)
	t.Cleanup(func() {
		signal.Stop(ready)
	})
	return ready
}

func waitForProbeReadySignal(t *testing.T, ready chan os.Signal, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ready:
		return
	case <-timer.C:
		t.Fatal("victim never signalled readiness within timeout")
	}
}

// killCycle runs the victim (which stalls mid-step and is SIGKILLed once it
// signals readiness) then the resumer (which recovers and completes). The two
// may be different binaries to exercise the recompiled-binary tier. dbPath is
// shared between them.
func killCycle(t *testing.T, victimBin, resumerBin, dbPath, wfID string) {
	t.Helper()
	ready := armProbeReadySignal(t)

	victim := exec.Command(victimBin)
	victim.Env = append(os.Environ(),
		"PROBE_DB="+dbPath,
		"PROBE_WFID="+wfID,
		"PROBE_STALL=120",
	)
	victim.Stderr = os.Stderr
	if err := victim.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}

	waitForProbeReadySignal(t, ready, 60*time.Second)

	// kill -9 the victim mid-step (after the side-effect write, before return).
	if err := victim.Process.Kill(); err != nil {
		t.Fatalf("kill victim: %v", err)
	}
	_ = victim.Wait() // reaps the killed process

	// Resume: a fresh process recovers the in-flight workflow and completes it.
	resumer := exec.Command(resumerBin)
	resumer.Env = append(os.Environ(),
		"PROBE_DB="+dbPath,
		"PROBE_WFID="+wfID,
		"PROBE_STALL=0",
	)
	out, err := resumer.CombinedOutput()
	if err != nil {
		t.Fatalf("resumer failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "COMPLETE propose") {
		t.Fatalf("resumer did not complete the recovered epoch; output:\n%s", out)
	}
}

func queuedSliceRecoveryCycle(t *testing.T, victimBin, resumerBin, dbPath, wfID string) {
	t.Helper()
	ready := armProbeReadySignal(t)

	victim := exec.Command(victimBin)
	victim.Env = append(os.Environ(),
		"PROBE_MODE=queue",
		"PROBE_DB="+dbPath,
		"PROBE_WFID="+wfID,
		"PROBE_STALL=120",
	)
	victim.Stderr = os.Stderr
	if err := victim.Start(); err != nil {
		t.Fatalf("start queue victim: %v", err)
	}

	waitForProbeReadySignal(t, ready, 60*time.Second)

	if err := victim.Process.Kill(); err != nil {
		t.Fatalf("kill queue victim: %v", err)
	}
	_ = victim.Wait()

	resumer := exec.Command(resumerBin)
	resumer.Env = append(os.Environ(),
		"PROBE_MODE=queue",
		"PROBE_DB="+dbPath,
		"PROBE_WFID="+wfID,
		"PROBE_STALL=0",
	)
	out, err := resumer.CombinedOutput()
	if err != nil {
		t.Fatalf("queue resumer failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "QUEUE COMPLETE") {
		t.Fatalf("queue resumer did not complete recovered queued slices; output:\n%s", out)
	}
}

// phaseRowCounts returns the count of engine-emitted (dedup_key NOT NULL)
// audit_events grouped by phase.
func phaseRowCounts(t *testing.T, dbPath string) map[string]int {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(
		`SELECT phase, COUNT(*) FROM audit_events WHERE dedup_key IS NOT NULL GROUP BY phase`)
	if err != nil {
		t.Fatalf("query phase counts: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var phase string
		var n int
		if err := rows.Scan(&phase, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		counts[phase] = n
	}
	return counts
}

// activityCount returns the total number of PROV-O activity rows in the file.
func activityCount(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activities`).Scan(&n); err != nil {
		t.Fatalf("count activities: %v", err)
	}
	return n
}

// assertExactlyOnce checks that each completed transition produced exactly one
// engine-emitted row in BOTH forensic tiers despite the killed step replaying on
// resume — audit_events (via dedup_key) and activities (via ON CONFLICT(id)).
// This completes the "both tables" exactly-once recovery guarantee (the
// activities half was deferred from the earlier audit-only recovery test).
func assertExactlyOnce(t *testing.T, dbPath string) {
	t.Helper()
	counts := phaseRowCounts(t, dbPath)
	if counts["elicit"] != 1 {
		t.Errorf("elicit audit row count = %d, want 1", counts["elicit"])
	}
	if counts["propose"] != 1 {
		t.Errorf("propose audit row count = %d, want 1 (the killed+replayed step must not duplicate)", counts["propose"])
	}
	// Activities tier: the epoch drove 2 transitions (elicit, propose), each
	// recording one activity; the killed propose step replayed on resume but the
	// deterministic id collapsed it, so the total is exactly 2.
	if got := activityCount(t, dbPath); got != 2 {
		t.Errorf("activities count = %d, want 2 (one per transition; replay must not duplicate)", got)
	}
}

func assertCLIStatusPhase(t *testing.T, pastureBin, dbPath, epochId string, want protocol.PhaseId) {
	t.Helper()
	cmd := exec.Command(pastureBin, "--db", dbPath, "--format", "json", "status", "--epoch-id", epochId)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pasture status failed: %v\n%s", err, out)
	}
	var status struct {
		CurrentPhase string `json:"currentPhase"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("decode pasture status json: %v\nbody:\n%s", err, out)
	}
	if status.CurrentPhase != string(want) {
		t.Fatalf("status currentPhase = %q, want %q", status.CurrentPhase, want)
	}
}

// TestRecovery_SameBinaryResume: kill -9 mid-step, resume with the SAME binary.
func TestRecovery_SameBinaryResume(t *testing.T) {
	root := moduleRoot(t)
	bin := buildProbe(t, root, "")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	killCycle(t, bin, bin, dbPath, "epoch-recover-same")
	assertExactlyOnce(t, dbPath)
}

// TestRecovery_RecompiledBinaryResume: resume with a REBUILT binary whose hash
// differs (getBinaryHash changes) but whose pinned ApplicationVersion matches,
// so DBOS recovery still fires.
func TestRecovery_RecompiledBinaryResume(t *testing.T) {
	root := moduleRoot(t)
	victimBin := buildProbe(t, root, "-X main.buildStamp=victim")
	resumerBin := buildProbe(t, root, "-X main.buildStamp=resumer-rebuilt")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	killCycle(t, victimBin, resumerBin, dbPath, "epoch-recover-recompiled")
	assertExactlyOnce(t, dbPath)
}

// TestRecovery_LegacyNullCoexistence: the dedup partial index must not reject
// the ordinary (NULL dedup_key) write path even on a database the engine has
// written deterministic keys into.
func TestRecovery_LegacyNullCoexistence(t *testing.T) {
	root := moduleRoot(t)
	bin := buildProbe(t, root, "")
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	killCycle(t, bin, bin, dbPath, "epoch-recover-legacy")
	assertExactlyOnce(t, dbPath)

	// Now write two legacy (no dedup_key) events through the ordinary trail.
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("open trail: %v", err)
	}
	defer trail.Close()
	for i := 0; i < 2; i++ {
		if err := trail.RecordEvent(t.Context(), protocol.AuditEvent{
			EpochId:   "epoch-recover-legacy",
			Phase:     protocol.PhaseLanding,
			Role:      "supervisor",
			EventType: protocol.EventPhaseTransition,
			Payload:   map[string]any{"i": i},
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("legacy RecordEvent %d: %v", i, err)
		}
	}

	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var nullCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE dedup_key IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count NULL rows: %v", err)
	}
	if nullCount != 2 {
		t.Errorf("legacy NULL-keyed row count = %d, want 2 (partial index must allow multiple NULLs)", nullCount)
	}
}

func TestRecovery_QueuedSliceSurvivesDaemonRestart(t *testing.T) {
	root := moduleRoot(t)
	bin := buildProbe(t, root, "")
	pastureBin := buildPastureCLI(t, root)
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	epochId := "epoch-recover-queued-slice"

	queuedSliceRecoveryCycle(t, bin, bin, dbPath, epochId)
	assertCLIStatusPhase(t, pastureBin, dbPath, epochId, protocol.PhasePropose)
}

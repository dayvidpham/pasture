// loadtest_guard_test.go pins the harness's live-store refusal and its knob
// defaults. These run fast and need no binaries, daemon, or store, so they
// are NOT skipped under -short: the refusal proof holds on every run.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheHarnessRefusesAnythingOutsideItsSandbox is the refusal proof for
// the epic's "no measurement against the live store" acceptance: the pure
// verdict behind requireDisposableDB rejects a path outside the sandbox, a
// path that IS the live store, and accepts a path inside the sandbox.
func TestTheHarnessRefusesAnythingOutsideItsSandbox(t *testing.T) {
	sandbox := t.TempDir()

	if err := disposableDBError(filepath.Join(sandbox, "loadtest.db"), sandbox); err != nil {
		t.Errorf("a store inside the sandbox must be accepted, got: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.db")
	if err := disposableDBError(outside, sandbox); err == nil {
		t.Errorf("a store outside the sandbox must be refused, %q was accepted", outside)
	} else if !strings.Contains(err.Error(), "not under the disposable sandbox") {
		t.Errorf("the refusal must name the sandbox escape, got: %v", err)
	}

	live := liveStorePath()
	if live == "" {
		t.Fatal("the live store path resolved empty; the refusal would pass vacuously")
	}
	// The live path is refused even when it is handed in as the sandbox's
	// own content: sandbox membership never overrides the live-store veto.
	if err := disposableDBError(live, filepath.Dir(live)); err == nil {
		t.Errorf("the live store %q must be refused even from its own directory", live)
	} else if !strings.Contains(err.Error(), "live store") {
		t.Errorf("the refusal must name the live store, got: %v", err)
	}
}

// TestTheHarnessKnobsDefaultToTheDocumentedShape pins the env-knob defaults
// the script documents, so a default change is a deliberate edit here, not
// a silent rescale of the harness.
func TestTheHarnessKnobsDefaultToTheDocumentedShape(t *testing.T) {
	for _, key := range []string{
		"PASTURE_LOADTEST_SOURCE", "PASTURE_LOADTEST_TASKS", "PASTURE_LOADTEST_WORKERS",
		"PASTURE_LOADTEST_BURST_SECS", "PASTURE_LOADTEST_THROUGHPUT_SECS",
		"PASTURE_LOADTEST_READ_ITERS", "PASTURE_LOADTEST_OPEN_ITERS",
		"PASTURE_LOADTEST_REPORT_ONLY", "PASTURE_LOADTEST_FORCE_SYNTHETIC",
	} {
		t.Setenv(key, "")
	}
	cfg := loadtestConfigFromEnv(t)
	if cfg.source != "" || cfg.reportOnly {
		t.Errorf("string/bool knobs must default empty/false, got %+v", cfg)
	}
	if cfg.syntheticTasks != 600 || cfg.workers != 18 || cfg.readIters != 30 || cfg.openIters != 20 {
		t.Errorf("count knobs must default to the documented shape, got %+v", cfg)
	}
	if cfg.burstWindow.Seconds() != 3 || cfg.sustainedRun.Seconds() != 10 {
		t.Errorf("window knobs must default to the documented shape, got %+v", cfg)
	}
}

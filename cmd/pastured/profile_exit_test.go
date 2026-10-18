package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The early-success command must flush the same profiling session as an
// ordinary return. A successful process exit alone does not prove cleanup.
func TestEarlySuccessFlushesWriteProfiles(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pastured")
	buildPasturedBinary(t, binary, "")
	for _, mode := range []string{"stages", "full"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "profile")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--version")
			cmd.Env = append(cmd.Environ(), "PASTURE_WRITE_PROFILE_DIR="+dir, "PASTURE_WRITE_PROFILE_MODE="+mode)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("early-success subprocess: %v\n%s", err, output)
			}
			data, err := os.ReadFile(filepath.Join(dir, "stages.json"))
			if err != nil {
				t.Fatalf("successful exit did not flush stage artifacts: %v", err)
			}
			var stages struct {
				Elapsed int64 `json:"elapsed_ns"`
			}
			if err := json.Unmarshal(data, &stages); err != nil || stages.Elapsed <= 0 {
				t.Fatalf("invalid completed stage profile %q: %v", data, err)
			}
			if mode == "full" {
				for _, name := range []string{"cpu.pprof", "trace.out", "heap.pprof", "allocs.pprof", "goroutine.pprof", "mutex.pprof", "block.pprof", "goroutines.txt"} {
					info, err := os.Stat(filepath.Join(dir, name))
					if err != nil {
						t.Fatalf("full profile missing %s after exit: %v", name, err)
					}
					if info.Size() == 0 {
						t.Errorf("full profile %s was not flushed", name)
					}
				}
				for _, args := range [][]string{{"tool", "pprof", "-top", filepath.Join(dir, "cpu.pprof")}, {"tool", "trace", "-d=parsed", filepath.Join(dir, "trace.out")}} {
					if output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
						t.Fatalf("read completed runtime profile: %v\n%s", err, output)
					}
				}
			}
		})
	}
}

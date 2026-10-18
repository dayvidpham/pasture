package writeprofile

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOptInAndExclusiveDestination(t *testing.T) {
	t.Setenv("PASTURE_WRITE_PROFILE_DIR", "")
	if err := StartEnv(); err != nil {
		t.Fatal(err)
	}
	Region("disabled")()
	Stop()
	if active.Load() != nil {
		t.Fatal("disabled profiler became active")
	}
	dir := filepath.Join(t.TempDir(), "profile")
	t.Setenv("PASTURE_WRITE_PROFILE_DIR", dir)
	t.Setenv("PASTURE_WRITE_PROFILE_MODE", "typo")
	if err := StartEnv(); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("bad mode created output")
	}
	t.Setenv("PASTURE_WRITE_PROFILE_MODE", "stages")
	if err := StartEnv(); err != nil {
		t.Fatal(err)
	}
	Region("known.stage")()
	Stop()
	Stop()
	if err := StartEnv(); err == nil {
		Stop()
		t.Fatal("existing destination accepted")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "stages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Events []event `json:"events"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].Stage != "known.stage" || result.Events[0].Nanoseconds < 0 {
		t.Fatalf("bad stage accounting: %s", raw)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("stages-only enabled runtime profiling")
	}
}

func TestFullArtifactsAndCrossGoroutineTimer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	t.Setenv("PASTURE_WRITE_PROFILE_DIR", dir)
	t.Setenv("PASTURE_WRITE_PROFILE_MODE", "full")
	if err := StartEnv(); err != nil {
		t.Fatal(err)
	}
	defer Stop()
	done := Timer("queue.wait")
	joined := make(chan struct{})
	go func() { done(); close(joined) }()
	<-joined
	end := Region("cpu.work")
	deadline := time.Now().Add(40 * time.Millisecond)
	for time.Now().Before(deadline) {
	}
	end()
	Stop()
	for _, name := range []string{"cpu", "heap", "allocs", "goroutine", "mutex", "block"} {
		f, err := os.Open(filepath.Join(dir, name+".pprof"))
		if err != nil {
			t.Fatal(err)
		}
		z, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		raw, err := io.ReadAll(z)
		_ = z.Close()
		_ = f.Close()
		if err != nil || len(raw) == 0 {
			t.Fatalf("invalid %s profile: %v", name, err)
		}
	}
	trace, err := os.ReadFile(filepath.Join(dir, "trace.out"))
	if err != nil || len(trace) < 16 || string(trace[:3]) != "go " {
		t.Fatalf("invalid trace: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("profile directory is not private")
	}
	if active.Load() != nil {
		t.Fatal("profiler remained active after Stop")
	}
}

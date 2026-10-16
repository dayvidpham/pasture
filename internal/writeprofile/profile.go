// Package writeprofile provides explicitly opted-in, local developer diagnostics.
// Stage names must be constants: payloads and identities never belong in profiles.
package writeprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sync"
	"sync/atomic"
	"time"
)

type event struct {
	Stage       string `json:"stage"`
	Nanoseconds int64  `json:"nanoseconds"`
}

type session struct {
	mu           sync.Mutex
	events       []event
	dir          string
	full         bool
	cpu, tracing *os.File
	started      time.Time
}

var active atomic.Pointer[session]

// StartEnv requires a new directory and an explicit mode. Normal invocations
// neither allocate artifacts nor enable runtime sampling. The harness supplies
// private destinations; this is not a security sandbox for untrusted commands.
func StartEnv() error {
	dir := os.Getenv("PASTURE_WRITE_PROFILE_DIR")
	if dir == "" {
		return nil
	}
	mode := os.Getenv("PASTURE_WRITE_PROFILE_MODE")
	if mode != "stages" && mode != "full" {
		return fmt.Errorf("write profiling startup: set PASTURE_WRITE_PROFILE_MODE to stages or full before requesting artifacts")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("write profiling startup: artifact directory must be absolute; choose a new private directory")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("write profiling startup: exclusively create artifact directory: %w; choose a new destination", err)
	}
	s := &session{dir: dir, full: mode == "full", started: time.Now()}
	if s.full {
		var err error
		s.cpu, err = os.Create(filepath.Join(dir, "cpu.pprof"))
		if err != nil {
			return err
		}
		if err = pprof.StartCPUProfile(s.cpu); err != nil {
			_ = s.cpu.Close()
			return err
		}
		s.tracing, err = os.Create(filepath.Join(dir, "trace.out"))
		if err != nil {
			pprof.StopCPUProfile()
			_ = s.cpu.Close()
			return err
		}
		if err = trace.Start(s.tracing); err != nil {
			pprof.StopCPUProfile()
			_ = s.cpu.Close()
			_ = s.tracing.Close()
			return err
		}
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)
	}
	active.Store(s)
	return nil
}

// Region measures a wall region, including waits. Nested/concurrent regions
// overlap and MUST NOT be added as if they partition process elapsed time.
func Region(name string) func() {
	return timer(name, true)
}

// Timer measures across goroutines without creating an invalid trace region.
func Timer(name string) func() { return timer(name, false) }

func timer(name string, tracing bool) func() {
	s := active.Load()
	if s == nil {
		return func() {}
	}
	start := time.Now()
	var r *trace.Region
	if tracing {
		r = trace.StartRegion(context.Background(), name)
	}
	return func() {
		if r != nil {
			r.End()
		}
		s.mu.Lock()
		s.events = append(s.events, event{Stage: name, Nanoseconds: time.Since(start).Nanoseconds()})
		s.mu.Unlock()
	}
}

// Stop is idempotent and must run before os.Exit as well as normal return.
func Stop() {
	s := active.Swap(nil)
	if s == nil {
		return
	}
	if s.full {
		trace.Stop()
		_ = s.tracing.Close()
		pprof.StopCPUProfile()
		_ = s.cpu.Close()
		for _, name := range []string{"heap", "allocs", "goroutine", "mutex", "block"} {
			if f, err := os.Create(filepath.Join(s.dir, name+".pprof")); err == nil {
				_ = pprof.Lookup(name).WriteTo(f, 0)
				_ = f.Close()
			}
		}
		if f, err := os.Create(filepath.Join(s.dir, "goroutines.txt")); err == nil {
			_ = pprof.Lookup("goroutine").WriteTo(f, 2)
			_ = f.Close()
		}
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Create(filepath.Join(s.dir, "stages.json"))
	if err == nil {
		_ = json.NewEncoder(f).Encode(struct {
			Elapsed int64   `json:"elapsed_ns"`
			Events  []event `json:"events"`
		}{time.Since(s.started).Nanoseconds(), s.events})
		_ = f.Close()
	}
}

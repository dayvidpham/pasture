package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/types"
)

// SetEnv sets an environment variable for the duration of the test and
// restores the previous value during cleanup.
func SetEnv(t *testing.T, key, value string) {
	t.Helper()

	oldValue, hadValue := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}

	t.Cleanup(func() {
		if hadValue {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// UnsetEnv removes an environment variable for the duration of the test and
// restores the previous value during cleanup.
func UnsetEnv(t *testing.T, key string) {
	t.Helper()

	oldValue, hadValue := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}

	t.Cleanup(func() {
		if hadValue {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// resolveGoEnvVar returns the effective value of a Go environment variable
// by running "go env <name>". Variables such as GOCACHE and GOPATH have
// computed defaults that depend on HOME; this resolves them through the Go
// toolchain before HOME is redirected, so subprocess go builds can find the
// warm shared cache instead of resolving defaults against the throwaway
// hermetic HOME.
func resolveGoEnvVar(name string) (string, error) {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "", fmt.Errorf(
			"testutil.SetHermeticEnv: resolve effective %s: 'go env %s' failed: %w"+
				" — ensure 'go' is in PATH before running tests",
			name, name, err,
		)
	}
	return strings.TrimSpace(string(out)), nil
}

// restoreEnvVar restores a single environment variable to its pre-capture state.
func restoreEnvVar(key, oldValue string, hadValue bool) {
	if hadValue {
		_ = os.Setenv(key, oldValue)
	} else {
		_ = os.Unsetenv(key)
	}
}

// DBPathEnv names the variable that overrides where pasture's unified store
// lives; CaptureDirEnv names the variable that turns host-payload capture on
// and points it at a directory; NamespaceEnv names the default namespace for
// --namespace and the scope of task list/ready/blocked. DBPathEnv and
// CaptureDirEnv are declared here, rather than imported, because their owners
// (internal/tasks/paths.go for the store, cmd/pasture's hook_environment.go for
// the capture directory) sit in packages this leaf cannot cheaply import.
// NamespaceEnv aliases internal/types.NamespaceEnv instead, so the production
// CLI can share one spelling without importing this test-only package.
//
// WHY THEY ARE NEUTRALIZED, AND WHY THE REST OF THE ENVIRONMENT IS NOT. HOME
// and XDG_DATA_HOME point AT the sandbox; these three point AWAY from it, and the
// store override outranks both of them. tasks.DefaultDBPath resolves
// PASTURE_DB_PATH first and only then consults XDG_DATA_HOME and HOME, so a
// developer or CI shell that exports PASTURE_DB_PATH defeats the redirect
// entirely: every test that falls back to the default store resolves the
// exported one, and because the lifecycle hook derives its fault record from
// the store path it would use, the same variable decides where
// lifecycle-faults.jsonl is appended. That is the route by which a test run
// appended to the operator's live store and live fault record. An exported
// PASTURE_NAMESPACE is milder but still changes what a test observes: it scopes
// list/ready/blocked and names newly created tasks and authors, so a test that
// omits --namespace silently reads or writes a different namespace.
const (
	// DBPathEnv is the store override (internal/tasks.DBPathEnv).
	DBPathEnv = "PASTURE_DB_PATH"
	// CaptureDirEnv is the payload-capture directory override
	// (cmd/pasture's hookCaptureDirEnv, internal/handlers' captureDirectoryEnv).
	CaptureDirEnv = "PASTURE_CAPTURE_DIR"
	// NamespaceEnv is the default namespace for --namespace and the scope of
	// task list/ready/blocked. It aliases the production-neutral constant in
	// internal/types so the CLI need not import this test-only package.
	NamespaceEnv = types.NamespaceEnv
)

// SetHermeticEnv points HOME and XDG_DATA_HOME at a temporary directory tree
// for a package-level test run, and removes the three variables that would
// point durable writes or task-read scope back outside it.
//
// Before redirecting HOME it resolves and pins GOCACHE and GOPATH to their
// effective values (via "go env"). This prevents subprocess go builds inside
// audit crash tests and cmd/pasture TestMain from resolving GOCACHE/GOPATH
// through the redirected (throwaway) HOME, which would produce a cold
// build-cache and module-cache hit on every run. GOCACHE and GOPATH are
// typically unset in the Nix dev shell, so os.Getenv("GOCACHE") is a no-op;
// only the toolchain's own resolution gives the real paths.
//
// IT ALSO UNSETS PASTURE_DB_PATH, PASTURE_CAPTURE_DIR AND PASTURE_NAMESPACE,
// WHICH IS NOT COSMETIC. tasks.DefaultDBPath reads PASTURE_DB_PATH BEFORE
// XDG_DATA_HOME and HOME, so redirecting HOME alone leaves a test process that
// inherited the operator's exported store path resolving that store — and the
// lifecycle fault record beside it — for every test that does not name its own
// --db. The capture override is the same shape one step milder: it copies host
// payloads into a directory the operator chose. PASTURE_NAMESPACE scopes task
// list/ready/blocked and names new tasks/authors, so an inherited value makes a
// test read or write a namespace it never asked for. All are restored by the
// returned cleanup, so a caller that wants to exercise them can still set them
// after this returns.
//
// Sharing the content-addressed build and module caches does not weaken test
// isolation: per-test SQLite databases remain isolated via --db flags and
// t.TempDir(); only HOME, XDG_* dirs and those three overrides are touched for
// hermeticity.
//
// The returned cleanup function restores all seven variables (GOCACHE, GOPATH,
// HOME, XDG_DATA_HOME, DBPathEnv, CaptureDirEnv, NamespaceEnv) to their
// original state and removes the temp dir.
func SetHermeticEnv(prefix string) (func(), error) {
	// Resolve effective GOCACHE and GOPATH BEFORE HOME changes.
	// os.Getenv("GOCACHE") / os.Getenv("GOPATH") are unset in the Nix dev
	// shell, so copying them directly is a no-op. The Go toolchain computes
	// them from the current HOME; we must capture these values now.
	effectiveGOCACHE, err := resolveGoEnvVar("GOCACHE")
	if err != nil {
		return nil, err
	}
	effectiveGOPATH, err := resolveGoEnvVar("GOPATH")
	if err != nil {
		return nil, err
	}

	// Snapshot the current env values for restoration in the cleanup function.
	oldGOCACHE, hadGOCACHE := os.LookupEnv("GOCACHE")
	oldGOPATH, hadGOPATH := os.LookupEnv("GOPATH")

	// Pin GOCACHE and GOPATH explicitly before redirecting HOME so that any
	// subprocess go build finds the warm shared cache, not a cold one derived
	// from the hermetic temp HOME.
	if effectiveGOCACHE != "" {
		if err := os.Setenv("GOCACHE", effectiveGOCACHE); err != nil {
			return nil, fmt.Errorf(
				"testutil.SetHermeticEnv: pin GOCACHE=%q: %w"+
					" — this is required so subprocess go builds reuse the warm cache",
				effectiveGOCACHE, err,
			)
		}
	}
	if effectiveGOPATH != "" {
		if err := os.Setenv("GOPATH", effectiveGOPATH); err != nil {
			restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
			return nil, fmt.Errorf(
				"testutil.SetHermeticEnv: pin GOPATH=%q: %w"+
					" — this is required so subprocess go builds find the module cache",
				effectiveGOPATH, err,
			)
		}
	}

	// Create the hermetic temp dir tree.
	dir, err := os.MkdirTemp("", prefix+"-*")
	if err != nil {
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		return nil, fmt.Errorf("testutil.SetHermeticEnv: create temp dir: %w", err)
	}

	oldHome, hadHome := os.LookupEnv("HOME")
	oldXDG, hadXDG := os.LookupEnv("XDG_DATA_HOME")
	oldDBPath, hadDBPath := os.LookupEnv(DBPathEnv)
	oldCapture, hadCapture := os.LookupEnv(CaptureDirEnv)
	oldNamespace, hadNamespace := os.LookupEnv(NamespaceEnv)
	home := filepath.Join(dir, "home")
	xdg := filepath.Join(dir, "xdg")

	if err := os.MkdirAll(home, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		return nil, fmt.Errorf("testutil.SetHermeticEnv: create hermetic HOME dir %q: %w", home, err)
	}
	if err := os.MkdirAll(xdg, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		return nil, fmt.Errorf("testutil.SetHermeticEnv: create hermetic XDG dir %q: %w", xdg, err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		_ = os.RemoveAll(dir)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		return nil, fmt.Errorf("testutil.SetHermeticEnv: set HOME=%q: %w", home, err)
	}
	if err := os.Setenv("XDG_DATA_HOME", xdg); err != nil {
		_ = os.RemoveAll(dir)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		restoreEnvVar("HOME", oldHome, hadHome)
		return nil, fmt.Errorf("testutil.SetHermeticEnv: set XDG_DATA_HOME=%q: %w", xdg, err)
	}
	// The three overrides point AWAY from the sandbox and outrank its paths, or
	// silently change which namespace a read sees, so they are removed rather
	// than redirected: a test process must not inherit the operator's store,
	// capture directory, or namespace at all. See the doc comment.
	for _, override := range []string{DBPathEnv, CaptureDirEnv, NamespaceEnv} {
		if err := os.Unsetenv(override); err != nil {
			_ = os.RemoveAll(dir)
			restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
			restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
			restoreEnvVar("HOME", oldHome, hadHome)
			restoreEnvVar("XDG_DATA_HOME", oldXDG, hadXDG)
			return nil, fmt.Errorf("testutil.SetHermeticEnv: unset %s: %w"+
				" — a test run must not inherit the operator's store, capture directory, or task namespace", override, err)
		}
	}

	return func() {
		restoreEnvVar("HOME", oldHome, hadHome)
		restoreEnvVar("XDG_DATA_HOME", oldXDG, hadXDG)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		restoreEnvVar(DBPathEnv, oldDBPath, hadDBPath)
		restoreEnvVar(CaptureDirEnv, oldCapture, hadCapture)
		restoreEnvVar(NamespaceEnv, oldNamespace, hadNamespace)
		_ = os.RemoveAll(dir)
	}, nil
}

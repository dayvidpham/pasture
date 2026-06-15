package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// SetHermeticEnv points HOME and XDG_DATA_HOME at a temporary directory tree
// for a package-level test run.
//
// Before redirecting HOME it resolves and pins GOCACHE and GOPATH to their
// effective values (via "go env"). This prevents subprocess go builds inside
// audit crash tests and cmd/pasture TestMain from resolving GOCACHE/GOPATH
// through the redirected (throwaway) HOME, which would produce a cold
// build-cache and module-cache hit on every run. GOCACHE and GOPATH are
// typically unset in the Nix dev shell, so os.Getenv("GOCACHE") is a no-op;
// only the toolchain's own resolution gives the real paths.
//
// Sharing the content-addressed build and module caches does not weaken test
// isolation: per-test SQLite databases remain isolated via --db flags and
// t.TempDir(); only HOME and XDG_* dirs are redirected for hermeticity.
//
// The returned cleanup function restores all four variables (GOCACHE, GOPATH,
// HOME, XDG_DATA_HOME) to their original state and removes the temp dir.
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

	return func() {
		restoreEnvVar("HOME", oldHome, hadHome)
		restoreEnvVar("XDG_DATA_HOME", oldXDG, hadXDG)
		restoreEnvVar("GOCACHE", oldGOCACHE, hadGOCACHE)
		restoreEnvVar("GOPATH", oldGOPATH, hadGOPATH)
		_ = os.RemoveAll(dir)
	}, nil
}

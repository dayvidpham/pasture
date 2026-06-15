package testutil

import (
	"fmt"
	"os"
	"path/filepath"
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

// SetHermeticEnv points HOME and XDG_DATA_HOME at a temporary directory tree
// for a package-level test run.
func SetHermeticEnv(prefix string) (func(), error) {
	dir, err := os.MkdirTemp("", prefix+"-*")
	if err != nil {
		return nil, err
	}
	oldHome, hadHome := os.LookupEnv("HOME")
	oldXDG, hadXDG := os.LookupEnv("XDG_DATA_HOME")
	home := filepath.Join(dir, "home")
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(home, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.MkdirAll(xdg, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Setenv("HOME", home); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("set HOME: %w", err)
	}
	if err := os.Setenv("XDG_DATA_HOME", xdg); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("set XDG_DATA_HOME: %w", err)
	}
	return func() {
		if hadHome {
			_ = os.Setenv("HOME", oldHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadXDG {
			_ = os.Setenv("XDG_DATA_HOME", oldXDG)
		} else {
			_ = os.Unsetenv("XDG_DATA_HOME")
		}
		_ = os.RemoveAll(dir)
	}, nil
}

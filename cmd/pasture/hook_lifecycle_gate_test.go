package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// TestInvalidInvocationCreatesNoDatabaseFile is the M1 §8 preservation pin for
// the write-gate slice: "an invalid invocation creates no database file." The
// warrant now threads through Receive, but it is built only on the
// admitted-dispatch path AFTER the store is opened, so it must not perturb the
// pre-store ordering that rejects an invalid invocation before any storage
// access. An unknown-harness invocation is refused at dispatch — before the
// activation gate, before the store open, and before any gate warrant is
// constructed — so it exercises exactly the path the warrant threading must
// leave untouched. It runs the built production CLI so the real process
// boundary (exit code, stdout, stderr, on-disk files) is observed.
//
// Green by construction from L1 (the pre-store ordering is unchanged); it fails
// only if a future change moves the store open ahead of an invalid-invocation
// refusal.
func TestInvalidInvocationCreatesNoDatabaseFile(t *testing.T) {
	binary := lifecycleBinary(t)

	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "not-a-harness", "--event", "SessionStart", "--host-version", "1.0.0")
	command.Stdin = bytes.NewReader([]byte(`{"hook_event_name":"SessionStart"}`))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "an invalid invocation must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "an invalid invocation must emit no native continuation on stdout")
	require.NotEmpty(t, stderr.String(), "an invalid invocation must report an actionable diagnostic on stderr")
	require.Contains(t, stderr.String(), "not supported")

	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "M1 §8: an invalid invocation must create no database file")
	for _, suffix := range []string{"-wal", "-shm"} {
		_, sidecarErr := os.Stat(dbPath + suffix)
		require.ErrorIs(t, sidecarErr, os.ErrNotExist, "M1 §8: an invalid invocation must leave no %s sidecar", suffix)
	}
}

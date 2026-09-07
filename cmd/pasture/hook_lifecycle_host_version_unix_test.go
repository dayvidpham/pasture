//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLifecycleVersionQueryReapsCancelledExecutable(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "pid")
	executable := versionExecutable(t, "printf '%s' \"$$\" > '"+pidFile+"'\nexec sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := queryLifecycleHostVersion(ctx, executable)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the explicit executable must have started before cancellation")
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	require.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH, "the direct executable was killed and reaped, not left as a zombie")
}

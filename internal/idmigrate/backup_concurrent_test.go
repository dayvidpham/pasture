package idmigrate

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestConcurrentArchiveStoreWithSameStampNeverSharesStaging pins the
// two-daemon archive race at the unit level: concurrent archivers stamping
// the same second (two pastured processes starting together compute the same
// UTC-second stamp) must never share a staging file and must never publish
// over each other's verified archive.
//
// Under the old `<final>.partial` staging, every archiver with the same stamp
// names the same staging file: one process's clear/rename deletes or moves
// another's bytes mid-flight, so a loser fails with "couldn't flush the
// staged archive" (ENOENT on its own staging file) instead of an accepted
// safe loss — the CI failure this pins. With per-attempt staging plus
// publish-if-absent finals, every archiver succeeds independently.
//
// The barrier start plus several rounds force the overlap; the assertion is
// that ALL attempts succeed with DISTINCT verified finals and no staging
// litter left behind.
func TestConcurrentArchiveStoreWithSameStampNeverSharesStaging(t *testing.T) {
	t.Parallel()
	path, db := markerlessStore(t)
	_ = db
	backupDir := t.TempDir()
	stamp := time.Date(2026, 10, 15, 16, 38, 27, 0, time.UTC)

	const goroutines = 8
	const rounds = 3
	type outcome struct {
		archive Archive
		err     error
	}
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]outcome, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				archive, err := ArchiveStore(context.Background(), path, backupDir, stamp)
				results[i] = outcome{archive: archive, err: err}
			}(i)
		}
		close(start)
		wg.Wait()
		for i, result := range results {
			require.NoError(t, result.err, "round %d archiver %d must succeed with its own staging and final", round, i)
			require.NotEmpty(t, result.archive.Path, "round %d archiver %d must publish a final", round, i)
		}
		seen := make(map[string]struct{}, goroutines)
		for _, result := range results {
			canonical, err := filepath.EvalSymlinks(result.archive.Path)
			require.NoError(t, err)
			if _, duplicate := seen[canonical]; duplicate {
				t.Fatalf("round %d published the same final archive twice: %q", round, canonical)
			}
			seen[canonical] = struct{}{}
			info, err := os.Stat(canonical)
			require.NoError(t, err, "round %d published final must exist", round)
			require.Greater(t, info.Size(), int64(0), "round %d published final must be non-empty", round)
		}
	}
	entries, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".partial", "no staging file may survive successful archives, got %q", entry.Name())
		require.Contains(t, entry.Name(), ".id-backup-", "unexpected file in the backup dir: %q", entry.Name())
	}
	require.Len(t, entries, goroutines*rounds, "every attempt must publish its own final archive")
}

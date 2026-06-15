package testutil

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	_ "modernc.org/sqlite"
)

var goldenUnifiedDB struct {
	once sync.Once
	path string
	err  error
}

// GoldenUnifiedDBPath returns a per-test copy of a pre-migrated unified
// pasture.db. The golden source is built once per test binary through the real
// production opener, then copied byte-for-byte for each test that opts in.
func GoldenUnifiedDBPath(t *testing.T) string {
	t.Helper()
	src := goldenUnifiedDBSource(t)
	dst := filepath.Join(t.TempDir(), "pasture.db")
	if err := copyFile(dst, src); err != nil {
		t.Fatalf("copy golden pasture.db: %v", err)
	}
	return dst
}

// OpenGoldenTaskTracker opens a copied golden database with migrations
// explicitly disabled. Migration tests should not use this helper.
func OpenGoldenTaskTracker(t *testing.T) (protocol.TaskTracker, string) {
	t.Helper()
	dbPath := GoldenUnifiedDBPath(t)
	tracker, err := tasks.OpenTaskTrackerWithOptions(dbPath, tasks.WithSkipMigrations())
	if err != nil {
		t.Fatalf("OpenTaskTrackerWithOptions(%q, WithSkipMigrations): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("Close failed during cleanup: %v", err)
		}
	})
	return tracker, dbPath
}

// copyFile copies src to dst using ordinary filesystem bytes. The destination
// parent directory must already exist.
func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %q: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open destination %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination %q: %w", dst, err)
	}
	return nil
}

func goldenUnifiedDBSource(t *testing.T) string {
	t.Helper()
	goldenUnifiedDB.once.Do(func() {
		dir := filepath.Join(os.TempDir(), fmt.Sprintf("pasture-golden-db-%d", os.Getpid()))
		_ = os.RemoveAll(dir)
		err := os.MkdirAll(dir, 0o700)
		if err != nil {
			goldenUnifiedDB.err = err
			return
		}
		dbPath := filepath.Join(dir, "pasture.db")
		tracker, err := tasks.OpenTaskTracker(dbPath)
		if err != nil {
			goldenUnifiedDB.err = fmt.Errorf("build golden tracker: %w", err)
			return
		}
		if err := tracker.Close(); err != nil {
			goldenUnifiedDB.err = fmt.Errorf("close golden tracker: %w", err)
			return
		}
		if err := checkpointAndAssertGolden(dbPath); err != nil {
			goldenUnifiedDB.err = err
			return
		}
		goldenUnifiedDB.path = dbPath
	})
	if goldenUnifiedDB.err != nil {
		t.Fatalf("build golden pasture.db: %v", goldenUnifiedDB.err)
	}
	return goldenUnifiedDB.path
}

func checkpointAndAssertGolden(dbPath string) error {
	db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
	if err != nil {
		return fmt.Errorf("open golden db for checkpoint: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint golden db: %w", err)
	}
	version, err := audit.ReadSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("read golden audit schema version: %w", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		return fmt.Errorf("golden audit schema version = %d, want %d", version, audit.MaxKnownSchemaVersion)
	}
	var models int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ml_models`).Scan(&models); err != nil {
		return fmt.Errorf("count golden ml_models: %w", err)
	}
	if models == 0 {
		return fmt.Errorf("golden provenance ml_models table is empty")
	}
	return nil
}

package idmigrate_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// TestConvertIsANoOpOnACurrentStore: a store the production opener created
// already carries the slash marker, so a conversion reports AlreadyCurrent and
// writes nothing — no backup is even made.
func TestConvertIsANoOpOnACurrentStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before := sha256File(t, path)

	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open shared: %v", err)
	}
	defer db.Close()
	report, err := idmigrate.Convert(context.Background(), db, idmigrate.ConvertOptions{StorePath: path})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !report.AlreadyCurrent {
		t.Fatalf("report=%+v want AlreadyCurrent", report)
	}
	if report.BackupPath != "" || report.BackupSHA256 != "" {
		t.Fatalf("report=%+v want no backup on the already-current path", report)
	}
	if after := sha256File(t, path); after != before {
		t.Fatalf("store bytes changed on the no-op path: before=%s after=%s", before, after)
	}
}

// TestConvertIsANoOpOnAnEmptyStore: a store with no identities at all gets its
// marker from the dependency's own activation, so conversion reports
// NothingToConvert and makes no backup.
func TestConvertIsANoOpOnAnEmptyStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.db")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open shared: %v", err)
	}
	defer db.Close()
	report, err := idmigrate.Convert(context.Background(), db, idmigrate.ConvertOptions{StorePath: path})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !report.NothingToConvert || report.AlreadyCurrent {
		t.Fatalf("report=%+v want NothingToConvert only", report)
	}
}

// TestNormalOpenRefusesARetiredStore: a populated store with no slash marker is
// refused by the production open path with a storage-category error that names
// the converting command — before any schema work, actor registration or
// journal decode.
func TestNormalOpenRefusesARetiredStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "retired.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE tasks (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create tasks: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks(id) VALUES (?)`, "file://project--01968a3c-9d4f-7c8a-bc12-feedfacecafe"); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = tasks.OpenTaskTracker(path)
	if err == nil {
		t.Fatal("OpenTaskTracker accepted a populated store with no slash marker")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("OpenTaskTracker returned %T, want *pasterrors.StructuredError: %v", err, err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category=%q want %q", se.Category, pasterrors.CategoryStorage)
	}
	if code := pasterrors.ExitCode(se); code != 5 {
		t.Errorf("ExitCode=%d want 5", code)
	}
	for _, want := range []string{"pasture migrate ids --dry-run", "pasture migrate ids --db", "retired two-dash"} {
		if !strings.Contains(se.Fix+se.What+se.Why, want) {
			t.Errorf("refusal text missing %q; What=%q Why=%q Fix=%q", want, se.What, se.Why, se.Fix)
		}
	}
}

// TestDryRunLeavesTheStoreBytesUnchanged: the read-only dry run reports the
// format and affected counts and never writes, so the store's SHA-256 is
// identical before and after.
func TestDryRunLeavesTheStoreBytesUnchanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := tracker.Create("file://dry-run-test", "seed", "seed", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before := sha256File(t, path)

	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open shared: %v", err)
	}
	defer db.Close()
	preview, err := idmigrate.DryRun(context.Background(), db, path)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !preview.Current() {
		t.Fatalf("preview=%+v want a current (slash-marker) store", preview)
	}
	if after := sha256File(t, path); after != before {
		t.Fatalf("dry run changed the store bytes: before=%s after=%s", before, after)
	}
}

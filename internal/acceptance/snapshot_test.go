package acceptance_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestSnapshotFileExactDeterministicAndReadOnly(t *testing.T) {
	store := testutil.OpenAcceptanceStore(t)
	if _, err := store.Tracker.Create("acceptance", "seed", "seed through production API", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("production seed Create: %v", err)
	}
	store.Close(t)
	fileBefore, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatalf("SnapshotFile(first): %v", err)
	}
	second, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatalf("SnapshotFile(second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("snapshot is nondeterministic")
	}
	fileAfter, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(fileBefore) != sha256.Sum256(fileAfter) {
		t.Fatal("read-only snapshot changed database bytes")
	}
	journal, ok := first.Semantic(acceptance.SemanticJournal)
	if !ok || journal.RowCount == 0 || !strings.HasPrefix(journal.ByteDigest, "sha256:") {
		t.Fatalf("journal snapshot = %#v, ok=%t", journal, ok)
	}
	if first.RowCount == 0 || len(first.Tables) == 0 {
		t.Fatalf("empty store snapshot: %#v", first)
	}
}

func TestSnapshotDeltaAndUnrelatedIdentity(t *testing.T) {
	store := testutil.OpenAcceptanceStore(t)
	if _, err := store.Tracker.Create("acceptance", "seed", "seed", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	store.Close(t)
	before, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	store.Reopen(t)
	if _, err := store.Tracker.Create("acceptance", "second", "second", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	store.Close(t)
	after, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	beforeGraph, _ := before.Semantic(acceptance.SemanticGraph)
	afterGraph, _ := after.Semantic(acceptance.SemanticGraph)
	delta, err := acceptance.Delta(beforeGraph, afterGraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Added) == 0 || delta.RowCount <= beforeGraph.RowCount || delta.ByteDigest == beforeGraph.ByteDigest {
		t.Fatalf("graph delta = %#v", delta)
	}
	changes, err := acceptance.CompareRowChanges(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("production mutation produced no exact row changes")
	}
	if err := acceptance.AssertExactRowChanges(before, after, changes); err != nil {
		t.Fatalf("exact row identity delta: %v", err)
	}
}

func TestSnapshotFileBoundsAndMissingStore(t *testing.T) {
	t.Parallel()
	if _, err := acceptance.SnapshotFile(context.Background(), t.TempDir()+"/missing.db", acceptance.DefaultSnapshotLimits()); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("missing store error = %v", err)
	}
	store := testutil.OpenAcceptanceStore(t)
	store.Close(t)
	limits := acceptance.DefaultSnapshotLimits()
	limits.MaxRowsPerTable = 1
	if _, err := acceptance.SnapshotFile(context.Background(), store.Path, limits); err == nil || !strings.Contains(err.Error(), "MaxRowsPerTable") {
		t.Fatalf("row bound error = %v", err)
	}
}

func TestAssertExactRowChangesRejectsSameTableUnrelatedCorruption(t *testing.T) {
	t.Parallel()
	before := acceptance.StoreSnapshot{Tables: []acceptance.TableSnapshot{{Name: "tasks", Columns: []string{"id", "value"}, Rows: []acceptance.CanonicalRow{{Identity: "allowed", Value: "before-a"}, {Identity: "unrelated", Value: "before-b"}}}}}
	after := before
	after.Tables = []acceptance.TableSnapshot{{Name: "tasks", Columns: []string{"id", "value"}, Rows: []acceptance.CanonicalRow{{Identity: "allowed", Value: "before-a"}, {Identity: "unrelated", Value: "corrupt-b"}}}}
	wrongAllowance := []acceptance.RowChange{{Table: "tasks", Identity: "allowed", Kind: acceptance.RowChanged}}
	if err := acceptance.AssertExactRowChanges(before, after, wrongAllowance); err == nil || !strings.Contains(err.Error(), "exact row delta mismatch") {
		t.Fatalf("same-table corruption error = %v", err)
	}
	exact := []acceptance.RowChange{{Table: "tasks", Identity: "unrelated", Kind: acceptance.RowChanged}}
	if err := acceptance.AssertExactRowChanges(before, after, exact); err != nil {
		t.Fatalf("exact same-table identity allowance: %v", err)
	}
}

func TestSnapshotFileCanonicalTotalByteBudgetAndBoundaryFailures(t *testing.T) {
	t.Parallel()
	path := newAdversarialSnapshotDB(t)
	baseline, err := acceptance.SnapshotFile(context.Background(), path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	retained := retainedCanonicalBytes(baseline)
	limits := acceptance.DefaultSnapshotLimits()
	limits.MaxTotalBytes = retained - 1
	if _, err := acceptance.SnapshotFile(context.Background(), path, limits); err == nil || !strings.Contains(err.Error(), "before retaining") {
		t.Fatalf("incremental total-byte error = %v", err)
	}
	limits.MaxTotalBytes = retained
	if _, err := acceptance.SnapshotFile(context.Background(), path, limits); err != nil {
		t.Fatalf("exact canonical total-byte bound rejected: %v", err)
	}

	limits = acceptance.DefaultSnapshotLimits()
	limits.MaxCellBytes = 2
	if _, err := acceptance.SnapshotFile(context.Background(), path, limits); err == nil || !strings.Contains(err.Error(), "MaxCellBytes") {
		t.Fatalf("cell byte bound error = %v", err)
	}
	limits = acceptance.DefaultSnapshotLimits()
	limits.MaxTables = 1
	if _, err := acceptance.SnapshotFile(context.Background(), path, limits); err == nil || !strings.Contains(err.Error(), "MaxTables") {
		t.Fatalf("table count bound error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acceptance.SnapshotFile(cancelled, path, acceptance.DefaultSnapshotLimits()); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestSnapshotFilePreservesFullSequenceAndProjectsJournalOnly(t *testing.T) {
	t.Parallel()
	path := newAdversarialSnapshotDB(t)
	snapshot, err := acceptance.SnapshotFile(context.Background(), path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	sequence := tableByName(t, snapshot, "sqlite_sequence")
	if sequence.RowCount != 1 || !strings.Contains(sequence.Rows[0].Value, hex.EncodeToString([]byte("tiny"))) {
		t.Fatalf("full sqlite_sequence did not retain non-journal row: %#v", sequence.Rows)
	}
	journalSemantic, _ := snapshot.Semantic(acceptance.SemanticJournal)
	journalSequenceProjection := semanticTableByName(t, journalSemantic, "sqlite_sequence")
	if journalSequenceProjection.RowCount != 0 {
		t.Fatalf("journal semantic projection retained non-journal sequence rows: %#v", journalSequenceProjection.Rows)
	}

	store := testutil.OpenAcceptanceStore(t)
	if _, err := store.Tracker.Create("acceptance", "sequence", "journal sequence", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	store.Close(t)
	production, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	journalSemantic, _ = production.Semantic(acceptance.SemanticJournal)
	journalSequence := semanticTableByName(t, journalSemantic, "sqlite_sequence")
	if journalSequence.RowCount != 1 || !strings.Contains(journalSequence.Rows[0].Value, hex.EncodeToString([]byte("journal"))) {
		t.Fatalf("journal sequence snapshot = %#v", journalSequence)
	}
}

func TestSnapshotSequenceChangesRemainExactlyDetectable(t *testing.T) {
	t.Parallel()
	path := newAdversarialSnapshotDB(t)
	before, err := acceptance.SnapshotFile(context.Background(), path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tiny(value) VALUES ('jkl')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := acceptance.SnapshotFile(context.Background(), path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	changes, err := acceptance.CompareRowChanges(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTableChange(changes, "sqlite_sequence", acceptance.RowChanged) {
		t.Fatalf("non-journal sequence change missing from full snapshot delta: %v", changes)
	}

	store := testutil.OpenAcceptanceStore(t)
	if _, err := store.Tracker.Create("acceptance", "seed", "seed", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	store.Close(t)
	journalBefore, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	store.Reopen(t)
	if _, err := store.Tracker.Create("acceptance", "next", "next", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatal(err)
	}
	store.Close(t)
	journalAfter, err := acceptance.SnapshotFile(context.Background(), store.Path, acceptance.DefaultSnapshotLimits())
	if err != nil {
		t.Fatal(err)
	}
	changes, err = acceptance.CompareRowChanges(journalBefore, journalAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTableChange(changes, "sqlite_sequence", acceptance.RowChanged) {
		t.Fatalf("journal sequence change missing from full snapshot delta: %v", changes)
	}
}

func TestExactRowComparisonRejectsTableAndColumnSchemaDrift(t *testing.T) {
	t.Parallel()
	base := acceptance.StoreSnapshot{Tables: []acceptance.TableSnapshot{{Name: "tasks", Columns: []string{"id", "title"}, Rows: []acceptance.CanonicalRow{}}}}
	tableDrift := acceptance.StoreSnapshot{Tables: []acceptance.TableSnapshot{{Name: "tasks", Columns: []string{"id", "title"}, Rows: []acceptance.CanonicalRow{}}, {Name: "extra", Columns: []string{"id"}, Rows: []acceptance.CanonicalRow{}}}}
	if changes, err := acceptance.CompareRowChanges(base, tableDrift); err == nil || !strings.Contains(err.Error(), "table inventory mismatch") || changes != nil {
		t.Fatalf("table drift changes=%v error=%v", changes, err)
	}
	columnDrift := acceptance.StoreSnapshot{Tables: []acceptance.TableSnapshot{{Name: "tasks", Columns: []string{"id", "renamed_title"}, Rows: []acceptance.CanonicalRow{}}}}
	if changes, err := acceptance.CompareRowChanges(base, columnDrift); err == nil || !strings.Contains(err.Error(), "column schema mismatch") || changes != nil {
		t.Fatalf("column drift changes=%v error=%v", changes, err)
	}
	if err := acceptance.AssertExactRowChanges(base, columnDrift, nil); err == nil || !strings.Contains(err.Error(), "column schema mismatch") {
		t.Fatalf("AssertExactRowChanges schema drift error=%v", err)
	}
	beforeSemantic := acceptance.SemanticSnapshot{Kind: acceptance.SemanticGraph, Tables: base.Tables}
	afterSemantic := acceptance.SemanticSnapshot{Kind: acceptance.SemanticGraph, Tables: columnDrift.Tables}
	if _, err := acceptance.Delta(beforeSemantic, afterSemantic); err == nil || !strings.Contains(err.Error(), "column schema mismatch") {
		t.Fatalf("semantic Delta schema drift error=%v", err)
	}
}

func newAdversarialSnapshotDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE tiny (id INTEGER PRIMARY KEY AUTOINCREMENT, value TEXT NOT NULL); INSERT INTO tiny(value) VALUES ('abc'), ('def'), ('ghi')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func retainedCanonicalBytes(snapshot acceptance.StoreSnapshot) int {
	total := 0
	for _, table := range snapshot.Tables {
		total += len(table.Name)
		for _, column := range table.Columns {
			total += len(column)
		}
		for _, row := range table.Rows {
			total += len(row.Identity) + len(row.Value)
		}
	}
	return total
}

func tableByName(t *testing.T, snapshot acceptance.StoreSnapshot, name string) acceptance.TableSnapshot {
	t.Helper()
	for _, table := range snapshot.Tables {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("snapshot has no table %q", name)
	return acceptance.TableSnapshot{}
}

func semanticTableByName(t *testing.T, snapshot acceptance.SemanticSnapshot, name string) acceptance.TableSnapshot {
	t.Helper()
	for _, table := range snapshot.Tables {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("semantic snapshot has no table %q", name)
	return acceptance.TableSnapshot{}
}

func hasTableChange(changes []acceptance.RowChange, table string, kind acceptance.RowChangeKind) bool {
	for _, change := range changes {
		if change.Table == table && change.Kind == kind {
			return true
		}
	}
	return false
}

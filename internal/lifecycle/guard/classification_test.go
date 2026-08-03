package guard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLifecycleModelClassificationIsTotal(t *testing.T) {
	t.Parallel()
	_, source, _, _ := runtime.Caller(0)
	if err := ValidateModelDirectory(ModelDirectoryFromGuardPackage(filepath.Dir(source))); err != nil {
		t.Fatal(err)
	}
}

func TestUnclassifiedExportedStructFailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte("package model\ntype ForgottenRecord struct { ID int64 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModelDirectory(dir)
	if err == nil || !strings.Contains(err.Error(), "ForgottenRecord") {
		t.Fatalf("expected ForgottenRecord classification failure, got %v", err)
	}
}

func TestMarkedNonJournalStructNeedsNoExceptionEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := "package model\ntype nonJournalValue struct{}\ntype FutureQuery struct { nonJournalValue; Limit int }\n"
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelDirectory(dir); err != nil {
		t.Fatalf("mechanical non-journal predicate rejected marked value: %v", err)
	}
}

func TestImmutableSnapshotStatusMutationFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := "package model\ntype DefinitionStatus uint8\ntype DefinitionSnapshot struct { Status DefinitionStatus }\ntype OccurrenceRecord struct{}\ntype DefinitionStateFact struct{}\ntype DefinitionStateRecord struct { SnapshotJournalID int64 }\n"
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModelDirectory(dir)
	if err == nil || !strings.Contains(err.Error(), "immutable snapshot contains lifecycle status") {
		t.Fatalf("expected immutable-snapshot shape failure, got %v", err)
	}
}

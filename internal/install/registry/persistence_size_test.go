package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

func storeWithEncodedSize(t *testing.T, size int) Store {
	t.Helper()
	c, err := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	if err != nil {
		t.Fatal(err)
	}
	key, err := GlobalKey(c)
	if err != nil {
		t.Fatal(err)
	}
	diagnosticLength := size
	for attempts := 0; attempts < 4; attempts++ {
		record, recordErr := NewRecord(RecordInput{Key: key, Source: SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, Observation: ObservationInstalled, Trust: TrustNotApplicable, LastOperation: OperationInspect, LastOutcome: OutcomeCompleted, Diagnostic: strings.Repeat("x", diagnosticLength)})
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		store := New()
		if err := store.Upsert(record); err != nil {
			t.Fatal(err)
		}
		encoded, err := store.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) == size {
			return store
		}
		diagnosticLength += size - len(encoded)
		if diagnosticLength < 0 {
			t.Fatalf("cannot construct a %d-byte registry", size)
		}
	}
	t.Fatalf("could not converge on a %d-byte encoded registry", size)
	return Store{}
}

func TestPersistenceEnforcesOneExactByteLimitForSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	exact := storeWithEncodedSize(t, maxRegistryBytes)
	if err := Save(path, exact); err != nil {
		t.Fatalf("Save at exact limit: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxRegistryBytes {
		t.Fatalf("saved size=%d, want %d", info.Size(), maxRegistryBytes)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load at exact limit: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("\n")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("Load above limit error=%v", err)
	}

	prior := New()
	if err := Save(path, prior); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oversized := storeWithEncodedSize(t, maxRegistryBytes+1)
	if err := Save(path, oversized); err == nil || !strings.Contains(err.Error(), "size") || !strings.Contains(err.Error(), "before replacement") {
		t.Fatalf("oversized Save error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("oversized Save changed the prior committed registry")
	}
	if loaded, err := Load(path); err != nil || loaded.Len() != 0 {
		t.Fatalf("prior registry no longer loads: len=%d err=%v", loaded.Len(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-registry-") {
			t.Fatalf("oversized Save left temporary residue %q", entry.Name())
		}
	}
}

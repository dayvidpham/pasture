//go:build windows

package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

func TestWindowsFailedReplacePreservesCommittedRegistryAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	key, _ := GlobalKey(c)
	oldRecord, _ := NewRecord(RecordInput{Key: key, Source: SourceInstaller, Strategy: activation.NativePluginKindValue(), Managed: true, Observation: ObservationInstalled, Trust: TrustNotApplicable, LastOperation: OperationEnsure, LastOutcome: OutcomeCompleted, Diagnostic: "old"})
	oldStore := New()
	_ = oldStore.Upsert(oldRecord)
	if err := Save(path, oldStore); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newRecord, _ := NewRecord(RecordInput{Key: key, Source: SourceHomeManager, Strategy: activation.NativePluginKindValue(), Managed: false, Observation: ObservationAbsent, Trust: TrustPending, LastOperation: OperationRemove, LastOutcome: OutcomeFailed, Diagnostic: "new"})
	newStore := New()
	_ = newStore.Upsert(newRecord)
	original := registryWindowsRename
	registryWindowsRename = func(*os.Root, string, string) error { return errors.New("injected replace failure") }
	t.Cleanup(func() { registryWindowsRename = original })
	if err := Save(path, newStore); err == nil || !strings.Contains(err.Error(), "replace failure") {
		t.Fatalf("Save error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("committed registry changed after failed replacement")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-registry-") {
			t.Fatalf("failed replacement left temporary file %q", entry.Name())
		}
	}
}

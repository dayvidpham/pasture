//go:build windows

package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"golang.org/x/sys/windows"
)

func TestWindowsCreationDescriptorSetsCurrentUserAsOwner(t *testing.T) {
	want, descriptor, dacl, err := currentWindowsOwnerACL()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(want) {
		t.Fatalf("creation descriptor owner=%v want current user %v err=%v", owner, want, err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("creation descriptor DACL=%v", dacl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil {
		t.Fatalf("read creation descriptor ACE: %v", err)
	}
	if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !sid.Equals(want) {
		t.Fatalf("creation descriptor ACE SID=%v want owner %v", sid, want)
	}
}

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

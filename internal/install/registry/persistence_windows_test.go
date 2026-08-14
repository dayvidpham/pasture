//go:build windows

package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func TestWindowsSaveLoadReplaceOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installations.yaml")
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	key, _ := registry.GlobalKey(c)
	one, _ := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.NativePluginKindValue(), Managed: true, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted})
	first := registry.New()
	_ = first.Upsert(one)
	if err := registry.Save(path, first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	loaded, err := registry.Load(path)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if got, ok := loaded.Lookup(key); !ok || got.Observation() != registry.ObservationInstalled {
		t.Fatalf("first record=%+v", got)
	}
	two, _ := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceHomeManager, Strategy: activation.NativePluginKindValue(), Managed: false, Observation: registry.ObservationAbsent, Trust: registry.TrustPending, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed})
	second := registry.New()
	_ = second.Upsert(two)
	if err := registry.Save(path, second); err != nil {
		t.Fatalf("replacement Save: %v", err)
	}
	loaded, err = registry.Load(path)
	if err != nil {
		t.Fatalf("replacement Load: %v", err)
	}
	if got, ok := loaded.Lookup(key); !ok || got.Observation() != registry.ObservationAbsent || got.Source() != registry.SourceHomeManager {
		t.Fatalf("replacement record=%+v", got)
	}

	unsafe := filepath.Join(t.TempDir(), "unsafe.yaml")
	data, _ := second.Marshal()
	if err := os.WriteFile(unsafe, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load(unsafe); err == nil {
		t.Fatal("inherited non-owner-only ACL accepted")
	}
	link := filepath.Join(t.TempDir(), "linked.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatalf("create registry reparse test boundary: %v", err)
	}
	if _, err := registry.Load(link); err == nil {
		t.Fatal("reparse registry accepted")
	}
	if err := registry.Save(link, second); err == nil {
		t.Fatal("reparse registry destination accepted")
	}
	parentTarget := t.TempDir()
	parentLink := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(parentTarget, parentLink); err != nil {
		t.Fatalf("create parent reparse test boundary: %v", err)
	}
	if err := registry.Save(filepath.Join(parentLink, "installations.yaml"), second); err == nil {
		t.Fatal("reparse registry parent accepted")
	}
	failureDir := t.TempDir()
	failurePath := filepath.Join(failureDir, "installations.yaml")
	if err := os.Mkdir(failurePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(failurePath, second); err == nil {
		t.Fatal("directory registry destination accepted")
	}
	entries, err := os.ReadDir(failureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-registry-") {
			t.Fatalf("failed Save left temporary file %q", entry.Name())
		}
	}
}

//go:build linux || darwin

package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

func TestFailedRenamePreservesCommittedRegistryAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installations.yaml")
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	key, _ := GlobalKey(c)
	bundle, _ := artifact.ParseBundleID("artifact.bundle.v1:sha256:" + strings.Repeat("a", 64))
	version, _ := NewVersion("claude@old")
	selector, _ := NewSelector("old@user")
	pathValue, _ := artifact.NewPath("skills/old/SKILL.md")
	mode, _ := artifact.NewMode(0o644)
	leaf, _ := NewLeaf(pathValue, artifact.RegularFileType(), mode, artifact.DigestBytes([]byte("old")))
	dirValue, _ := artifact.NewPath("skills/old")
	oldRecord, err := NewRecord(RecordInput{Key: key, Source: SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, ArtifactID: bundle, Version: version, Selector: selector, Leaves: []Leaf{leaf}, CreatedDirs: []artifact.Path{dirValue}, Observation: ObservationInstalled, Trust: TrustNotApplicable, LastOperation: OperationEnsure, LastOutcome: OutcomeCompleted, Diagnostic: "old diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	initial := New()
	_ = initial.Upsert(oldRecord)
	if err := Save(path, initial); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	original := registryUnixRenameat
	registryUnixRenameat = func(int, string, int, string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { registryUnixRenameat = original })
	newRecord, err := NewRecord(RecordInput{Key: key, Source: SourceHomeManager, Strategy: activation.NativePluginKindValue(), Managed: false, Observation: ObservationAbsent, Trust: TrustPending, LastOperation: OperationRemove, LastOutcome: OutcomeFailed, Diagnostic: "new diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	replacement := New()
	_ = replacement.Upsert(newRecord)
	newBytes, _ := replacement.Marshal()
	if string(newBytes) == string(want) {
		t.Fatal("test stores are not byte-distinct")
	}
	if err := Save(path, replacement); err == nil || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("Save error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("committed bytes changed\nwant %q\ngot  %q", want, got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("committed mode=%04o", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gotRecord, ok := loaded.Lookup(key)
	if !ok {
		t.Fatal("old record disappeared")
	}
	if gotRecord.Source() != SourceInstaller || gotRecord.Strategy() != activation.DirectFileKindValue() || !gotRecord.Managed() || gotRecord.ArtifactID() != bundle || gotRecord.Version() != version || gotRecord.Selector() != selector || gotRecord.Observation() != ObservationInstalled || gotRecord.Trust() != TrustNotApplicable || gotRecord.LastOperation() != OperationEnsure || gotRecord.LastOutcome() != OutcomeCompleted || gotRecord.Diagnostic() != "old diagnostic" {
		t.Fatalf("old scalar ownership changed: %+v", gotRecord)
	}
	if len(gotRecord.Leaves()) != 1 || gotRecord.Leaves()[0].Path() != pathValue || gotRecord.Leaves()[0].Type() != artifact.RegularFileType() || gotRecord.Leaves()[0].Mode() != mode || gotRecord.Leaves()[0].Digest() != leaf.Digest() || len(gotRecord.CreatedDirs()) != 1 || gotRecord.CreatedDirs()[0] != dirValue {
		t.Fatalf("old nested ownership changed: %+v", gotRecord)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pasture-registry-") {
			t.Fatalf("temporary residue %q", entry.Name())
		}
	}
}

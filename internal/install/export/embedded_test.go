package export_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
)

// This is the production path a release build runs: the embedded target
// descriptors, exported and then proven against the very manifests they came
// from. It runs once over the real assets rather than per-cell.
func TestExport_EmbeddedTargetsProduceVerifiedArchives(t *testing.T) {
	t.Parallel()
	cells, err := export.EmbeddedBundles()
	if err != nil {
		t.Fatalf("embedded bundles: %v", err)
	}
	canonical := artifact.ComponentIDs()
	if len(cells) != len(canonical) {
		t.Fatalf("embedded source yields %d cells, want %d", len(cells), len(canonical))
	}
	byID := map[artifact.ComponentID]artifact.Bundle{}
	for index, item := range cells {
		if item.ID != canonical[index] {
			t.Fatalf("cell %d is %s, want canonical order entry %s", index, item.ID, canonical[index])
		}
		byID[item.ID] = item.Bundle
	}

	outDir := filepath.Join(t.TempDir(), "release")
	result, err := export.Export(context.Background(), export.Request{Version: mustVersion(t, "1.4.0"), OutDir: outDir}, export.EmbeddedBundles)
	if err != nil {
		t.Fatalf("export embedded targets: %v", err)
	}
	for _, cellResult := range result.Cells {
		content, readErr := os.ReadFile(cellResult.ArchivePath)
		if readErr != nil {
			t.Fatalf("read %q: %v", cellResult.Asset, readErr)
		}
		if err := export.VerifyArchive(cellResult.ID, cellResult.Asset, content, byID[cellResult.ID]); err != nil {
			t.Fatalf("embedded archive for %s does not match its bundle: %v", cellResult.ID, err)
		}
		if cellResult.Members == 0 {
			t.Fatalf("embedded archive for %s carries no members", cellResult.ID)
		}
	}
}

// Executable bits survive the round trip: the Claude target assigns 0755 to its
// shell scripts because embedding drops the bit, and the archive must carry the
// manifest's mode rather than re-deriving it.
func TestExport_EmbeddedShellScriptsKeepTheirExecutableMode(t *testing.T) {
	t.Parallel()
	cells, err := export.EmbeddedBundles()
	if err != nil {
		t.Fatalf("embedded bundles: %v", err)
	}
	scripts := 0
	for _, item := range cells {
		members, membersErr := export.BundleMembers(item.Bundle)
		if membersErr != nil {
			t.Fatalf("members for %s: %v", item.ID, membersErr)
		}
		for _, member := range members {
			if !strings.HasSuffix(member.Path, ".sh") {
				continue
			}
			scripts++
			if member.Mode != 0o755 {
				t.Fatalf("%s member %q has mode %04o, want the manifest's 0755", item.ID, member.Path, member.Mode)
			}
		}
	}
	if scripts == 0 {
		t.Fatal("no shell script members were found; the executable-mode rule is no longer exercised")
	}
}

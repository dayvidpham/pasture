package export_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// This test deliberately does NOT use export.ReadArchive: it decodes the real
// exported archives with the standard library and compares them against the
// embedded bundle manifests directly, so the format is proven by an
// independent reader rather than confirmed by its own.
func TestExport_EmbeddedArchivesDecodeWithTheStandardLibrary(t *testing.T) {
	t.Parallel()
	cells, err := export.EmbeddedBundles()
	if err != nil {
		t.Fatalf("embedded bundles: %v", err)
	}
	outDir := filepath.Join(t.TempDir(), "release")
	result, err := export.Export(context.Background(), export.Request{Version: mustVersion(t, "1.4.0"), OutDir: outDir}, export.EmbeddedBundles)
	if err != nil {
		t.Fatalf("export embedded targets: %v", err)
	}
	archives := map[artifact.ComponentID]string{}
	for _, cellResult := range result.Cells {
		archives[cellResult.ID] = cellResult.ArchivePath
	}
	executable := 0
	for _, item := range cells {
		path, ok := archives[item.ID]
		if !ok {
			t.Fatalf("no archive was written for %s", item.ID)
		}
		entries := item.Bundle.Manifest().Entries()
		file, openErr := os.Open(path)
		if openErr != nil {
			t.Fatalf("open %s archive: %v", item.ID, openErr)
		}
		decompressor, gzipErr := gzip.NewReader(file)
		if gzipErr != nil {
			file.Close()
			t.Fatalf("%s archive is not a gzip stream: %v", item.ID, gzipErr)
		}
		reader := tar.NewReader(decompressor)
		index := 0
		for {
			header, nextErr := reader.Next()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Fatalf("%s archive member %d: %v", item.ID, index, nextErr)
			}
			if index >= len(entries) {
				t.Fatalf("%s archive holds more members than its manifest declares (%d)", item.ID, len(entries))
			}
			entry := entries[index]
			name := strings.TrimSuffix(header.Name, "/")
			if name != entry.Path().String() {
				t.Fatalf("%s member %d is %q, but the manifest declares %q at that position",
					item.ID, index, name, entry.Path())
			}
			if uint32(header.Mode) != entry.Mode().Bits() {
				t.Fatalf("%s member %q has mode %04o, but the manifest declares %04o",
					item.ID, name, uint32(header.Mode), entry.Mode().Bits())
			}
			if header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
				t.Fatalf("%s member %q carries ownership uid=%d gid=%d uname=%q gname=%q",
					item.ID, name, header.Uid, header.Gid, header.Uname, header.Gname)
			}
			if !header.ModTime.Equal(time.Unix(0, 0).UTC()) {
				t.Fatalf("%s member %q carries modification time %s, want the fixed epoch", item.ID, name, header.ModTime)
			}
			switch header.Typeflag {
			case tar.TypeDir:
				if !entry.IsDirectory() {
					t.Fatalf("%s member %q is archived as a directory but declared as a file", item.ID, name)
				}
			case tar.TypeReg:
				if !entry.IsRegular() {
					t.Fatalf("%s member %q is archived as a file but declared as a directory", item.ID, name)
				}
				body, readErr := io.ReadAll(reader)
				if readErr != nil {
					t.Fatalf("%s member %q body: %v", item.ID, name, readErr)
				}
				if digest := artifact.DigestBytes(body); digest != entry.Digest() {
					t.Fatalf("%s member %q digests to %s, but the manifest declares %s", item.ID, name, digest, entry.Digest())
				}
				if strings.HasSuffix(name, ".sh") {
					executable++
					if uint32(header.Mode) != 0o755 {
						t.Fatalf("%s member %q has mode %04o, want 0755", item.ID, name, uint32(header.Mode))
					}
				}
			default:
				t.Fatalf("%s member %q has unsupported tar type %q", item.ID, name, string(header.Typeflag))
			}
			index++
		}
		if index != len(entries) {
			t.Fatalf("%s archive holds %d members, but its manifest declares %d", item.ID, index, len(entries))
		}
		if closeErr := decompressor.Close(); closeErr != nil {
			t.Fatalf("%s archive gzip trailer: %v", item.ID, closeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			t.Fatalf("close %s archive: %v", item.ID, closeErr)
		}
	}
	if executable == 0 {
		t.Fatal("no executable shell members were decoded; the mode rule is no longer exercised")
	}
}

package export_test

import (
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
)

// leaf declares one synthetic bundle file: its bundle-relative path, its
// permission mode, and its exact bytes.
type leaf struct {
	path string
	mode uint32
	body string
}

// newBundle builds a real artifact.Bundle from synthetic leaves, exercising the
// same constructor the embedded target descriptors use.
func newBundle(t *testing.T, leaves ...leaf) artifact.Bundle {
	t.Helper()
	source := fstest.MapFS{}
	entries := make([]artifact.Entry, 0, len(leaves))
	for _, item := range leaves {
		entryPath, err := artifact.NewPath(item.path)
		if err != nil {
			t.Fatalf("path %q: %v", item.path, err)
		}
		mode, err := artifact.NewMode(item.mode)
		if err != nil {
			t.Fatalf("mode %04o: %v", item.mode, err)
		}
		entry, err := artifact.NewFileEntry(entryPath, mode, artifact.DigestBytes([]byte(item.body)))
		if err != nil {
			t.Fatalf("entry %q: %v", item.path, err)
		}
		entries = append(entries, entry)
		source[item.path] = &fstest.MapFile{Data: []byte(item.body), Mode: 0o644}
	}
	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	bundle, err := artifact.NewBundle(source, manifest)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	return bundle
}

// cellLeaves gives each canonical cell distinct bytes so a mixed-up cell is
// visible as a different archive rather than an identical one.
func cellLeaves(id artifact.ComponentID) []leaf {
	stem := id.String()
	return []leaf{
		{path: "zebra.md", mode: 0o644, body: "zebra for " + stem + "\n"},
		{path: "alpha/nested.json", mode: 0o644, body: "{\"cell\":\"" + stem + "\"}\n"},
		{path: "alpha/run.sh", mode: 0o755, body: "#!/bin/sh\necho " + stem + "\n"},
	}
}

// syntheticSource yields one small bundle per canonical component, so export
// tests run the production path without the embedded asset trees.
func syntheticSource(t *testing.T) export.BundleSource {
	t.Helper()
	cells := make([]export.CellBundle, 0, 9)
	for _, id := range artifact.ComponentIDs() {
		cells = append(cells, export.CellBundle{ID: id, Bundle: newBundle(t, cellLeaves(id)...)})
	}
	return func() ([]export.CellBundle, error) { return cells, nil }
}

func mustVersion(t *testing.T, value string) artifact.Version {
	t.Helper()
	version, err := artifact.ParseVersion(value)
	if err != nil {
		t.Fatalf("parse version %q: %v", value, err)
	}
	return version
}

package export_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
)

func exportInto(t *testing.T, dir string, source export.BundleSource) export.Result {
	t.Helper()
	result, err := export.Export(context.Background(), export.Request{Version: mustVersion(t, "1.4.0"), OutDir: dir}, source)
	if err != nil {
		t.Fatalf("export into %q: %v", dir, err)
	}
	return result
}

func TestExport_WritesEveryCanonicalCell(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	result := exportInto(t, outDir, syntheticSource(t))

	canonical := artifact.ComponentIDs()
	if len(result.Cells) != len(canonical) {
		t.Fatalf("exported %d cells, want %d", len(result.Cells), len(canonical))
	}
	for index, id := range canonical {
		cellResult := result.Cells[index]
		if cellResult.ID != id {
			t.Fatalf("cell %d is %s, want canonical order entry %s", index, cellResult.ID, id)
		}
		content, err := os.ReadFile(filepath.Join(outDir, cellResult.Asset))
		if err != nil {
			t.Fatalf("read exported asset %q: %v", cellResult.Asset, err)
		}
		if digest := artifact.DigestBytes(content); digest != cellResult.Digest {
			t.Fatalf("asset %q digests to %s but the report states %s", cellResult.Asset, digest, cellResult.Digest)
		}
		if cellResult.Size != int64(len(content)) {
			t.Fatalf("asset %q is %d bytes but the report states %d", cellResult.Asset, len(content), cellResult.Size)
		}
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read output directory: %v", err)
	}
	if len(entries) != len(canonical)+1 {
		t.Fatalf("output holds %d files, want %d archives plus the component set", len(entries), len(canonical))
	}
}

// The archives must carry exactly the members, modes, and digests of the
// bundles they were built from — this is the whole provenance claim.
func TestExport_ArchivesMatchTheirBundles(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	source := syntheticSource(t)
	result := exportInto(t, outDir, source)
	cells, err := source()
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	byID := map[artifact.ComponentID]artifact.Bundle{}
	for _, item := range cells {
		byID[item.ID] = item.Bundle
	}
	for _, cellResult := range result.Cells {
		content, err := os.ReadFile(filepath.Join(outDir, cellResult.Asset))
		if err != nil {
			t.Fatalf("read exported asset %q: %v", cellResult.Asset, err)
		}
		if err := export.VerifyArchive(cellResult.ID, cellResult.Asset, content, byID[cellResult.ID]); err != nil {
			t.Fatalf("archive for %s does not match its bundle: %v", cellResult.ID, err)
		}
		if cellResult.BundleID != byID[cellResult.ID].ID() {
			t.Fatalf("cell %s reports bundle %s but its bundle is %s", cellResult.ID, cellResult.BundleID, byID[cellResult.ID].ID())
		}
	}
}

func TestExport_IsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := syntheticSource(t)
	first := exportInto(t, filepath.Join(root, "first"), source)
	second := exportInto(t, filepath.Join(root, "second"), source)
	for index := range first.Cells {
		if first.Cells[index].Digest != second.Cells[index].Digest {
			t.Fatalf("cell %s is not reproducible: %s then %s",
				first.Cells[index].ID, first.Cells[index].Digest, second.Cells[index].Digest)
		}
		firstBytes, err := os.ReadFile(first.Cells[index].ArchivePath)
		if err != nil {
			t.Fatalf("read first archive: %v", err)
		}
		secondBytes, err := os.ReadFile(second.Cells[index].ArchivePath)
		if err != nil {
			t.Fatalf("read second archive: %v", err)
		}
		if string(firstBytes) != string(secondBytes) {
			t.Fatalf("cell %s archive bytes differ across runs", first.Cells[index].ID)
		}
	}
	firstSet, err := os.ReadFile(first.ComponentSetPath)
	if err != nil {
		t.Fatalf("read first component set: %v", err)
	}
	secondSet, err := os.ReadFile(second.ComponentSetPath)
	if err != nil {
		t.Fatalf("read second component set: %v", err)
	}
	if string(firstSet) != string(secondSet) {
		t.Fatal("component set bytes differ across runs")
	}
}

func TestExport_RefusesAnExistingOutputDirectory(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatalf("pre-create output directory: %v", err)
	}
	existing := filepath.Join(outDir, "published.txt")
	if err := os.WriteFile(existing, []byte("published\n"), 0o644); err != nil {
		t.Fatalf("pre-create existing file: %v", err)
	}
	_, err := export.Export(context.Background(), export.Request{Version: mustVersion(t, "1.4.0"), OutDir: outDir}, syntheticSource(t))
	if err == nil {
		t.Fatal("export claimed a directory that already exists")
	}
	if !strings.Contains(err.Error(), "already") && !strings.Contains(err.Error(), "exists") {
		t.Fatalf("failure does not explain the claimed directory: %v", err)
	}
	if _, statErr := os.Stat(existing); statErr != nil {
		t.Fatalf("export disturbed the existing directory: %v", statErr)
	}
}

func TestExport_RemovesTheDirectoryWhenACellIsMissing(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	full := syntheticSource(t)
	partial := export.BundleSource(func() ([]export.CellBundle, error) {
		cells, err := full()
		if err != nil {
			return nil, err
		}
		return cells[:len(cells)-1], nil
	})
	_, err := export.Export(context.Background(), export.Request{Version: mustVersion(t, "1.4.0"), OutDir: outDir}, partial)
	if err == nil {
		t.Fatal("export accepted an incomplete installation matrix")
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("incomplete export left the output directory behind: %v", statErr)
	}
}

func TestExport_RemovesTheDirectoryWhenCancelled(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := export.Export(ctx, export.Request{Version: mustVersion(t, "1.4.0"), OutDir: outDir}, syntheticSource(t))
	if err == nil {
		t.Fatal("export ignored a cancelled context")
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled export left the output directory behind: %v", statErr)
	}
}

func TestExport_RejectsAnUnparsedVersion(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	_, err := export.Export(context.Background(), export.Request{OutDir: outDir}, syntheticSource(t))
	if err == nil {
		t.Fatal("export accepted a zero version")
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("rejected export created an output directory: %v", statErr)
	}
}

// AssetBasename must agree with the aggregate manifest validator exactly; that
// validator is the production gate a release passes through.
func TestAssetBasename_IsAcceptedByAggregateValidation(t *testing.T) {
	t.Parallel()
	version := mustVersion(t, "1.4.0")
	revision, err := artifact.ParseRevision(strings.Repeat("ab", 20))
	if err != nil {
		t.Fatalf("parse revision: %v", err)
	}
	specs := make([]artifact.AggregateComponentSpec, 0, 9)
	for _, id := range artifact.ComponentIDs() {
		asset, assetErr := export.AssetBasename(version, id)
		if assetErr != nil {
			t.Fatalf("asset basename for %s: %v", id, assetErr)
		}
		contract, contractErr := artifact.ProductionRuntimeContract(id.Harness())
		if contractErr != nil {
			t.Fatalf("runtime contract for %s: %v", id, contractErr)
		}
		bundle := newBundle(t, cellLeaves(id)...)
		specs = append(specs, artifact.AggregateComponentSpec{
			Harness: id.Harness(), Extension: id.Extension(), Asset: asset,
			Digest: artifact.DigestBytes([]byte(asset)), BundleID: bundle.ID(),
			RuntimeContractID: contract, PastureRevision: revision, AuraRevision: revision,
		})
	}
	manifest, err := artifact.NewAggregateManifest(artifact.AggregateManifestSpec{
		Version: version, Channel: artifact.ReleaseFinal,
		InstallerMin: version, InstallerMax: version,
		PastureRevision: revision, AuraRevision: revision, Components: specs,
	})
	if err != nil {
		t.Fatalf("aggregate validation rejected the exported asset names: %v", err)
	}
	if len(manifest.Components()) != 9 {
		t.Fatalf("aggregate holds %d components, want 9", len(manifest.Components()))
	}
}

func TestAssetBasename_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	version := mustVersion(t, "1.4.0")
	if _, err := export.AssetBasename(version, artifact.ComponentID{}); err == nil {
		t.Fatal("asset naming accepted a zero component coordinate")
	}
	if _, err := export.AssetBasename(artifact.Version{}, artifact.ComponentIDs()[0]); err == nil {
		t.Fatal("asset naming accepted a zero version")
	}
}

// componentSetJSON is the exact document shape the aggregate release producer
// decodes (see aggregate-release/main.go in the Aura repository, which allows
// only these root and record fields and rejects anything else).
type componentSetJSON struct {
	Schema     string `json:"schema"`
	Components []struct {
		ID       string `json:"id"`
		Artifact string `json:"artifact"`
		Asset    string `json:"asset"`
		BundleID string `json:"bundle_id"`
	} `json:"components"`
}

func TestExport_ComponentSetMatchesTheProducerContract(t *testing.T) {
	t.Parallel()
	outDir := filepath.Join(t.TempDir(), "release")
	result := exportInto(t, outDir, syntheticSource(t))

	raw, err := os.ReadFile(result.ComponentSetPath)
	if err != nil {
		t.Fatalf("read component set: %v", err)
	}
	var document componentSetJSON
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("the component set is not decodable by the producer contract: %v", err)
	}
	if document.Schema != export.ComponentSetSchema {
		t.Fatalf("component set schema is %q, want %q", document.Schema, export.ComponentSetSchema)
	}
	canonical := artifact.ComponentIDs()
	if len(document.Components) != len(canonical) {
		t.Fatalf("component set holds %d records, want %d", len(document.Components), len(canonical))
	}
	for index, record := range document.Components {
		id, parseErr := artifact.ParseComponentID(record.ID)
		if parseErr != nil {
			t.Fatalf("record %d has an unparsable component identity: %v", index, parseErr)
		}
		if id != canonical[index] {
			t.Fatalf("record %d is %s, want canonical order entry %s", index, id, canonical[index])
		}
		if _, parseErr := artifact.ParseBundleID(record.BundleID); parseErr != nil {
			t.Fatalf("record %d has an unparsable bundle identity: %v", index, parseErr)
		}
		if record.BundleID != result.Cells[index].BundleID.String() {
			t.Fatalf("record %d states bundle %s but the export wrote %s", index, record.BundleID, result.Cells[index].BundleID)
		}
		if record.Asset != result.Cells[index].Asset {
			t.Fatalf("record %d states asset %q but the export wrote %q", index, record.Asset, result.Cells[index].Asset)
		}
		// The producer resolves a relative artifact path against the directory
		// holding the component set, so the emitted path must be readable there.
		resolved := filepath.Join(filepath.Dir(result.ComponentSetPath), record.Artifact)
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("record %d artifact %q does not resolve to a regular file: %v", index, record.Artifact, statErr)
		}
	}
}

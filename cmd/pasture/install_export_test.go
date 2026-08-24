package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
)

// exportTestSource builds one tiny real bundle per canonical cell so the verb
// test runs the production command path without the embedded asset trees.
func exportTestSource(t *testing.T) export.BundleSource {
	t.Helper()
	cells := make([]export.CellBundle, 0, 9)
	for _, id := range artifact.ComponentIDs() {
		body := []byte("cell " + id.String() + "\n")
		entryPath, err := artifact.NewPath("only.md")
		if err != nil {
			t.Fatal(err)
		}
		mode, err := artifact.NewMode(0o644)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := artifact.NewFileEntry(entryPath, mode, artifact.DigestBytes(body))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := artifact.NewManifest(entry)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := artifact.NewBundle(fstest.MapFS{"only.md": &fstest.MapFile{Data: body, Mode: 0o644}}, manifest)
		if err != nil {
			t.Fatal(err)
		}
		cells = append(cells, export.CellBundle{ID: id, Bundle: bundle})
	}
	return func() ([]export.CellBundle, error) { return cells, nil }
}

func runExportVerb(t *testing.T, source export.BundleSource, args ...string) (string, error) {
	t.Helper()
	cmd := newInstallExportComponentsCommand(source)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInstallExportComponents_WritesAssetsAndComponentSet(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "release")
	output, err := runExportVerb(t, exportTestSource(t), "--version", "1.4.0", "--out", outDir)
	if err != nil {
		t.Fatalf("export verb: %v (output: %s)", err, output)
	}
	for _, id := range artifact.ComponentIDs() {
		asset, assetErr := export.AssetBasename(mustCommandVersion(t, "1.4.0"), id)
		if assetErr != nil {
			t.Fatal(assetErr)
		}
		if !strings.Contains(output, asset) {
			t.Fatalf("verb output does not report asset %q: %s", asset, output)
		}
		if _, statErr := os.Stat(filepath.Join(outDir, asset)); statErr != nil {
			t.Fatalf("asset %q was not written: %v", asset, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outDir, export.ComponentSetFilename)); statErr != nil {
		t.Fatalf("component set was not written: %v", statErr)
	}
}

func TestInstallExportComponents_JSONReportMatchesWrittenBytes(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "release")
	output, err := runExportVerb(t, exportTestSource(t), "--version", "1.4.0", "--out", outDir, "--json")
	if err != nil {
		t.Fatalf("export verb: %v (output: %s)", err, output)
	}
	var report exportReport
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("verb JSON report is not decodable: %v (output: %s)", decodeErr, output)
	}
	if report.Version != "1.4.0" || report.ArchiveFormat != export.ArchiveFormat {
		t.Fatalf("report states version %q and format %q", report.Version, report.ArchiveFormat)
	}
	if len(report.Cells) != len(artifact.ComponentIDs()) {
		t.Fatalf("report holds %d cells, want %d", len(report.Cells), len(artifact.ComponentIDs()))
	}
	for _, cellReport := range report.Cells {
		content, readErr := os.ReadFile(filepath.Join(outDir, cellReport.Asset))
		if readErr != nil {
			t.Fatalf("read asset %q: %v", cellReport.Asset, readErr)
		}
		if digest := artifact.DigestBytes(content); digest.String() != cellReport.Digest {
			t.Fatalf("asset %q digests to %s but the report states %s", cellReport.Asset, digest, cellReport.Digest)
		}
		if int64(len(content)) != cellReport.Bytes {
			t.Fatalf("asset %q is %d bytes but the report states %d", cellReport.Asset, len(content), cellReport.Bytes)
		}
	}
}

func TestInstallExportComponents_RejectsMissingAndInvalidFlags(t *testing.T) {
	source := exportTestSource(t)
	outDir := filepath.Join(t.TempDir(), "release")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "no version", args: []string{"--out", outDir}, want: "--version is required"},
		{name: "no output", args: []string{"--version", "1.4.0"}, want: "--out is required"},
		{name: "leading v", args: []string{"--version", "v1.4.0", "--out", outDir}, want: "is not a release version"},
		{name: "positional argument", args: []string{"--version", "1.4.0", "--out", outDir, "extra"}, want: "unknown command"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runExportVerb(t, source, testCase.args...)
			if err == nil {
				t.Fatalf("verb accepted %v", testCase.args)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("failure %q does not explain %q", err, testCase.want)
			}
			if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
				t.Fatalf("rejected invocation created the output directory: %v", statErr)
			}
		})
	}
}

// The verb must be reachable as a subcommand of the human-facing install verb
// without shadowing its positional harness/extension grammar.
func TestInstallExportComponents_IsWiredUnderInstall(t *testing.T) {
	var found *cobra.Command
	for _, sub := range installCmd.Commands() {
		if sub.Name() == "export-components" {
			found = sub
		}
	}
	if found == nil {
		t.Fatal("export-components is not registered under the install verb")
	}
	for _, flagName := range []string{"version", "out", "json"} {
		if found.Flags().Lookup(flagName) == nil {
			t.Fatalf("export-components has no --%s flag", flagName)
		}
	}
}

func mustCommandVersion(t *testing.T, value string) artifact.Version {
	t.Helper()
	version, err := artifact.ParseVersion(value)
	if err != nil {
		t.Fatalf("parse version %q: %v", value, err)
	}
	return version
}

package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/export"
	"github.com/dayvidpham/pasture/internal/types"
)

// runBundleExport builds the nine immutable component archives and
// the component-set document a release producer consumes. The bundle source is
// injected so tests exercise this exact production path with small synthetic
// bundles instead of the embedded target assets.
func runBundleExport(cmd *cobra.Command, source export.BundleSource) error {
	versionValue, _ := cmd.Flags().GetString("version")
	outValue, _ := cmd.Flags().GetString("out")
	if versionValue == "" {
		return fmt.Errorf("bundle export: --version is required; pass the release version the assets are named for, for example 1.4.0")
	}
	if outValue == "" {
		return fmt.Errorf("bundle export: --out is required; pass a new directory path to claim for the exported assets")
	}
	version, err := artifact.ParseVersion(versionValue)
	if err != nil {
		return fmt.Errorf("bundle export: --version %q is not a release version: %w", versionValue, err)
	}
	result, err := export.Export(cmd.Context(), export.Request{Version: version, OutDir: outValue}, source)
	if err != nil {
		return err
	}
	exportJSON, _ := cmd.Flags().GetBool("json")
	if exportJSON || resolveFormat() == types.OutputJSON {
		return writeJSON(cmd.OutOrStdout(), exportReportJSON(result))
	}
	return writeExportText(cmd.OutOrStdout(), result)
}

type exportCellJSON struct {
	Component string `json:"component"`
	Asset     string `json:"asset"`
	BundleID  string `json:"bundle_id"`
	Digest    string `json:"digest"`
	Bytes     int64  `json:"bytes"`
	Members   int    `json:"members"`
}

type exportReport struct {
	Version       string           `json:"version"`
	OutputDir     string           `json:"output_dir"`
	ComponentSet  string           `json:"component_set"`
	ArchiveFormat string           `json:"archive_format"`
	Cells         []exportCellJSON `json:"cells"`
}

func exportReportJSON(result export.Result) exportReport {
	cells := make([]exportCellJSON, 0, len(result.Cells))
	for _, c := range result.Cells {
		cells = append(cells, exportCellJSON{
			Component: c.ID.String(),
			Asset:     c.Asset,
			BundleID:  c.BundleID.String(),
			Digest:    c.Digest.String(),
			Bytes:     c.Size,
			Members:   c.Members,
		})
	}
	return exportReport{
		Version:       result.Version.String(),
		OutputDir:     result.OutDir,
		ComponentSet:  result.ComponentSetPath,
		ArchiveFormat: export.ArchiveFormat,
		Cells:         cells,
	}
}

func writeExportText(w io.Writer, result export.Result) error {
	if _, err := fmt.Fprintf(w, "exported %d components for %s into %s\n", len(result.Cells), result.Version, result.OutDir); err != nil {
		return fmt.Errorf("component export output: write header: %w", err)
	}
	for _, c := range result.Cells {
		if _, err := fmt.Fprintf(w, "  %-20s %-34s %s %d bytes %d members\n  %s\n",
			c.ID, c.Asset, c.Digest, c.Size, c.Members, c.BundleID); err != nil {
			return fmt.Errorf("component export output: write row %s: %w", c.ID, err)
		}
	}
	if _, err := fmt.Fprintf(w, "component set: %s\n", result.ComponentSetPath); err != nil {
		return fmt.Errorf("component export output: write component-set line: %w", err)
	}
	return nil
}

func newBundleExportCommand(source export.BundleSource) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the nine release component archives and their component set",
		Long: `export writes one immutable component archive per harness/extension cell, plus
the component-set document a release producer consumes.

  pasture bundle export --version 1.4.0 --out ./build/1.4.0

Every archive is built from the same embedded target descriptors the installer
activates: its members, their paths, and their permission modes come from that
cell's bundle manifest, and the component set records each cell's exact bundle
identity. The output is byte-identical across runs for the same Pasture build,
and each written archive is re-read and checked against its bundle manifest
before the export is reported.

--out must not already exist; it is created and, on any failure, removed, so a
partial set can never be published. The archive format is specified in
docs/component-archive-format.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBundleExport(cmd, source)
		},
	}
	cmd.Flags().String("version", "", "Release version the assets are named for, without a leading v (for example 1.4.0)")
	cmd.Flags().String("out", "", "New output directory to claim; it must not already exist")
	cmd.Flags().Bool("json", false, "Write the deterministic export report document")
	return cmd
}

var bundleExportCmd = newBundleExportCommand(export.EmbeddedBundles)

func init() {
	bundleCmd.AddCommand(bundleExportCmd)
}

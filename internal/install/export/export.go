package export

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/artifact"
)

// CellBundle pairs one canonical component coordinate with the exact target
// bundle the installer would activate for it.
type CellBundle struct {
	ID     artifact.ComponentID
	Bundle artifact.Bundle
}

// BundleSource yields the nine canonical cell bundles. Production wiring reads
// the embedded target descriptors; tests inject small synthetic bundles.
type BundleSource func() ([]CellBundle, error)

// Request is one complete export: the immutable release version the assets are
// named for, and the output directory to claim.
type Request struct {
	Version artifact.Version
	OutDir  string
}

// CellResult reports what was written for one component coordinate.
type CellResult struct {
	ID          artifact.ComponentID
	Asset       string
	ArchivePath string
	BundleID    artifact.BundleID
	Digest      artifact.Digest
	Size        int64
	Members     int
}

// Result reports a complete export.
type Result struct {
	Version          artifact.Version
	OutDir           string
	ComponentSetPath string
	Cells            []CellResult
}

// Export writes one component archive per canonical cell into a freshly
// claimed output directory, verifies each written archive against the bundle
// manifest it was built from, and writes the component-set document the
// aggregate release producer consumes. A failed export leaves no directory
// behind, so a partial set can never be published.
func Export(ctx context.Context, request Request, source BundleSource) (result Result, err error) {
	if ctx == nil {
		return Result{}, exportFault(
			"component export", "a non-nil context", "the caller passed a nil context",
			"cancellation could not be observed", "pass the command context", fs.ErrInvalid)
	}
	if request.Version.String() == "" {
		return Result{}, exportFault(
			"component export", "a parsed release version", "the request version was not constructed",
			"the assets could not be tied to one immutable release",
			"parse the version with artifact.ParseVersion", fs.ErrInvalid)
	}
	if source == nil {
		return Result{}, exportFault(
			"component export", "a bundle source", "the bundle source is nil",
			"no target bundles could be read", "pass EmbeddedBundles or a test source", fs.ErrInvalid)
	}
	bundles, err := indexBundles(source)
	if err != nil {
		return Result{}, err
	}
	outDir, err := filepath.Abs(request.OutDir)
	if err != nil {
		return Result{}, exportFault(
			"component export", "an absolute output directory",
			fmt.Sprintf("output directory %q could not be resolved: %v", request.OutDir, err),
			"the destination could not be claimed", "pass a valid new directory path", err)
	}
	if err := os.Mkdir(outDir, 0o755); err != nil {
		return Result{}, exportFault(
			"component export", "a new, unclaimed output directory",
			fmt.Sprintf("output directory %q could not be created: %v", outDir, err),
			"any existing export is preserved and will not be overwritten",
			"choose a new directory path whose parent already exists", err)
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		if cleanupErr := os.RemoveAll(outDir); cleanupErr != nil {
			err = exportFault(
				"component export cleanup", "the incomplete output directory is removed",
				fmt.Sprintf("directory %q could not be removed: %v", outDir, cleanupErr),
				"partial export bytes remain and must not be published",
				"remove the directory manually after repairing permissions; the original failure was: "+err.Error(), cleanupErr)
		}
	}()

	cells := make([]CellResult, 0, len(bundles))
	for _, id := range artifact.ComponentIDs() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, exportFault(
				"component export", "the export runs to completion",
				fmt.Sprintf("the context was cancelled before component %s was written: %v", id, ctxErr),
				"the incomplete export directory is removed and nothing is published",
				"rerun the export without cancelling it", ctxErr)
		}
		bundle, ok := bundles[id]
		if !ok {
			return Result{}, exportFault(
				"component export", "the bundle source yields every canonical component",
				fmt.Sprintf("the bundle source omitted component %s", id),
				"the export would cover less than the complete installation matrix",
				"provide one bundle for every artifact.ComponentIDs entry", fs.ErrNotExist)
		}
		cellResult, cellErr := writeCell(request.Version, outDir, id, bundle)
		if cellErr != nil {
			return Result{}, cellErr
		}
		cells = append(cells, cellResult)
	}

	document, err := MarshalComponentSet(cells)
	if err != nil {
		return Result{}, err
	}
	componentSetPath := filepath.Join(outDir, ComponentSetFilename)
	if err := os.WriteFile(componentSetPath, document, 0o644); err != nil {
		return Result{}, exportFault(
			"component export", "the component set is written",
			fmt.Sprintf("component set %q could not be written: %v", componentSetPath, err),
			"the incomplete export directory is removed and nothing is published",
			"repair output filesystem capacity and permissions, then retry with a new path", err)
	}
	complete = true
	return Result{Version: request.Version, OutDir: outDir, ComponentSetPath: componentSetPath, Cells: cells}, nil
}

// writeCell writes one archive and re-reads it, proving the written bytes carry
// exactly the members, modes, and digests the bundle manifest declares.
func writeCell(version artifact.Version, outDir string, id artifact.ComponentID, bundle artifact.Bundle) (CellResult, error) {
	asset, err := AssetBasename(version, id)
	if err != nil {
		return CellResult{}, err
	}
	expected, err := BundleMembers(bundle)
	if err != nil {
		return CellResult{}, err
	}
	archivePath := filepath.Join(outDir, asset)
	file, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return CellResult{}, exportFault(
			"component archive write", "each asset basename is claimed exactly once",
			fmt.Sprintf("archive %q could not be created for component %s: %v", archivePath, id, err),
			"the incomplete export directory is removed and nothing is published",
			"repair output filesystem capacity and permissions, then retry with a new path", err)
	}
	writeErr := WriteArchive(file, bundle)
	closeErr := file.Close()
	if writeErr != nil {
		return CellResult{}, writeErr
	}
	if closeErr != nil {
		return CellResult{}, exportFault(
			"component archive write", "each archive is flushed and closed",
			fmt.Sprintf("archive %q could not be closed for component %s: %v", archivePath, id, closeErr),
			"the incomplete export directory is removed and nothing is published",
			"repair output filesystem capacity, then retry with a new path", closeErr)
	}
	content, err := os.ReadFile(archivePath)
	if err != nil {
		return CellResult{}, exportFault(
			"component archive verification", "each written archive is readable",
			fmt.Sprintf("archive %q could not be re-read for component %s: %v", archivePath, id, err),
			"the archive could not be proven to match its target bundle",
			"repair output filesystem permissions, then retry with a new path", err)
	}
	if err := verifyMembers(id, archivePath, content, expected); err != nil {
		return CellResult{}, err
	}
	return CellResult{
		ID:          id,
		Asset:       asset,
		ArchivePath: archivePath,
		BundleID:    bundle.ID(),
		Digest:      artifact.DigestBytes(content),
		Size:        int64(len(content)),
		Members:     len(expected),
	}, nil
}

func indexBundles(source BundleSource) (map[artifact.ComponentID]artifact.Bundle, error) {
	cells, err := source()
	if err != nil {
		return nil, err
	}
	index := make(map[artifact.ComponentID]artifact.Bundle, len(cells))
	for _, item := range cells {
		if !item.ID.IsValid() {
			return nil, exportFault(
				"component export", "every source cell names a canonical component",
				"the bundle source yielded a zero or unsupported component coordinate",
				"the export could not be addressed to an installation cell",
				"yield one cell per artifact.ComponentIDs entry", fs.ErrInvalid)
		}
		if _, duplicate := index[item.ID]; duplicate {
			return nil, exportFault(
				"component export", "every component appears at most once",
				fmt.Sprintf("the bundle source yielded component %s more than once", item.ID),
				"the exported bytes for that cell would be ambiguous",
				"yield each artifact.ComponentIDs entry exactly once", fs.ErrExist)
		}
		index[item.ID] = item.Bundle
	}
	return index, nil
}

func exportFault(operation, rule, reason, impact, fix string, cause error) error {
	return archiveFault(operation, rule, reason, impact, fix, cause)
}

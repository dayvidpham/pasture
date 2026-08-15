package opencode

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/dayvidpham/pasture/artifact"
)

//go:embed all:assets
var assetsFS embed.FS

func componentFromAssets(extension artifact.Extension, root string, defaultEnabled bool) (Component, error) {
	source, err := fs.Sub(assetsFS, root)
	if err != nil {
		return Component{}, fmt.Errorf("open embedded root %q: %w", root, err)
	}
	mode, err := artifact.NewMode(0o644)
	if err != nil {
		return Component{}, err
	}
	var entries []artifact.Entry
	err = fs.WalkDir(source, ".", func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() {
			return nil
		}
		if item.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("embedded asset %q is a symlink; regenerate from regular harness output", name)
		}
		content, readErr := fs.ReadFile(source, name)
		if readErr != nil {
			return readErr
		}
		assetPath, pathErr := artifact.NewPath(name)
		if pathErr != nil {
			return fmt.Errorf("embedded asset path %q is unsafe: %w", name, pathErr)
		}
		entry, entryErr := artifact.NewFileEntry(assetPath, mode, artifact.DigestBytes(content))
		if entryErr != nil {
			return entryErr
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return Component{}, fmt.Errorf("walk embedded root %q: %w", root, err)
	}
	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		return Component{}, fmt.Errorf("build manifest for %q: %w", root, err)
	}
	bundle, err := artifact.NewBundle(source, manifest)
	if err != nil {
		return Component{}, fmt.Errorf("snapshot embedded root %q: %w", root, err)
	}
	id, err := artifact.NewComponentID(artifact.HarnessOpenCode, extension)
	if err != nil {
		return Component{}, err
	}
	return NewComponent(id, bundle, defaultEnabled)
}

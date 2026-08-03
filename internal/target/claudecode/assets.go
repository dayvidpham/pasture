package claudecode

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

// assetsFS carries the generated Claude Code plugin trees the installed CLI
// materializes without access to the source checkout. The `all:` prefix embeds
// dotfiles such as each plugin's `.claude-plugin/plugin.json`, which the default
// go:embed pattern would otherwise omit.
//
//go:embed all:assets
var assetsFS embed.FS

// pluginRoot is the embedded path to one generated Claude Code plugin tree.
const (
	skillsPluginRoot = "assets/pasture-skills"
	agentsPluginRoot = "assets/pasture-agents"
	hooksPluginRoot  = "assets/pasture-hooks"
)

// bundleForPluginRoot builds an immutable, content-addressed artifact.Bundle
// from the embedded plugin tree rooted at pluginRoot.
//
// Modes are assigned deterministically from the leaf name so the same embedded
// bytes always produce the same manifest and BundleID: shell scripts receive
// 0755 because they are executed in place during a hook, and every other regular
// file receives 0644. embed.FS discards the source executable bit, so the mode
// must be reconstructed from a stable rule rather than read from the filesystem.
func bundleForPluginRoot(pluginRoot string) (artifact.Bundle, error) {
	sub, err := fs.Sub(assetsFS, pluginRoot)
	if err != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"claudecode.bundleForPluginRoot: cannot open embedded plugin root %q — "+
				"the generated Claude Code asset tree is missing or misnamed at build time; "+
				"regenerate the embedded assets so %q exists: %w",
			pluginRoot, pluginRoot, err,
		)
	}

	var entries []artifact.Entry
	walkErr := fs.WalkDir(sub, ".", func(name string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk embedded plugin tree at %q: %w", name, err)
		}
		if dirEntry.IsDir() {
			return nil
		}
		content, readErr := fs.ReadFile(sub, name)
		if readErr != nil {
			return fmt.Errorf("read embedded plugin file %q: %w", name, readErr)
		}
		path, pathErr := artifact.NewPath(name)
		if pathErr != nil {
			return fmt.Errorf("embedded plugin path %q is not a canonical artifact path: %w", name, pathErr)
		}
		mode, modeErr := artifact.NewMode(modeForLeaf(name))
		if modeErr != nil {
			return fmt.Errorf("assign mode for embedded plugin file %q: %w", name, modeErr)
		}
		entry, entryErr := artifact.NewFileEntry(path, mode, artifact.DigestBytes(content))
		if entryErr != nil {
			return fmt.Errorf("build manifest entry for embedded plugin file %q: %w", name, entryErr)
		}
		entries = append(entries, entry)
		return nil
	})
	if walkErr != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"claudecode.bundleForPluginRoot(%s): enumerating the generated plugin tree failed — "+
				"the embedded Claude Code assets are incomplete or contain an unsupported entry; "+
				"regenerate the embedded assets: %w",
			pluginRoot, walkErr,
		)
	}
	if len(entries) == 0 {
		return artifact.Bundle{}, fmt.Errorf(
			"claudecode.bundleForPluginRoot(%s): the generated plugin tree declares no files — "+
				"a Claude Code plugin bundle must carry at least its .claude-plugin/plugin.json manifest; "+
				"regenerate the embedded assets so the plugin root is non-empty",
			pluginRoot,
		)
	}

	manifest, manifestErr := artifact.NewManifest(entries...)
	if manifestErr != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"claudecode.bundleForPluginRoot(%s): the generated plugin tree does not form a valid manifest — "+
				"two files collide on one path or a file shadows a directory; "+
				"regenerate the embedded assets so every leaf owns a distinct clean path: %w",
			pluginRoot, manifestErr,
		)
	}
	bundle, bundleErr := artifact.NewBundle(sub, manifest)
	if bundleErr != nil {
		return artifact.Bundle{}, fmt.Errorf(
			"claudecode.bundleForPluginRoot(%s): the generated plugin tree failed content validation — "+
				"the embedded bytes and the derived manifest disagree, which must never happen for a self-embedded tree; "+
				"regenerate the embedded assets: %w",
			pluginRoot, bundleErr,
		)
	}
	return bundle, nil
}

// modeForLeaf returns the deterministic permission bits for a generated plugin
// file. Shell scripts are executable; everything else is a plain 0644 file.
func modeForLeaf(name string) uint32 {
	if strings.HasSuffix(name, ".sh") {
		return 0o755
	}
	return 0o644
}

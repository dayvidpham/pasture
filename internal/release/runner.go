package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/types"
)

// ─── ReleaseOptions ──────────────────────────────────────────────────────────

// ReleaseOptions controls the release workflow for a single repository.
type ReleaseOptions struct {
	// BumpKind specifies which semver component to increment (major, minor, patch).
	BumpKind types.BumpKind
	// DryRun, when true, prints what would happen without making changes.
	DryRun bool
	// Sync, when true, aligns all version files to the canonical before bumping.
	Sync bool
	// NoChangelog skips changelog generation.
	NoChangelog bool
	// NoCommit skips git commit.
	NoCommit bool
	// NoTag skips git tag.
	NoTag bool
	// RepoRoot is the absolute path to the repository root.
	RepoRoot string
	// Plugin, when non-empty, names the plugin whose entry in a registered
	// CROSS-REPO marketplace.json should be synced to the new version AFTER
	// the in-repo commit/tag succeed (mirrors aura-release --plugin). The
	// marketplace path is looked up via the plugin registry (see RegistryPath).
	Plugin string
	// RegistryPath overrides the plugin registry location used to resolve the
	// cross-repo marketplace path for Plugin. When empty, DefaultRegistryPath()
	// is used. Exposed primarily so integration tests can point at a temp
	// registry; production callers leave it empty.
	RegistryPath string
}

// RunRelease executes the full release workflow for a single repository.
//
// Workflow:
//  1. Discover version files.
//  2. Pre-flight: refuse to release from a detached HEAD.
//  3. Validate working tree (unless dry-run).
//  4. Optionally sync version drift.
//  5. Bump version across all files.
//  6. Generate changelog.
//  7. Git commit.
//  8. Git tag.
//  9. Optionally sync a plugin's entry in a cross-repo marketplace (--plugin).
func RunRelease(opts ReleaseOptions) error {
	prefix := ""
	if opts.DryRun {
		prefix = "[dry-run] "
	}

	// 1. Discover version files.
	files, err := DiscoverVersionFiles(opts.RepoRoot)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf(
			"validation error: no version files found in %s — "+
				"pasture-release looks for: pyproject.toml, package.json, "+
				".claude-plugin/plugin.json, .claude-plugin/marketplace.json",
			opts.RepoRoot,
		)
	}

	// 2. Pre-flight: refuse to release from a detached HEAD (mirrors
	// aura-release:617-621). A detached HEAD has no branch to advance, so a
	// release commit/tag would be stranded. This guard runs even in dry-run so
	// previews surface the same blocker an actual release would hit.
	detached, err := GitIsDetachedHead(opts.RepoRoot)
	if err != nil {
		return err
	}
	if detached {
		return fmt.Errorf(
			"validation error: cannot release from a detached HEAD in %s — "+
				"HEAD points at a commit, not a branch, so the release commit/tag "+
				"would be stranded — "+
				"switch to a branch first, e.g. 'git checkout main', then re-run",
			opts.RepoRoot,
		)
	}

	// 3. Validate working tree.
	if !opts.DryRun {
		status, err := GitStatus(opts.RepoRoot)
		if err != nil {
			return err
		}
		if workingTreeDirty(status) {
			return fmt.Errorf(
				"validation error: working tree has uncommitted changes in %s — "+
					"commit or stash your changes before releasing",
				opts.RepoRoot,
			)
		}
	}

	// 4. Read current versions and optionally sync.
	versions := make(map[string]string, len(files))
	for _, vf := range files {
		v, err := vf.Read()
		if err != nil {
			return err
		}
		versions[vf.Name()] = v
	}

	canonical := versions[files[0].Name()]

	if !versionsConsistent(versions) {
		if opts.Sync {
			fmt.Printf("%sSyncing all files to canonical version: %s\n", prefix, canonical)
			for _, vf := range files[1:] {
				if err := vf.Write(canonical, opts.DryRun); err != nil {
					return err
				}
			}
		} else {
			var drifts []string
			for name, v := range versions {
				drifts = append(drifts, fmt.Sprintf("%s=%s", name, v))
			}
			return fmt.Errorf(
				"validation error: version drift detected across files — %s — "+
					"run with --sync to align all files before bumping",
				strings.Join(drifts, ", "),
			)
		}
	}

	// 5. Bump version.
	if !opts.BumpKind.IsValid() {
		return fmt.Errorf(
			"validation error: unknown bump kind %q — "+
				"expected one of: major, minor, patch",
			opts.BumpKind,
		)
	}
	current, err := ParseSemVer(canonical)
	if err != nil {
		return err
	}
	var bumped SemVer
	switch opts.BumpKind {
	case types.BumpMajor:
		bumped = SemVer{Major: current.Major + 1, Minor: 0, Patch: 0}
	case types.BumpMinor:
		bumped = SemVer{Major: current.Major, Minor: current.Minor + 1, Patch: 0}
	case types.BumpPatch:
		bumped = SemVer{Major: current.Major, Minor: current.Minor, Patch: current.Patch + 1}
	}
	bumpedStr := bumped.String()
	tagName := "v" + bumpedStr

	fmt.Printf("%sBumping: %s -> %s (%s)\n", prefix, canonical, bumpedStr, opts.BumpKind)

	for _, vf := range files {
		fmt.Printf("%sUpdating %s\n", prefix, vf.Name())
		if err := vf.Write(bumpedStr, opts.DryRun); err != nil {
			_ = GitRollback(opts.RepoRoot, tagName)
			return err
		}
	}

	// 6. Changelog.
	changelogPath := filepath.Join(opts.RepoRoot, "CHANGELOG.md")
	if !opts.NoChangelog {
		entry, err := buildChangelogEntry(opts.RepoRoot, bumped)
		if err != nil {
			_ = GitRollback(opts.RepoRoot, tagName)
			return err
		}
		if opts.DryRun {
			fmt.Printf("%sChangelog entry:\n%s\n", prefix, entry)
		} else {
			if err := prependChangelog(changelogPath, entry); err != nil {
				_ = GitRollback(opts.RepoRoot, tagName)
				return err
			}
			fmt.Printf("%sUpdated CHANGELOG.md\n", prefix)
		}
	}

	// 7. Git commit.
	if !opts.NoCommit {
		var stageFiles []string
		for _, vf := range files {
			rel, err := filepath.Rel(opts.RepoRoot, vf.Path())
			if err != nil {
				rel = vf.Path()
			}
			stageFiles = append(stageFiles, rel)
		}
		if !opts.NoChangelog {
			rel, _ := filepath.Rel(opts.RepoRoot, changelogPath)
			stageFiles = append(stageFiles, rel)
		}
		if opts.DryRun {
			fmt.Printf("%sWould commit: chore: release %s\n", prefix, tagName)
		} else {
			if err := GitCommit(opts.RepoRoot, stageFiles, "chore: release "+tagName); err != nil {
				_ = GitRollback(opts.RepoRoot, tagName)
				return err
			}
			fmt.Printf("%sCommitted: chore: release %s\n", prefix, tagName)
		}
	}

	// 8. Git tag.
	if !opts.NoTag {
		if opts.DryRun {
			fmt.Printf("%sWould tag: %s\n", prefix, tagName)
		} else {
			if err := GitTag(opts.RepoRoot, tagName, "Release "+bumpedStr); err != nil {
				// On tag failure, delete the tag and restore the working tree.
				// The release commit (step 7) is intentionally left in place,
				// matching aura-release; see GitRollback.
				_ = GitRollback(opts.RepoRoot, tagName)
				return err
			}
			fmt.Printf("%sTagged: %s\n", prefix, tagName)
		}
	}

	// 9. Cross-repo marketplace sync — AFTER commit/tag (mirrors aura-release).
	// When --plugin is set, sync the named plugin's entry in a registered
	// (possibly cross-repo) marketplace.json to the new version, leaving that
	// marketplace's own metadata.version untouched.
	if opts.Plugin != "" {
		if err := syncCrossRepoMarketplace(opts, files, bumpedStr, prefix); err != nil {
			return err
		}
	}

	fmt.Printf("\n%sRelease %s complete!\n", prefix, tagName)
	if !opts.DryRun && !opts.NoCommit {
		fmt.Println("Next: git push && git push --tags")
	}
	return nil
}

// syncCrossRepoMarketplace resolves opts.Plugin to its registered marketplace
// path and writes the new version into that marketplace's plugins[<name>]
// entry. It runs AFTER the in-repo commit/tag so the cross-repo write does not
// pollute this repo's release commit.
//
// Double-bump guard: if the resolved marketplace path is the SAME file that was
// already discovered and bumped in step 4 (i.e. the marketplace lives inside
// this repo and was bumped as part of the normal flow), the per-plugin write is
// skipped — the in-repo bump already wrote metadata.version and re-writing
// plugins[].version here would be redundant / conflicting. This mirrors
// aura-release:734-743.
//
// discovered are the version files bumped in the main flow; bumpedStr is the
// new version; prefix is the dry-run log prefix.
func syncCrossRepoMarketplace(opts ReleaseOptions, discovered []VersionFile, bumpedStr, prefix string) error {
	registryPath := opts.RegistryPath
	if registryPath == "" {
		registryPath = DefaultRegistryPath()
	}

	var registry PluginRegistry
	if err := registry.Load(registryPath); err != nil {
		return fmt.Errorf(
			"workflow error: --plugin %q was requested but the plugin registry "+
				"at %s could not be loaded — %w — "+
				"create it with 'pasture-release registry init' and register the "+
				"plugin with 'pasture-release registry add', or omit --plugin",
			opts.Plugin, registryPath, err,
		)
	}

	pluginEntry, marketplaceEntry := registry.FindPlugin(opts.Plugin, opts.RepoRoot)
	if pluginEntry == nil || marketplaceEntry == nil {
		return fmt.Errorf(
			"workflow error: --plugin %q has no matching entry in the plugin "+
				"registry at %s — cannot sync its cross-repo marketplace because "+
				"the registry does not know where that marketplace lives — "+
				"register it with 'pasture-release registry add %s --remote <url>', "+
				"or omit --plugin to skip the cross-repo sync",
			opts.Plugin, registryPath, opts.Plugin,
		)
	}

	// Resolve the registered marketplace path to an absolute path for a
	// reliable comparison against the discovered (already-bumped) files.
	resolvedMarketplace, err := filepath.Abs(marketplaceEntry.Path)
	if err != nil {
		resolvedMarketplace = marketplaceEntry.Path
	}

	// Double-bump guard: skip if this marketplace was already bumped in-repo.
	for _, vf := range discovered {
		resolvedDiscovered, dErr := filepath.Abs(vf.Path())
		if dErr != nil {
			resolvedDiscovered = vf.Path()
		}
		if resolvedDiscovered == resolvedMarketplace {
			fmt.Printf(
				"%sSkipping cross-repo marketplace sync for plugin %q: %s was "+
					"already bumped in this repo (double-bump guard)\n",
				prefix, opts.Plugin, resolvedMarketplace,
			)
			return nil
		}
	}

	fmt.Printf(
		"%sSyncing marketplace %s: plugins[%s].version -> %s\n",
		prefix, resolvedMarketplace, opts.Plugin, bumpedStr,
	)
	if err := WritePluginVersion(resolvedMarketplace, opts.Plugin, bumpedStr, opts.DryRun); err != nil {
		return fmt.Errorf(
			"workflow error: failed to sync plugin %q into marketplace %s — %w",
			opts.Plugin, resolvedMarketplace, err,
		)
	}
	return nil
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func versionsConsistent(versions map[string]string) bool {
	var first string
	for _, v := range versions {
		if first == "" {
			first = v
			continue
		}
		if v != first {
			return false
		}
	}
	return true
}

func workingTreeDirty(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Ignore .beads/ paths — this is the pasture-release beads-ignore convention.
		path := line
		if len(line) > 3 {
			path = line[3:]
		}
		if !strings.HasPrefix(path, ".beads/") {
			return true
		}
	}
	return false
}

func buildChangelogEntry(repoRoot string, bumped SemVer) (string, error) {
	latestTag, err := GitLatestVersionTag(repoRoot)
	if err != nil {
		return "", err
	}
	var subjects []string
	if latestTag != "" {
		subjects, err = GitCommitsSince(repoRoot, latestTag)
	} else {
		subjects, err = GitAllCommits(repoRoot)
	}
	if err != nil {
		return "", err
	}

	var commits []ConventionalCommit
	for _, s := range subjects {
		cc, parseErr := ParseConventionalCommit(s)
		if parseErr != nil {
			// Non-conventional commits go in "Other".
			commits = append(commits, ConventionalCommit{
				Type: "other", Scope: "", Description: s, Raw: s,
			})
			continue
		}
		commits = append(commits, *cc)
	}

	return GenerateChangelog(commits, bumped), nil
}

func prependChangelog(path, entry string) error {
	var existing string
	data, err := os.ReadFile(path)
	if err == nil {
		existing = string(data)
	}

	header := "# Changelog\n\n"
	var content string
	if strings.HasPrefix(existing, "# Changelog") {
		idx := strings.Index(existing, "\n")
		rest := strings.TrimLeft(existing[idx+1:], "\n")
		content = header + entry + "\n" + rest
	} else {
		content = header + entry + "\n" + existing
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

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
}

// RunRelease executes the full release workflow for a single repository.
//
// Workflow:
//  1. Discover version files.
//  2. Validate working tree (unless dry-run).
//  3. Optionally sync version drift.
//  4. Bump version across all files.
//  5. Generate changelog.
//  6. Git commit.
//  7. Git tag.
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

	// 2. Validate working tree.
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

	// 3. Read current versions and optionally sync.
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

	// 4. Bump version.
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

	// 5. Changelog.
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

	// 6. Git commit.
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

	// 7. Git tag.
	if !opts.NoTag {
		if opts.DryRun {
			fmt.Printf("%sWould tag: %s\n", prefix, tagName)
		} else {
			if err := GitTag(opts.RepoRoot, tagName, "Release "+bumpedStr); err != nil {
				return err
			}
			fmt.Printf("%sTagged: %s\n", prefix, tagName)
		}
	}

	fmt.Printf("\n%sRelease %s complete!\n", prefix, tagName)
	if !opts.DryRun && !opts.NoCommit {
		fmt.Println("Next: git push && git push --tags")
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
		// Ignore .beads/ paths (same convention as pasture-release).
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

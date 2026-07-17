package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// canonicalRoots is the single, code-owned, closed list of canonical source
// roots (relative to the pasture module root) this package scans: skills
// (which nests every protocol/template/example document under
// skills/protocol, i.e. pasture#47's "fragments and operational protocol
// documents") and agents. CanonicalRoots returns a defensive copy — this is
// the only enumeration accessor, mirroring ir.EnabledHarnessIDs's rationale.
var canonicalRoots = [...]string{"skills", "agents"}

// CanonicalRoots returns a fresh defensive copy of the closed, code-owned
// canonical source-root list every production scan must use. Tests may scan
// a smaller synthetic root set to isolate one behavior, but any call in the
// production code path — including ScanCanonical — uses exactly this set.
func CanonicalRoots() []string {
	return append([]string(nil), canonicalRoots[:]...)
}

// excludedPathSegments is the closed, code-owned exclusion list. It matches
// whole path segments only (never a substring), so a real active file whose
// name merely contains one of these words (e.g. "user-acceptance-testing")
// is never excluded — see TestDiscoverExcludesOnlyExactSegments.
//
//   - "testdata": Go test-fixture convention.
//   - "vendor": vendored third-party content.
//   - ".git": repository metadata.
//   - ".opencode": pasture's generated OpenCode output mirror (see
//     internal/codegen/harness.go's OpenCodeTarget) — full generated output,
//     never a canonical source root.
var excludedPathSegments = map[string]bool{
	"testdata":  true,
	"vendor":    true,
	".git":      true,
	".opencode": true,
}

// isExcludedPath reports whether relPath (slash-separated, relative to the
// scan base directory) contains an excluded path segment anywhere along its
// path.
func isExcludedPath(relPath string) bool {
	for _, segment := range strings.Split(relPath, "/") {
		if excludedPathSegments[segment] {
			return true
		}
	}
	return false
}

// Discover independently walks baseDir/root for every root in roots and
// returns the sorted, deduplicated set of relative (to baseDir,
// slash-separated) Markdown owner paths. It is independent of any
// OwnerManifest by construction — Discover never reads a manifest, so the
// manifest can never become the discovery source (see ReconcileOwners).
//
// Discover rejects a symlinked root, directory, or file with an actionable
// diagnostic (pasture#47 requires "rejecting symlinked owners"), and it
// excludes every path segment named in the closed excludedPathSegments list.
// Only files with a ".md" extension are reported: figures/*.yaml,
// skills/install-cli/.gitkeep, and every other non-Markdown file under a
// canonical root are silently not owners, not silently excluded content.
func Discover(baseDir string, roots []string) ([]string, error) {
	return walkCanonicalRoots(baseDir, roots, true)
}

// discoverAllFiles independently walks every canonical root exactly as
// Discover does (same exclusion list, same symlink rejection), but reports
// every non-excluded file regardless of extension. HashTree uses this — not
// Discover — because pasture#47's byte-for-byte read-only proof must cover
// the whole canonical tree, not only the ".md" owners a classification scan
// parses.
func discoverAllFiles(baseDir string, roots []string) ([]string, error) {
	return walkCanonicalRoots(baseDir, roots, false)
}

// walkCanonicalRoots is the single walk shared by Discover and
// discoverAllFiles. onlyMarkdown selects Discover's owner-discovery
// semantics (".md" files only); the exclusion list, symlink rejection, and
// deterministic sorted output are identical in both modes.
func walkCanonicalRoots(baseDir string, roots []string, onlyMarkdown bool) ([]string, error) {
	if len(roots) == 0 {
		return nil, diagnostic(
			"no canonical roots were supplied",
			"discovery has nothing to walk without at least one root",
			"scan.Discover", "root discovery",
			"no owners can be found and every downstream classification step would run on an empty set",
			"call Discover with scan.CanonicalRoots() (or an explicit non-empty root list in a test)",
			nil,
		)
	}

	seen := make(map[string]bool)
	var discovered []string

	for _, root := range roots {
		rootPath := filepath.Join(baseDir, root)
		info, err := os.Lstat(rootPath)
		if err != nil {
			return nil, diagnostic(
				fmt.Sprintf("canonical root %q could not be inspected", root),
				"every code-owned canonical root must exist under the scan base directory",
				"scan.Discover:"+rootPath, "root discovery",
				"the scan cannot prove it walked every canonical source root",
				"create the missing root or remove it from the code-owned canonical root list",
				err,
			)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, diagnostic(
				fmt.Sprintf("canonical root %q is a symlink", root),
				"pasture#47 requires rejecting symlinked owners so a scan can never be redirected outside its canonical roots",
				"scan.Discover:"+rootPath, "root discovery",
				"the scan cannot trust the tree it would walk",
				"replace the symlink with a real directory",
				nil,
			)
		}
		if !info.IsDir() {
			return nil, diagnostic(
				fmt.Sprintf("canonical root %q is not a directory", root),
				"a canonical source root must be a directory to walk",
				"scan.Discover:"+rootPath, "root discovery",
				"the scan cannot enumerate owners under a non-directory root",
				"point the canonical root at a directory",
				nil,
			)
		}

		walkErr := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(baseDir, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)

			if entry.Type()&fs.ModeSymlink != 0 {
				return diagnostic(
					fmt.Sprintf("owner %q is a symlink", rel),
					"pasture#47 requires rejecting symlinked owners so a scan can never be redirected outside its canonical roots",
					"scan.Discover:"+path, "root discovery",
					"the scan cannot trust the tree it would walk",
					"replace the symlink with a real file or directory",
					nil,
				)
			}
			if entry.IsDir() {
				if path != rootPath && isExcludedPath(rel) {
					return filepath.SkipDir
				}
				return nil
			}
			if isExcludedPath(rel) {
				return nil
			}
			if onlyMarkdown && !strings.EqualFold(filepath.Ext(rel), ".md") {
				return nil
			}
			if seen[rel] {
				return nil
			}
			seen[rel] = true
			discovered = append(discovered, rel)
			return nil
		})
		if walkErr != nil {
			if _, ok := walkErr.(*Diagnostic); ok {
				return nil, walkErr
			}
			return nil, diagnostic(
				fmt.Sprintf("walking canonical root %q failed", root),
				"root discovery must be able to enumerate every file under a canonical root",
				"scan.Discover:"+rootPath, "root discovery",
				"the scan cannot prove it discovered every active owner",
				"resolve the filesystem error and re-run the scan",
				walkErr,
			)
		}
	}

	sort.Strings(discovered)
	return discovered, nil
}

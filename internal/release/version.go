// Package release provides version management, changelog generation, git
// helpers, and plugin registry operations for pasture-release.
package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dayvidpham/pasture/internal/types"
)

// ─── SemVer ──────────────────────────────────────────────────────────────────

// SemVer represents a semantic version number (major.minor.patch).
type SemVer struct {
	Major int
	Minor int
	Patch int
}

// semverRE matches a bare X.Y.Z semver string.
var semverRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// ParseSemVer parses a semver string of the form "X.Y.Z".
// Returns an error if the string does not match.
func ParseSemVer(s string) (SemVer, error) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return SemVer{}, fmt.Errorf(
			"validation error: invalid semver %q — expected X.Y.Z format (e.g. 1.2.3); "+
				"check the version field in your manifest file",
			s,
		)
	}
	var v SemVer
	fmt.Sscan(m[1], &v.Major)
	fmt.Sscan(m[2], &v.Minor)
	fmt.Sscan(m[3], &v.Patch)
	return v, nil
}

// Bump returns a new SemVer with the specified component incremented.
// Minor and Patch are reset to 0 on Major bump; Patch is reset on Minor bump.
func (v SemVer) Bump(kind types.BumpKind) SemVer {
	switch kind {
	case types.BumpMajor:
		return SemVer{Major: v.Major + 1, Minor: 0, Patch: 0}
	case types.BumpMinor:
		return SemVer{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
	case types.BumpPatch:
		return SemVer{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		panic(fmt.Sprintf("release.SemVer.Bump: unknown BumpKind %q", kind))
	}
}

// String returns the dotted "X.Y.Z" representation.
func (v SemVer) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ─── VersionFile interface ────────────────────────────────────────────────────

// VersionFile is implemented by any file that stores a semver version string.
// Each implementation knows how to locate the version within its own format.
type VersionFile interface {
	// Name returns the display name / relative path of the file.
	Name() string
	// Path returns the absolute filesystem path.
	Path() string
	// Read extracts the current version string from the file.
	Read() (string, error)
	// Write persists a new version string to the file.
	// When dryRun is true, the change is printed but not applied.
	Write(version string, dryRun bool) error
}

// ─── pyproject.toml ──────────────────────────────────────────────────────────

// pyprojectVersionRE matches `version = "X.Y.Z"` as a bare line.
// We validate it is inside [project] via findPyprojectVersion / replacePyprojectVersion.
var pyprojectVersionRE = regexp.MustCompile(`(?m)^version\s*=\s*"(\d+\.\d+\.\d+)"`)

// projectSectionRE matches the [project] section header.
var projectSectionRE = regexp.MustCompile(`(?m)^\[project\]`)

// PyprojectVersionFile reads/writes the version in a pyproject.toml [project]
// section.
type PyprojectVersionFile struct {
	name string
	path string
}

// NewPyprojectVersionFile constructs a PyprojectVersionFile.
func NewPyprojectVersionFile(name, path string) *PyprojectVersionFile {
	return &PyprojectVersionFile{name: name, path: path}
}

func (f *PyprojectVersionFile) Name() string { return f.name }
func (f *PyprojectVersionFile) Path() string { return f.path }

// pyprojectVersion extracts the version from the [project] section of a
// pyproject.toml byte slice. Returns ("", false) if not found.
func pyprojectVersion(data []byte) (string, bool) {
	projectIdx := projectSectionRE.FindIndex(data)
	if projectIdx == nil {
		return "", false
	}
	// Find next section header after [project].
	rest := data[projectIdx[1]:]
	nextSectionRE := regexp.MustCompile(`(?m)^\[`)
	nextIdx := nextSectionRE.FindIndex(rest)
	var block []byte
	if nextIdx != nil {
		block = rest[:nextIdx[0]]
	} else {
		block = rest
	}
	m := pyprojectVersionRE.FindSubmatch(block)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func (f *PyprojectVersionFile) Read() (string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", fmt.Errorf(
			"validation error: cannot read %s — %w — "+
				"ensure the file exists and is readable",
			f.name, err,
		)
	}
	ver, ok := pyprojectVersion(data)
	if !ok {
		return "", fmt.Errorf(
			"validation error: no version field in [project] section of %s — "+
				"add `version = \"X.Y.Z\"` under [project] in %s",
			f.name, f.path,
		)
	}
	return ver, nil
}

func (f *PyprojectVersionFile) Write(version string, dryRun bool) error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("validation error: cannot read %s — %w", f.name, err)
	}
	_, ok := pyprojectVersion(data)
	if !ok {
		return fmt.Errorf(
			"validation error: no version field in [project] section of %s", f.name,
		)
	}
	if dryRun {
		fmt.Printf("[dry-run] would write version %s to %s\n", version, f.name)
		return nil
	}
	// Replace the version value inside the [project] block only.
	// Strategy: replace the first occurrence of version = "OLD" after [project].
	projectIdx := projectSectionRE.FindIndex(data)
	rest := data[projectIdx[1]:]
	nextSectionRE := regexp.MustCompile(`(?m)^\[`)
	nextIdx := nextSectionRE.FindIndex(rest)
	var blockEnd int
	if nextIdx != nil {
		blockEnd = projectIdx[1] + nextIdx[0]
	} else {
		blockEnd = len(data)
	}
	block := data[projectIdx[1]:blockEnd]
	// Replace only the first match in the block.
	replaced := pyprojectVersionRE.ReplaceAllLiteral(block, []byte(`version = "`+version+`"`))
	var updated []byte
	updated = append(updated, data[:projectIdx[1]]...)
	updated = append(updated, replaced...)
	updated = append(updated, data[blockEnd:]...)
	if err := os.WriteFile(f.path, updated, 0o644); err != nil {
		return fmt.Errorf("validation error: cannot write %s — %w", f.name, err)
	}
	return nil
}

// ─── package.json / plugin.json ──────────────────────────────────────────────

// JsonVersionFile reads/writes the top-level "version" field of a JSON file.
// Used for package.json and .claude-plugin/plugin.json.
type JsonVersionFile struct {
	name string
	path string
}

// NewJsonVersionFile constructs a JsonVersionFile.
func NewJsonVersionFile(name, path string) *JsonVersionFile {
	return &JsonVersionFile{name: name, path: path}
}

func (f *JsonVersionFile) Name() string { return f.name }
func (f *JsonVersionFile) Path() string { return f.path }

func (f *JsonVersionFile) Read() (string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", fmt.Errorf("validation error: cannot read %s — %w", f.name, err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf(
			"validation error: %s is not valid JSON — %w — fix JSON syntax and retry",
			f.name, err,
		)
	}
	ver, ok := obj["version"].(string)
	if !ok {
		return "", fmt.Errorf(
			`validation error: %s has no top-level "version" string field — `+
				`add "version": "X.Y.Z" to %s`,
			f.name, f.path,
		)
	}
	return ver, nil
}

func (f *JsonVersionFile) Write(version string, dryRun bool) error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("validation error: cannot read %s — %w", f.name, err)
	}
	// Use ordered replacement to preserve key order and formatting
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("validation error: %s is not valid JSON — %w", f.name, err)
	}
	obj["version"] = version
	if dryRun {
		fmt.Printf("[dry-run] would write version %s to %s\n", version, f.name)
		return nil
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("validation error: cannot marshal %s — %w", f.name, err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(f.path, out, 0o644); err != nil {
		return fmt.Errorf("validation error: cannot write %s — %w", f.name, err)
	}
	return nil
}

// ─── marketplace.json ────────────────────────────────────────────────────────

// MarketplaceVersionFile reads/writes the metadata.version field in a
// marketplace.json file.
type MarketplaceVersionFile struct {
	name string
	path string
}

// NewMarketplaceVersionFile constructs a MarketplaceVersionFile.
func NewMarketplaceVersionFile(name, path string) *MarketplaceVersionFile {
	return &MarketplaceVersionFile{name: name, path: path}
}

func (f *MarketplaceVersionFile) Name() string { return f.name }
func (f *MarketplaceVersionFile) Path() string { return f.path }

func (f *MarketplaceVersionFile) Read() (string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", fmt.Errorf("validation error: cannot read %s — %w", f.name, err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf(
			"validation error: %s is not valid JSON — %w", f.name, err,
		)
	}
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf(
			`validation error: %s missing "metadata" object — `+
				`add "metadata": {"version": "X.Y.Z"} to %s`,
			f.name, f.path,
		)
	}
	ver, ok := meta["version"].(string)
	if !ok {
		return "", fmt.Errorf(
			`validation error: %s missing metadata.version string — `+
				`add "version": "X.Y.Z" inside the "metadata" object in %s`,
			f.name, f.path,
		)
	}
	return ver, nil
}

func (f *MarketplaceVersionFile) Write(version string, dryRun bool) error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("validation error: cannot read %s — %w", f.name, err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("validation error: %s is not valid JSON — %w", f.name, err)
	}
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf(
			`validation error: %s missing "metadata" object`, f.name,
		)
	}
	meta["version"] = version
	obj["metadata"] = meta
	if dryRun {
		fmt.Printf("[dry-run] would write version %s to %s\n", version, f.name)
		return nil
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("validation error: cannot marshal %s — %w", f.name, err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(f.path, out, 0o644); err != nil {
		return fmt.Errorf("validation error: cannot write %s — %w", f.name, err)
	}
	return nil
}

// ─── Discovery ───────────────────────────────────────────────────────────────

// skipDirs lists directories that are excluded from version file scanning.
var skipDirs = map[string]bool{
	"node_modules": true,
	".venv":        true,
	"__pycache__":  true,
}

// scanSpec describes how to discover one type of version-bearing file.
type scanSpec struct {
	// filename is the relative path from the scan root (e.g. "pyproject.toml").
	filename string
	// constructor builds the appropriate VersionFile implementation.
	constructor func(name, path string) VersionFile
	// validator returns true if the file actually contains a parseable version.
	validator func(path string) bool
	// subdirs, when true, scans root AND each immediate child directory.
	subdirs bool
}

// scanSpecs is the ordered list of file patterns to auto-discover.
// Scan order determines the canonical file for --sync (first pyproject.toml wins).
var scanSpecs = []scanSpec{
	{
		filename:    "pyproject.toml",
		constructor: func(n, p string) VersionFile { return NewPyprojectVersionFile(n, p) },
		validator:   hasPyprojectVersion,
		subdirs:     true,
	},
	{
		filename:    "package.json",
		constructor: func(n, p string) VersionFile { return NewJsonVersionFile(n, p) },
		validator:   hasJsonVersion,
		subdirs:     true,
	},
	{
		filename:    filepath.Join(".claude-plugin", "plugin.json"),
		constructor: func(n, p string) VersionFile { return NewJsonVersionFile(n, p) },
		validator:   hasJsonVersion,
		subdirs:     false,
	},
	{
		filename:    filepath.Join(".claude-plugin", "marketplace.json"),
		constructor: func(n, p string) VersionFile { return NewMarketplaceVersionFile(n, p) },
		validator:   hasMarketplaceVersion,
		subdirs:     false,
	},
}

func hasJsonVersion(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	_, ok := obj["version"].(string)
	return ok
}

func hasPyprojectVersion(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, ok := pyprojectVersion(data)
	return ok
}

func hasMarketplaceVersion(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = meta["version"].(string)
	return ok
}

// subdirs returns sorted immediate child directories of root, excluding
// hidden directories and those in skipDirs.
func subdirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skipDirs[name] {
			continue
		}
		dirs = append(dirs, filepath.Join(root, name))
	}
	return dirs
}

// DiscoverVersionFiles auto-discovers all version-bearing files under root.
// For each spec with subdirs=true, root is checked first, then immediate
// child directories (hidden dirs and skip dirs are excluded).
func DiscoverVersionFiles(root string) ([]VersionFile, error) {
	var found []VersionFile

	for _, spec := range scanSpecs {
		// Check at root level.
		p := filepath.Join(root, spec.filename)
		if spec.validator(p) {
			found = append(found, spec.constructor(spec.filename, p))
		}

		if spec.subdirs {
			for _, child := range subdirs(root) {
				p := filepath.Join(child, spec.filename)
				if spec.validator(p) {
					rel := filepath.Join(filepath.Base(child), spec.filename)
					found = append(found, spec.constructor(rel, p))
				}
			}
		}
	}

	return found, nil
}

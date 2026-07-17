package effects_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOSPublicationFSStatReportsNodeTypes proves Stat classifies a regular
// file, a directory, an absent path, and a symlink (as NodeOther, so
// publication treats it as unrelated drift instead of overwriting it) exactly
// as the PublicationFS contract requires.
func TestOSPublicationFSStatReportsNodeTypes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()

	// Absent.
	node, err := fs.Stat(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.Equal(t, effects.NodeAbsent, node.Type)

	// Regular file, with an exact mode.
	filePath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hi"), 0o640))
	node, err = fs.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, effects.NodeFile, node.Type)
	assert.Equal(t, os.FileMode(0o640), node.Mode)

	// Directory.
	dirPath := filepath.Join(dir, "subdir")
	require.NoError(t, os.Mkdir(dirPath, 0o755))
	node, err = fs.Stat(dirPath)
	require.NoError(t, err)
	assert.Equal(t, effects.NodeDir, node.Type)

	// Symlink: reported as NodeOther via Lstat, never followed.
	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(dir, "link")
		require.NoError(t, os.Symlink(filePath, linkPath))
		node, err = fs.Stat(linkPath)
		require.NoError(t, err)
		assert.Equal(t, effects.NodeOther, node.Type, "a symlink must never be treated as the file it points to")
	}
}

// TestOSPublicationFSReadFile proves ReadFile returns exact content and
// propagates a not-exist error for a missing path.
func TestOSPublicationFSReadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()

	filePath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("exact content"), 0o644))

	content, err := fs.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, []byte("exact content"), content)

	_, err = fs.ReadFile(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

// TestOSPublicationFSWriteFileEnforcesExactMode proves WriteFile chmods after
// writing so a pre-existing file's mode bits do not survive a rewrite — the
// os.WriteFile call alone only applies mode to a newly created file, so the
// explicit os.Chmod afterward is load-bearing whenever the file pre-exists
// with different permission bits.
func TestOSPublicationFSWriteFileEnforcesExactMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()
	filePath := filepath.Join(dir, "file.txt")

	// Pre-exists with a different mode than the write will request.
	require.NoError(t, os.WriteFile(filePath, []byte("old"), 0o600))

	require.NoError(t, fs.WriteFile(filePath, []byte("new content"), 0o644))

	info, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "mode must be exactly enforced even over a pre-existing file")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new content"), content)
}

// TestOSPublicationFSWriteFileSurfacesUnderlyingError proves WriteFile
// propagates the os.WriteFile error (for example, an unwritable parent
// directory) without attempting the chmod step.
func TestOSPublicationFSWriteFileSurfacesUnderlyingError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()
	// The parent directory does not exist, so the write itself fails.
	target := filepath.Join(dir, "missing-parent", "file.txt")

	err := fs.WriteFile(target, []byte("x"), 0o644)
	require.Error(t, err)
}

// TestOSPublicationFSStatSurfacesUnderlyingError proves Stat propagates a
// non-not-exist error (for example, an unreadable parent directory) rather
// than masking it as NodeAbsent.
func TestOSPublicationFSStatSurfacesUnderlyingError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("permission-bit denial semantics differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o000))
	defer os.Chmod(blocked, 0o755) //nolint:errcheck // best-effort cleanup so t.TempDir() can remove it

	fs := effects.NewOSPublicationFS()
	_, err := fs.Stat(filepath.Join(blocked, "file.txt"))
	require.Error(t, err, "an unreadable parent directory must surface an error, not NodeAbsent")
}

// TestOSPublicationFSMkdirAll proves MkdirAll creates nested directories and
// is idempotent when they already exist.
func TestOSPublicationFSMkdirAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()
	nested := filepath.Join(dir, "a", "b", "c")

	require.NoError(t, fs.MkdirAll(nested, 0o755))
	info, err := os.Stat(nested)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Idempotent: calling again on an existing directory does not error.
	require.NoError(t, fs.MkdirAll(nested, 0o755))
}

// TestOSPublicationFSRemove proves Remove deletes exactly the named node (not
// a directory tree — Remove is not RemoveAll) and errors on a missing path.
func TestOSPublicationFSRemove(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fs := effects.NewOSPublicationFS()
	filePath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o644))

	require.NoError(t, fs.Remove(filePath))
	_, err := os.Stat(filePath)
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err))

	err = fs.Remove(filepath.Join(dir, "missing"))
	require.Error(t, err, "removing an absent path must surface an error, not silently succeed")

	// Remove refuses a non-empty directory (exactly os.Remove semantics, never
	// RemoveAll), so a stale-leaf directory with unexpected children fails
	// loudly instead of being deleted wholesale.
	nonEmptyDir := filepath.Join(dir, "nonempty")
	require.NoError(t, os.Mkdir(nonEmptyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nonEmptyDir, "child"), []byte("x"), 0o644))
	err = fs.Remove(nonEmptyDir)
	require.Error(t, err, "Remove must never behave like RemoveAll")
}

// TestOSPublicationFSSatisfiesPublishThroughRealOSSeam runs a full Publish()
// against the real os-backed seam in a temp directory: create, update, and
// stale-leaf removal, verified on disk. This is the strongest available proof
// that OSPublicationFS actually satisfies the same PublicationFS contract the
// memFS fake asserts, not just that its individual methods behave in
// isolation.
func TestOSPublicationFSSatisfiesPublishThroughRealOSSeam(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	payloadRoot := filepath.Join(dir, "payload")
	fs := effects.NewOSPublicationFS()

	// Create.
	first := mustRenderedTree(t, "a/SKILL.md", "# Skill v1\n")
	report, err := effects.Publish(first, payloadRoot, fs)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathCreated, report.Results[0].Outcome)

	onDisk, err := os.ReadFile(filepath.Join(payloadRoot, "a/SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Skill v1\n", string(onDisk))

	// Update: same path, new content.
	second := mustRenderedTree(t, "a/SKILL.md", "# Skill v2\n")
	report, err = effects.Publish(second, payloadRoot, fs)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathUpdated, report.Results[0].Outcome)

	onDisk, err = os.ReadFile(filepath.Join(payloadRoot, "a/SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Skill v2\n", string(onDisk))

	// Stale-leaf removal: publishing a tree at a new path removes the old leaf
	// from disk.
	third := mustRenderedTree(t, "b/SKILL.md", "# Skill b\n")
	report, err = effects.Publish(third, payloadRoot, fs)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)

	_, err = os.Stat(filepath.Join(payloadRoot, "a/SKILL.md"))
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err), "the stale leaf must be actually removed from disk")

	onDisk, err = os.ReadFile(filepath.Join(payloadRoot, "b/SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Skill b\n", string(onDisk))

	var removed bool
	for _, result := range report.Results {
		if result.Path == filepath.Join(payloadRoot, "a/SKILL.md") && result.Outcome == effects.PathRemoved {
			removed = true
		}
	}
	assert.True(t, removed, "stale removal is reported per-path against the real OS seam")

	// Retry converges: publishing the same (third) tree again reports verified,
	// no further mutation.
	report, err = effects.Publish(third, payloadRoot, fs)
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathVerified, report.Results[0].Outcome)
}

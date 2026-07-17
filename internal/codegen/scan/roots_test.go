package scan_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exclusionFixtureRoot returns the checked-in negative-fixture tree used to
// prove the closed exclusion list matches whole path segments only (see
// testdata/exclusion).
func exclusionFixtureRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "testdata", "exclusion")
}

func TestDiscoverExcludesOnlyExactSegments(t *testing.T) {
	t.Parallel()

	discovered, err := scan.Discover(exclusionFixtureRoot(t), []string{"skills"})
	require.NoError(t, err)

	assert.Contains(t, discovered, "skills/real/active.md",
		"a plain active file must always be discovered")
	assert.Contains(t, discovered, "skills/user-acceptance-testing/SKILL.md",
		"a directory whose name merely contains the substring \"test\" must not be excluded")
	assert.Contains(t, discovered, "skills/testdata-real/visible.md",
		"a directory whose name merely starts with \"testdata\" (\"testdata-real\") must not be excluded")

	assert.NotContains(t, discovered, "skills/testdata/hidden.md",
		"a file under an exact \"testdata\" segment must be excluded")
	assert.NotContains(t, discovered, "skills/vendor/thirdparty.md",
		"a file under an exact \"vendor\" segment must be excluded")
	assert.NotContains(t, discovered, "skills/.opencode/generated.md",
		"a file under an exact \".opencode\" segment (generated output) must be excluded")
}

func TestDiscoverIsDeterministicAndMarkdownOnly(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "b", "SKILL.md"), "# B\n")
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "# A\n")
	mustWriteFile(t, filepath.Join(base, "skills", "a", "notes.txt"), "not markdown\n")
	mustWriteFile(t, filepath.Join(base, "agents", "x.md"), "# X\n")

	discovered, err := scan.Discover(base, []string{"skills", "agents"})
	require.NoError(t, err)
	assert.Equal(t, []string{"agents/x.md", "skills/a/SKILL.md", "skills/b/SKILL.md"}, discovered)

	// Discovery must be repeatable across repeated calls (and, by the same
	// logic, across worktrees): no ordering nondeterminism.
	again, err := scan.Discover(base, []string{"skills", "agents"})
	require.NoError(t, err)
	assert.Equal(t, discovered, again)
}

func TestDiscoverRejectsSymlinkedOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	t.Parallel()

	base := t.TempDir()
	realFile := filepath.Join(base, "skills", "real.md")
	mustWriteFile(t, realFile, "# Real\n")
	symlinkPath := filepath.Join(base, "skills", "linked.md")
	require.NoError(t, os.Symlink(realFile, symlinkPath))

	_, err := scan.Discover(base, []string{"skills"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "symlink")
}

func TestDiscoverRejectsSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	t.Parallel()

	base := t.TempDir()
	realRoot := filepath.Join(base, "real-skills")
	require.NoError(t, os.MkdirAll(realRoot, 0o755))
	symlinkRoot := filepath.Join(base, "skills")
	require.NoError(t, os.Symlink(realRoot, symlinkRoot))

	_, err := scan.Discover(base, []string{"skills"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "symlink")
}

func TestDiscoverRejectsMissingRoot(t *testing.T) {
	t.Parallel()

	_, err := scan.Discover(t.TempDir(), []string{"skills"})
	assert.Error(t, err)
}

func TestDiscoverRejectsNoRoots(t *testing.T) {
	t.Parallel()

	_, err := scan.Discover(t.TempDir(), nil)
	assert.Error(t, err)
}

func mustWriteFile(t testing.TB, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

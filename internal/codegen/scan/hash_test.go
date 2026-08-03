package scan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashTreeIsStableAndDetectsAnyByteChange(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "skills", "a", "SKILL.md")
	mustWriteFile(t, target, "# A\n\nOriginal content.\n")

	first, err := scan.HashTree(base, []string{"skills"})
	require.NoError(t, err)
	require.NotEmpty(t, first)

	repeat, err := scan.HashTree(base, []string{"skills"})
	require.NoError(t, err)
	assert.Equal(t, first, repeat, "hashing an unmodified tree twice must produce the same digest")

	require.NoError(t, os.WriteFile(target, []byte("# A\n\nOriginal content!\n"), 0o644))
	changed, err := scan.HashTree(base, []string{"skills"})
	require.NoError(t, err)
	assert.NotEqual(t, first, changed, "a single changed byte must change the digest")
}

func TestHashTreeCoversNonMarkdownBytesToo(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "skills", "a", "SKILL.md"), "# A\n")
	nonMarkdown := filepath.Join(base, "skills", "a", "figure.yaml")
	mustWriteFile(t, nonMarkdown, "key: value\n")

	before, err := scan.HashTree(base, []string{"skills"})
	require.NoError(t, err)

	// Discover only ever reports ".md" owners, but the read-only proof must
	// still catch a change to a non-Markdown file under a canonical root.
	require.NoError(t, os.WriteFile(nonMarkdown, []byte("key: changed\n"), 0o644))
	after, err := scan.HashTree(base, []string{"skills"})
	require.NoError(t, err)
	assert.NotEqual(t, before, after)
}

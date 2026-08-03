package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOwnedPathAcceptsExactRelativePaths proves valid normalized relative paths
// construct and compare by exact value.
func TestOwnedPathAcceptsExactRelativePaths(t *testing.T) {
	t.Parallel()

	valid := []string{"a.md", "skills/worker/SKILL.md", ".hidden", "dir/.keep"}
	for _, raw := range valid {
		path, err := effects.NewOwnedPath(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, raw, path.String())
		assert.True(t, path.IsValid())
	}

	a := mustOwnedPath(t, "a/b.md")
	assert.True(t, a.Equal(mustOwnedPath(t, "a/b.md")))
	assert.False(t, a.Equal(mustOwnedPath(t, "a/c.md")))
	assert.False(t, effects.OwnedPath{}.IsValid())
}

// TestOwnedPathRejectsUnsafePaths is the negative table proving a filesystem
// effect can never name a glob, an absolute path, a traversal, or a
// non-normalized spelling.
func TestOwnedPathRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		hint string
	}{
		{"", "empty or padded"},
		{" a.md", "empty or padded"},
		{"/etc/passwd", "absolute"},
		{"*.go", "glob"},
		{"a/?.md", "glob"},
		{"a/[abc].md", "glob"},
		{"../escape", "escapes its root"},
		{"a/../../escape", "escapes its root"},
		{"a//b", "normalized"},
		{"./a", "normalized"},
		{"a/b/../b", "normalized"},
		{".", "specific entry"},
	}
	for _, testCase := range cases {
		_, err := effects.NewOwnedPath(testCase.raw)
		require.Error(t, err, testCase.raw)
		assert.Contains(t, err.Error(), testCase.hint, testCase.raw)
	}
}

package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileSystemEffectsNameExactPaths proves each filesystem effect names exact
// owned paths and reports the right kind, mutation, and runtime class.
func TestFileSystemEffectsNameExactPaths(t *testing.T) {
	t.Parallel()

	read, err := effects.NewReadFile(mustOwnedPath(t, "a.md"))
	require.NoError(t, err)
	assert.Equal(t, effects.FSRead, read.Kind())
	assert.False(t, read.StateChanging())
	assert.Equal(t, effects.RuntimeClassNative, read.Classify())

	write, err := effects.NewWriteReplaceFile(mustOwnedPath(t, "a.md"), []byte("x"))
	require.NoError(t, err)
	content, ok := write.Content()
	require.True(t, ok)
	assert.Equal(t, []byte("x"), content)
	assert.True(t, write.StateChanging())

	mkdir, err := effects.NewCreateDirectory(mustOwnedPath(t, "d"))
	require.NoError(t, err)
	assert.Equal(t, effects.FSCreateDirectory, mkdir.Kind())

	move, err := effects.NewMoveFile(mustOwnedPath(t, "from"), mustOwnedPath(t, "to"))
	require.NoError(t, err)
	dest, ok := move.Destination()
	require.True(t, ok)
	assert.Equal(t, "to", dest.String())

	remove, err := effects.NewRemoveFile(mustOwnedPath(t, "a.md"))
	require.NoError(t, err)
	assert.Equal(t, effects.FSRemove, remove.Kind())
	assert.True(t, remove.StateChanging())
}

// TestRemoveCannotNameAGlob proves a removal effect cannot be built from a glob:
// the guard is in OwnedPath, so a removal can never expand to unowned files.
func TestRemoveCannotNameAGlob(t *testing.T) {
	t.Parallel()

	_, err := effects.NewOwnedPath("*.go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "glob")

	_, err = effects.NewRemoveFile(effects.OwnedPath{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero or invalid")
}

// TestFileSystemEffectValidationNegatives covers the invalid-operand paths.
func TestFileSystemEffectValidationNegatives(t *testing.T) {
	t.Parallel()

	zero := effects.OwnedPath{}
	_, err := effects.NewReadFile(zero)
	require.Error(t, err)
	_, err = effects.NewWriteReplaceFile(zero, []byte("x"))
	require.Error(t, err)
	_, err = effects.NewCreateDirectory(zero)
	require.Error(t, err)
	_, err = effects.NewMoveFile(zero, mustOwnedPath(t, "to"))
	require.Error(t, err)

	// A self-move is rejected.
	_, err = effects.NewMoveFile(mustOwnedPath(t, "same"), mustOwnedPath(t, "same"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identical")

	assert.False(t, effects.FileSystemEffect{}.IsValid())
}

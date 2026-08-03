package claudecode_test

import (
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
)

// buildTestBundle builds a validated artifact.Bundle from an in-memory file set.
// Modes mirror the production rule: 0755 for shell scripts, 0644 otherwise. It
// lets a test construct a forged component bundle without touching the embedded
// production assets.
func buildTestBundle(t *testing.T, files map[string]string) artifact.Bundle {
	t.Helper()

	source := fstest.MapFS{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]artifact.Entry, 0, len(names))
	for _, name := range names {
		content := []byte(files[name])
		source[name] = &fstest.MapFile{Data: content}

		path, err := artifact.NewPath(name)
		require.NoError(t, err)
		bits := uint32(0o644)
		if strings.HasSuffix(name, ".sh") {
			bits = 0o755
		}
		mode, err := artifact.NewMode(bits)
		require.NoError(t, err)
		entry, err := artifact.NewFileEntry(path, mode, artifact.DigestBytes(content))
		require.NoError(t, err)
		entries = append(entries, entry)
	}

	manifest, err := artifact.NewManifest(entries...)
	require.NoError(t, err)
	bundle, err := artifact.NewBundle(source, manifest)
	require.NoError(t, err)
	return bundle
}

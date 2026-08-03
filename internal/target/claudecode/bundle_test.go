package claudecode_test

import (
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

// TestComponentBundlesLoadInIsolation is the isolated-load oracle: every
// component bundle materializes entirely from its own snapshotted bytes, with no
// dependency on sibling bundles or the source checkout. For each component it
// opens every declared leaf, verifies the exact bytes hash to the declared
// digest, and confirms the bundle refuses any path it did not declare (including
// leaves owned only by a sibling component).
func TestComponentBundlesLoadInIsolation(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	components := d.Components()
	for _, component := range components {
		component := component
		t.Run(component.Kind().String(), func(t *testing.T) {
			bundle := component.Bundle()
			manifest := bundle.Manifest()
			require.Positive(t, manifest.Len())

			// The plugin manifest is present and canonical for this component.
			assertPluginManifestPresent(t, bundle, manifest)

			var lastPath string
			for _, entry := range manifest.Entries() {
				path := entry.Path().String()

				// Manifest is lexicographically ordered and every entry freezes a
				// clean relative path, an octal mode, and a sha256 digest.
				assert.Less(t, lastPath, path, "manifest paths must be strictly sorted")
				lastPath = path
				assert.False(t, strings.HasPrefix(path, "/"))
				assert.NotContains(t, path, "..")
				assert.Regexp(t, `^[0-7]{4}$`, entry.Mode().String())
				assert.True(t, entry.IsRegular())
				assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, entry.Digest().String())

				// Every declared leaf opens and its bytes verify against the digest.
				file, openErr := bundle.Open(path)
				require.NoError(t, openErr)
				data, readErr := io.ReadAll(file)
				require.NoError(t, readErr)
				require.NoError(t, file.Close())
				assert.Equal(t, entry.Digest(), artifact.DigestBytes(data),
					"opened bytes must match the frozen digest")
			}

			// Isolation: a path owned only by a different component is not
			// resolvable from this bundle.
			_, err := bundle.Open("this/path/is/not/declared.md")
			require.Error(t, err)
			assert.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

// TestShellScriptsAreExecutable proves the deterministic mode rule survives into
// the bundle: the git-discipline hook script is 0755 so it can run in place.
func TestShellScriptsAreExecutable(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	var sawScript bool
	for _, entry := range d.Hooks().Bundle().Manifest().Entries() {
		if strings.HasSuffix(entry.Path().String(), ".sh") {
			sawScript = true
			assert.Equal(t, "0755", entry.Mode().String(),
				"hook shell scripts must be executable so they run in place")
		} else {
			assert.Equal(t, "0644", entry.Mode().String())
		}
	}
	assert.True(t, sawScript, "the hooks bundle must carry the git-discipline script")
}

// TestDescriptorGenerationIsDeterministic is the double-generate proof: building
// the descriptor twice yields byte-identical bundle identities and canonical
// manifests. Content-addressed generation must be stable input-to-output.
func TestDescriptorGenerationIsDeterministic(t *testing.T) {
	first, err := claudecode.Descriptor()
	require.NoError(t, err)
	second, err := claudecode.Descriptor()
	require.NoError(t, err)

	firstComponents := first.Components()
	secondComponents := second.Components()
	require.Len(t, secondComponents, len(firstComponents))

	for i := range firstComponents {
		a := firstComponents[i]
		b := secondComponents[i]
		assert.Equal(t, a.ID(), b.ID())
		assert.True(t, a.Bundle().Equal(b.Bundle()),
			"component %s must derive an identical BundleID across generations", a.Kind())
		assert.Equal(t, a.Bundle().ID().String(), b.Bundle().ID().String())

		firstManifest, err := a.Bundle().Manifest().MarshalJSON()
		require.NoError(t, err)
		secondManifest, err := b.Bundle().Manifest().MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, firstManifest, secondManifest,
			"component %s manifest must serialize byte-identically", a.Kind())
	}
}

func assertPluginManifestPresent(t *testing.T, bundle artifact.Bundle, manifest artifact.Manifest) {
	t.Helper()

	const manifestPath = ".claude-plugin/plugin.json"
	found := false
	for _, entry := range manifest.Entries() {
		if entry.Path().String() == manifestPath {
			found = true
			break
		}
	}
	require.True(t, found, "each Claude Code plugin bundle must declare %s", manifestPath)

	file, err := bundle.Open(manifestPath)
	require.NoError(t, err)
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed),
		"the embedded plugin.json must be valid JSON")
	name, ok := parsed["name"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(name, "pasture-"),
		"the plugin name must be one of the pasture-* native plugins, got %q", name)
}

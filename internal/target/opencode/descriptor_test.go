package opencode_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/target/opencode"
)

func TestDescriptorPublishesIndependentGeneratedBundles(t *testing.T) {
	t.Parallel()
	descriptor, err := opencode.Descriptor()
	require.NoError(t, err)
	require.Equal(t, runtime.OpenCode1_18_10().ID(), descriptor.RuntimeContractID())
	require.True(t, descriptor.Skills().DefaultEnabled())
	require.True(t, descriptor.Agents().DefaultEnabled())
	require.False(t, descriptor.Hooks().DefaultEnabled())

	for _, component := range descriptor.Components() {
		component := component
		t.Run(component.Extension().String(), func(t *testing.T) {
			t.Parallel()
			require.NotZero(t, component.Bundle().Manifest().Len())
			for _, entry := range component.Bundle().Manifest().Entries() {
				file, openErr := component.Bundle().Open(entry.Path().String())
				require.NoError(t, openErr)
				content, readErr := io.ReadAll(file)
				require.NoError(t, readErr)
				require.NoError(t, file.Close())
				require.Equal(t, entry.Digest(), artifact.DigestBytes(content))
			}
		})
	}
	require.Len(t, descriptor.Hooks().Bundle().Manifest().Entries(), 1)
	require.Equal(t, "pasture-hooks.ts", descriptor.Hooks().Bundle().Manifest().Entries()[0].Path().String())
}

func TestSkillsBundleIsIndependentOfSiblingCells(t *testing.T) {
	t.Parallel()
	first, err := opencode.Descriptor()
	require.NoError(t, err)
	second, err := opencode.Descriptor()
	require.NoError(t, err)
	require.True(t, first.Skills().Bundle().Equal(second.Skills().Bundle()))
	require.Equal(t, first.Skills().Bundle().ID(), second.Skills().Bundle().ID())

	for _, forbidden := range []string{"worker.md", "pasture-hooks.ts"} {
		_, err := first.Skills().Bundle().Open(forbidden)
		require.Error(t, err)
	}
	worker, err := first.Skills().Bundle().Open("worker/SKILL.md")
	require.NoError(t, err)
	content, err := io.ReadAll(worker)
	require.NoError(t, err)
	require.NoError(t, worker.Close())
	require.True(t, bytes.HasPrefix(content, []byte("---\n")))
}

package codegen

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaTaskStorageAndDependencyContract(t *testing.T) {
	t.Parallel()
	var storage bytes.Buffer
	buildHandoffs(&storage, 0)
	require.Contains(t, storage.String(), `storage-pattern="pasture-task-body"`)
	var dependencies bytes.Buffer
	buildDependencyModel(&dependencies, 0)
	resolved, err := ResolveBodyMarkersNeutral(dependencies.String())
	require.NoError(t, err)
	require.Contains(t, resolved, neutralTrackerEntryPoint+" block {parent-uri} --by {child-uri}")
	require.NotContains(t, resolved, "bd dep")
	var followup bytes.Buffer
	buildFollowupLifecycle(&followup, 0)
	require.NotContains(t, followup.String(), "bd dep")
	require.Contains(t, followup.String(), "{followup-slice-uri}")
}

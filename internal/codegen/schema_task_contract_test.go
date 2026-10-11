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
	require.Contains(t, dependencies.String(), "pasture task dep add {parent-uri} --blocked-by {child-uri}")
	require.NotContains(t, dependencies.String(), "bd dep")
	var followup bytes.Buffer
	buildFollowupLifecycle(&followup, 0)
	require.NotContains(t, followup.String(), "bd dep")
	require.Contains(t, followup.String(), "{followup-slice-uri}")
}

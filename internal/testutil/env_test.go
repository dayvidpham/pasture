package testutil_test

// Hermetic-environment self-tests. The behaviour under test decides where every
// other package's tests write durable state, so it is pinned here rather than
// only in a consumer.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// TestSetHermeticEnvRemovesTheOverridesThatPointAtTheOperatorsState is the pin
// on the defect this package's sandbox carried: it redirected HOME and
// XDG_DATA_HOME but left PASTURE_DB_PATH alone, and that variable is consulted
// FIRST by the production store resolver. A test process started from a shell
// that exports the operator's store therefore resolved the operator's store —
// and, because the lifecycle hook derives its fault record from the store path
// it would use, appended to the operator's live lifecycle-faults.jsonl.
// PASTURE_NAMESPACE is the same shape for reads: an inherited value silently
// scopes list/ready/blocked and names new tasks.
//
// WHAT IT VISITS: the three overrides testutil.SetHermeticEnv removes, and the
// two sandbox variables it sets. It does not visit the production resolver,
// because this package stays a leaf; the consequence for the store path is
// pinned by cmd/pasture's sandbox guard, which resolves the real
// lifecycleStorePath through the real command.
func TestSetHermeticEnvRemovesTheOverridesThatPointAtTheOperatorsState(t *testing.T) {
	const operatorStore = "/home/operator/.local/share/pasture/pasture.db"
	const operatorCapture = "/home/operator/.local/share/pasture-captures/live"
	const operatorNamespace = "https://example.test/operator-repo"
	t.Setenv(testutil.DBPathEnv, operatorStore)
	t.Setenv(testutil.CaptureDirEnv, operatorCapture)
	t.Setenv(testutil.NamespaceEnv, operatorNamespace)

	cleanup, err := testutil.SetHermeticEnv("pasture-hermetic-self-test")
	require.NoError(t, err)
	require.NotNil(t, cleanup, "a successful hermetic setup must hand back its restore")

	for _, override := range []string{testutil.DBPathEnv, testutil.CaptureDirEnv, testutil.NamespaceEnv} {
		_, present := os.LookupEnv(override)
		assert.False(t, present,
			"%s must not survive into a hermetic test process: it points away from the sandbox "+
				"(and, for the store, outranks HOME and XDG_DATA_HOME), so a test that falls back to "+
				"the default store would resolve the operator's own state", override)
	}
	for _, sandbox := range []string{"HOME", "XDG_DATA_HOME"} {
		dir := os.Getenv(sandbox)
		require.NotEmpty(t, dir, "%s must point at the sandbox after SetHermeticEnv", sandbox)
		assertInsideTempDir(t, dir, sandbox+" must land inside the system temporary directory, so a "+
			"write through it is a scratch write and never the operator's home")
	}

	cleanup()

	// THE RESTORE IS PART OF THE CONTRACT: cmd/pasture's TestMain runs this once
	// for the whole binary, and a caller that wants to exercise the override
	// sets it itself afterwards. A restore that dropped it would make the
	// neutralization indistinguishable from a permanent unset.
	assert.Equal(t, operatorStore, os.Getenv(testutil.DBPathEnv),
		"cleanup must restore PASTURE_DB_PATH to the value the process had before the sandbox")
	assert.Equal(t, operatorCapture, os.Getenv(testutil.CaptureDirEnv),
		"cleanup must restore PASTURE_CAPTURE_DIR to the value the process had before the sandbox")
	assert.Equal(t, operatorNamespace, os.Getenv(testutil.NamespaceEnv),
		"cleanup must restore PASTURE_NAMESPACE to the value the process had before the sandbox")
}

func assertInsideTempDir(t *testing.T, path, message string) {
	t.Helper()
	root := os.TempDir()
	relative, err := filepath.Rel(root, path)
	require.NoError(t, err, "%s: %q is not comparable with the temporary directory %q", message, path, root)
	inside := relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
	assert.True(t, inside, "%s: %q is outside %q", message, path, root)
}

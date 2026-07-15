package ir_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpaqueDescriptorCompileFixtures(t *testing.T) {
	t.Parallel()

	pass := exec.Command("go", "test", "./testdata/compilepass")
	pass.Env = append(os.Environ(), "CGO_ENABLED=0")
	passOutput, err := pass.CombinedOutput()
	require.NoError(t, err, "typed descriptor fixture must compile:\n%s", passOutput)

	fail := exec.Command("go", "test", "./testdata/compilefail")
	fail.Env = append(os.Environ(), "CGO_ENABLED=0")
	failOutput, err := fail.CombinedOutput()
	require.Error(t, err, "raw IDs/strings and wrong descriptor domains must fail compilation")
	message := string(failOutput)
	assert.Contains(t, message, "cannot use")
	assert.True(t,
		strings.Contains(message, "SemanticOperationID") || strings.Contains(message, "OperationDescriptor"),
		"compiler output must name an opaque typed boundary:\n%s", message,
	)
}

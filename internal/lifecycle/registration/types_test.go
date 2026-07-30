package registration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestClaudeManifestIsCompleteAndDefensivelyCopied(t *testing.T) {
	t.Parallel()
	manifest := registration.ClaudeCode2_1_210()
	require.Len(t, manifest.Events, 30)
	require.Equal(t, "SessionStart", manifest.Events[0].NativeName)
	require.Equal(t, "ElicitationResult", manifest.Events[29].NativeName)
	copy := manifest.Entries()
	copy[0].NativeName = "changed"
	copy[0].AllowedFields[0] = 0
	require.Equal(t, "SessionStart", manifest.Events[0].NativeName)
	require.NotZero(t, manifest.Events[0].AllowedFields[0])
}

func TestGoCompilerRejectsTargetNeutralHostContractImport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module boundary.invalid\n\ngo 1.25\n\nrequire github.com/dayvidpham/pasture v0.0.0\nreplace github.com/dayvidpham/pasture => "+moduleRoot(t)+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "violation.go"), []byte("package violation\nimport _ \"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract\"\n"), 0o600))
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = dir
	output, err := command.CombinedOutput()
	require.Error(t, err, "the compiler must reject imports from outside the ingress parent")
	require.Contains(t, string(output), "use of internal package")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(dir, "..", "..", ".."))
}

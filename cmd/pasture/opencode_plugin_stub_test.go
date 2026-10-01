package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen"
)

// TestOpenCodePluginHasNoRuntimeImport pins that the generated OpenCode plugin
// performs no runtime imports. The real host resolves no plugin package, so any
// runtime specifier fails loading there; the module must load with no sibling
// beside it.
func TestOpenCodePluginHasNoRuntimeImport(t *testing.T) {
	t.Parallel()
	module, err := codegen.GenerateOpenCodeHooksModule()
	require.NoError(t, err)
	require.NotContains(t, module, "@opencode/plugin")
	require.NotContains(t, module, "Plugin.define")
	require.NotContains(t, module, "require(")
	for _, line := range strings.Split(module, "\n") {
		trimmed := strings.TrimSpace(line)
		require.False(t, strings.HasPrefix(trimmed, "import "),
			"generated plugin must have zero import statements, got %q", line)
	}
	require.Contains(t, module, "export default {")
}

// TestOpenCodePluginLoadsWithoutStub proves the generated module loads under
// Bun in an empty directory with no node_modules beside it.
func TestOpenCodePluginLoadsWithoutStub(t *testing.T) {
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "bun is required to prove the plugin loads without a stub")
	module, err := codegen.GenerateOpenCodeHooksModule()
	require.NoError(t, err)
	dir := t.TempDir()
	modulePath := filepath.Join(dir, "pasture-hooks.ts")
	require.NoError(t, os.WriteFile(modulePath, []byte(module), 0o600))
	runner := filepath.Join(dir, "no-stub.ts")
	require.NoError(t, os.WriteFile(runner, []byte(
		"const mod = await import("+quoteForBun(modulePath)+");\n"+
			"const value = mod.default;\n"+
			"if (value === null || typeof value !== \"object\" || Array.isArray(value)) throw new Error(\"not the plain definition object\");\n"+
			"if (typeof value.id !== \"string\" || value.id.trim() === \"\") throw new Error(\"missing id\");\n"+
			"if (typeof value.setup !== \"function\") throw new Error(\"missing setup\");\n"+
			"console.log(\"no-stub load holds\");\n"), 0o600))
	out, err := exec.Command(bun, runner).CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "no-stub load holds")
}

func quoteForBun(path string) string {
	quoted := strings.ReplaceAll(path, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	return `"` + quoted + `"`
}

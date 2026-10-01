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

// TestOpenCodePluginHasNoRuntimeImport holds the generated OpenCode plugin to
// the shared no-runtime-import guard: the real host resolves no package at
// load time, so the module must load with no sibling beside it.
func TestOpenCodePluginHasNoRuntimeImport(t *testing.T) {
	t.Parallel()
	module, err := codegen.GenerateOpenCodeHooksModule()
	require.NoError(t, err)
	require.Empty(t, codegen.OpenCodePluginImportViolation(module),
		"the generated plugin must perform no runtime imports")
	require.Contains(t, module, "export default {")
}

// TestOpenCodePluginLoadsWithoutStub proves the generated module loads under
// Bun in an empty directory with no node_modules beside it. The runner first
// scans the module source with Bun's own transpiler — the authoritative read
// of which imports survive to load time — and then loads it the way the host
// does.
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
		"const source = await Bun.file("+quoteForBun(modulePath)+").text();\n"+
			"const scan = new Bun.Transpiler({ loader: \"ts\" }).scan(source);\n"+
			"if (scan.imports.length !== 0) throw new Error(\"runtime imports: \" + JSON.stringify(scan.imports));\n"+
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

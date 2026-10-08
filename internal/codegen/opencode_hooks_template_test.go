package codegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeHooksTemplateMarkersAndRendering(t *testing.T) {
	t.Parallel()
	source, err := templatesFS.ReadFile("templates/opencode-lifecycle.ts")
	if err != nil {
		t.Fatal(err)
	}
	markers := []string{
		"__PASTURE_CONTRACT_COMMENT__",
		"/* PASTURE_NATIVE_TOOLS */",
		`"__PASTURE_CONTRACT_LITERAL__"`,
		"/* PASTURE_METADATA */ {}",
		"__PASTURE_HOST_VERSION__",
		"__PASTURE_BINARY_ENV__",
		"/* PASTURE_HELPERS */",
		"/* PASTURE_SETUP */",
	}
	remainder := string(source)
	for _, marker := range markers {
		if count := strings.Count(string(source), marker); count != 1 {
			t.Fatalf("embedded template marker %q occurs %d times, want one", marker, count)
		}
		remainder = strings.ReplaceAll(remainder, marker, "")
	}
	if strings.Contains(remainder, "__PASTURE_") || strings.Contains(remainder, "/* PASTURE_") {
		t.Fatal("embedded template has an unknown reserved marker; use the fixed renderer slots")
	}
	data := openCodeHooksTemplateData{
		Contract:     "contract\"\\\n100%",
		NativeTools:  `"native", "second"`,
		MetadataJSON: `{"metadata":"value"}`,
		HostVersion:  "host",
		BinaryEnv:    "BINARY",
		Helpers:      "helper\n__PASTURE_BINARY_ENV__\n",
		Setup:        "    setup\n",
	}
	// A small constructed source isolates quoting, literal percent signs and
	// insertion boundaries without a second copy of the transport.
	input := strings.Join(markers[:6], "\n") + "\n/* PASTURE_HELPERS */\n/* PASTURE_SETUP */  },\n100%\n"
	want := strings.Join([]string{
		"contract\"\\\n100%",
		`"native", "second"`,
		`"contract\"\\\n100%"`,
		`{"metadata":"value"}`,
		"host",
		"BINARY",
		"helper",
		"__PASTURE_BINARY_ENV__",
		"",
		"    setup",
		"  },",
		"100%",
		"",
	}, "\n")
	if got := renderOpenCodeHooksTemplate(input, data); got != want {
		t.Fatalf("fixed rendering changed quoting, boundaries or rescanned a replacement:\ngot  %q\nwant %q", got, want)
	}
	if !strings.Contains(string(source), "/* PASTURE_HELPERS */\nexport default") ||
		!strings.Contains(string(source), "/* PASTURE_SETUP */  },\n};\n") {
		t.Fatal("embedded template changed helper or setup newline boundaries")
	}
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(module, "__PASTURE_") || strings.Contains(module, "/* PASTURE_") {
		t.Fatal("generated module retains a reserved template marker")
	}
}

func TestOpenCodeHooksTemplateTypeScriptSyntax(t *testing.T) {
	t.Parallel()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is required for OpenCode template transformSync syntax validation; enter the flake dev shell and rerun this test")
	}
	source, err := templatesFS.ReadFile("templates/opencode-lifecycle.ts")
	if err != nil {
		t.Fatal(err)
	}
	module, err := GenerateOpenCodeHooksModule()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "authored-template.ts")
	modulePath := filepath.Join(dir, "generated-module.ts")
	if err := os.WriteFile(templatePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modulePath, []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `
const transpiler = new Bun.Transpiler({loader: "ts"});
const paths = Bun.argv.slice(1);
if (paths.length !== 2) {
  throw new Error("syntax validation requires the authored template and generated output paths");
}
for (const path of paths) {
  try {
    transpiler.transformSync(await Bun.file(path).text());
  } catch (error) {
    throw new Error("transformSync syntax validation failed for " + path + ": " + error);
  }
  console.log("transformSync syntax accepted: " + path);
}
let observed = false;
try {
  transpiler.transformSync("const broken = ;");
} catch (error) {
  observed = true;
  console.log("malformed TypeScript rejected by transformSync: " + error);
}
if (!observed) {
  throw new Error("transformSync accepted malformed TypeScript; parser errors are not observed");
}
console.log("template and generated syntax assertions passed");
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bun, "-e", script, templatePath, modulePath).CombinedOutput()
	if err != nil {
		t.Fatalf("Bun transformSync syntax validation: %v\n%s", err, out)
	}
	for _, proof := range []string{
		"transformSync syntax accepted: " + templatePath,
		"transformSync syntax accepted: " + modulePath,
		"malformed TypeScript rejected by transformSync:",
		"template and generated syntax assertions passed",
	} {
		if !strings.Contains(string(out), proof) {
			t.Fatalf("syntax validation did not finish %q: %s", proof, out)
		}
	}
	t.Log(string(out))
}

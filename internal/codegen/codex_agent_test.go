package codegen

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/pelletier/go-toml/v2"
)

type codexAgentFile struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions"`
}

func TestCodexAgentsUseNativeMinimalShapeAndCanonicalBody(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	out := t.TempDir()
	files, err := codexAgentEmitter{}.Emit(root, out, filepath.Join(root, "skills", "protocol", "figures"), GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("codexAgentEmitter.Emit: %v", err)
	}

	wantCount := 0
	for _, spec := range RoleSpecs {
		if len(spec.Tools) > 0 {
			wantCount++
		}
	}
	if len(files) != wantCount {
		t.Fatalf("emitted %d Codex agents, want %d tool-bearing roles", len(files), wantCount)
	}

	for _, file := range files {
		var raw map[string]any
		if err := toml.Unmarshal([]byte(file.Content), &raw); err != nil {
			t.Fatalf("%s: parse generated TOML: %v", file.Path, err)
		}
		wantKeys := map[string]struct{}{
			"name":                   {},
			"description":            {},
			"developer_instructions": {},
		}
		for key := range raw {
			if _, ok := wantKeys[key]; !ok {
				t.Errorf("%s: generated agent has unexpected TOML key %q; want exactly name, description, developer_instructions", file.Path, key)
			}
		}
		for key := range wantKeys {
			if _, ok := raw[key]; !ok {
				t.Errorf("%s: generated agent is missing required TOML key %q", file.Path, key)
			}
		}

		var agent codexAgentFile
		if err := toml.Unmarshal([]byte(file.Content), &agent); err != nil {
			t.Fatalf("%s: decode generated TOML: %v", file.Path, err)
		}
		rel, err := filepath.Rel(out, file.Path)
		if err != nil {
			t.Fatalf("%s: derive generated relative path: %v", file.Path, err)
		}
		if filepath.ToSlash(filepath.Dir(rel)) != ".codex/agents" {
			t.Errorf("%s: generated agent path is %q, want .codex/agents/<name>.toml", file.Path, filepath.ToSlash(rel))
		}
		base := strings.TrimSuffix(filepath.Base(file.Path), ".toml")
		if !strings.HasPrefix(base, codexAgentNamespace) {
			t.Errorf("%s: filename %q must use the %q namespace", file.Path, filepath.Base(file.Path), codexAgentNamespace)
			continue
		}
		roleID := protocol.RoleId(strings.TrimPrefix(base, codexAgentNamespace))
		if !roleID.IsValid() {
			t.Errorf("%s: filename %q names unknown role %q; want pasture-<valid role>.toml", file.Path, filepath.Base(file.Path), roleID)
			continue
		}
		wantFilename := codexAgentNamespace + string(roleID) + ".toml"
		if filepath.Base(file.Path) != wantFilename {
			t.Errorf("%s: filename = %q, want namespaced filename %q", file.Path, filepath.Base(file.Path), wantFilename)
		}
		if agent.Name != base {
			t.Errorf("%s: name = %q, want namespaced filename stem %q", file.Path, agent.Name, base)
		}
		if strings.TrimSpace(agent.Name) == "" {
			t.Errorf("%s: required name is empty", file.Path)
		}
		if strings.TrimSpace(agent.Description) == "" {
			t.Errorf("%s: required description is empty", file.Path)
		}
		if strings.TrimSpace(agent.DeveloperInstructions) == "" {
			t.Errorf("%s: required developer_instructions is empty", file.Path)
		}
		canonical, err := renderAgent(roleID, filepath.Join(root, "skills", "protocol", "figures"))
		if err != nil {
			t.Fatalf("renderAgent(%s): %v", roleID, err)
		}
		body, err := stripFrontmatter(canonical)
		if err != nil {
			t.Fatalf("stripFrontmatter(%s): %v", roleID, err)
		}
		if agent.DeveloperInstructions != body {
			t.Errorf("%s: developer_instructions differs from canonical rendered agent body", file.Path)
		}
	}
}

func TestCodexAgentRejectsUnsafeMultilineLiteralBody(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"safe\n'''\nunsafe", "safe\r\nnormalized", "unsafe\x00control"} {
		if err := validateCodexMultilineLiteral(body); err == nil {
			t.Errorf("validateCodexMultilineLiteral(%q) returned nil, want actionable representation error", body)
		}
	}
}

func TestQuoteCodexTOMLBasicStringUsesTOMLEscapes(t *testing.T) {
	t.Parallel()

	got, err := quoteCodexTOMLBasicString("quote\" slash\\ alert\a\n")
	if err != nil {
		t.Fatalf("quoteCodexTOMLBasicString: %v", err)
	}
	want := `"quote\" slash\\ alert\u0007\n"`
	if got != want {
		t.Fatalf("quoteCodexTOMLBasicString = %q, want %q", got, want)
	}
}

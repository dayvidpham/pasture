package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// skillFrontmatter captures the keys an OpenCode skill SKILL.md may carry in its
// YAML frontmatter. OpenCode skills declare only name + description; the
// Claude-only `skills:` role-list and the agent-only `tools:`/`model:` keys must
// be absent. Decoding into a struct with KnownFields lets the test assert key
// presence/absence directly rather than scanning raw text.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Skills      string `yaml:"skills"`
	Tools       string `yaml:"tools"`
	Model       string `yaml:"model"`
}

// splitFrontmatter separates the leading YAML frontmatter block (delimited by a
// pair of "---" lines) from the markdown body of a SKILL.md file.
func splitFrontmatter(t *testing.T, path, content string) (frontmatter string, body string) {
	t.Helper()
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("%s: missing leading YAML frontmatter delimiter", path)
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("%s: missing closing YAML frontmatter delimiter", path)
	}
	return rest[:end], rest[end+len("\n---\n"):]
}

// bodyBetweenMarkers returns the generated body region (the text between the
// BEGIN and END markers, inclusive of the marker lines). The OpenCode body must
// match the Claude Code body byte-for-byte; comparing the marker region isolates
// that comparison from frontmatter differences.
func bodyBetweenMarkers(t *testing.T, path, content string) string {
	t.Helper()
	begin := strings.Index(content, GeneratedBegin)
	if begin < 0 {
		t.Fatalf("%s: missing BEGIN marker", path)
	}
	end := strings.Index(content, GeneratedEnd)
	if end < 0 {
		t.Fatalf("%s: missing END marker", path)
	}
	return content[begin : end+len(GeneratedEnd)]
}

// TestOpenCodeSkillsEmitTwentyNine renders the OpenCode harness into an isolated
// temp tree and asserts the full emission contract for SLICE-2:
//   - exactly 29 SKILL.md files under .opencode/skill/<dir>/ (5 roles + 24 commands),
//   - each carries valid name (== dir) + non-empty description frontmatter,
//   - none carries the Claude-only `skills:` list or the agent-only tools:/model: keys.
func TestOpenCodeSkillsEmitTwentyNine(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	out := t.TempDir()

	files, err := EmitHarness(out, OpenCodeTarget, figuresDir, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(%s): %v", HarnessOpenCode, err)
	}

	skillRoot := filepath.Join(out, ".opencode", "skill")
	skillByDir := make(map[string]GeneratedFile)
	for _, f := range files {
		rel, err := filepath.Rel(skillRoot, f.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // agent/manifest/other harness output — not a skill
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 || parts[1] != "SKILL.md" {
			continue // verbatim sibling docs (e.g. protocol/PROCESS.md) — not the SKILL.md entry
		}
		skillByDir[parts[0]] = f
	}

	// Expected dirs == exactly the role + command skill dirs the emitter iterates.
	var expectedDirs []string
	for _, item := range roleSkillItems() {
		expectedDirs = append(expectedDirs, item.dir)
	}
	for _, item := range commandSkillItems() {
		expectedDirs = append(expectedDirs, item.dir)
	}
	const wantRoles, wantCommands = 5, 24
	if len(expectedDirs) != wantRoles+wantCommands {
		t.Fatalf("expected %d skill dirs (%d roles + %d commands), enumerated %d",
			wantRoles+wantCommands, wantRoles, wantCommands, len(expectedDirs))
	}
	if len(skillByDir) != len(expectedDirs) {
		t.Fatalf("EmitHarness(%s) emitted %d SKILL.md under .opencode/skill/, want %d",
			HarnessOpenCode, len(skillByDir), len(expectedDirs))
	}

	for _, dir := range expectedDirs {
		f, ok := skillByDir[dir]
		if !ok {
			t.Fatalf("OpenCode emission missing skill dir %q", dir)
			continue
		}
		fmText, _ := splitFrontmatter(t, f.Path, f.Content)

		dec := yaml.NewDecoder(strings.NewReader(fmText))
		dec.KnownFields(false)
		var fm skillFrontmatter
		if err := dec.Decode(&fm); err != nil {
			t.Fatalf("%s: decode frontmatter: %v", f.Path, err)
		}
		if fm.Name != dir {
			t.Errorf("%s: name = %q, want %q (the skill dir)", f.Path, fm.Name, dir)
		}
		if strings.TrimSpace(fm.Description) == "" {
			t.Errorf("%s: description is empty", f.Path)
		}
		if fm.Skills != "" {
			t.Errorf("%s: frontmatter must omit the Claude-only `skills:` list, got %q", f.Path, fm.Skills)
		}
		if fm.Tools != "" {
			t.Errorf("%s: frontmatter must omit `tools:` (agent-only), got %q", f.Path, fm.Tools)
		}
		if fm.Model != "" {
			t.Errorf("%s: frontmatter must omit `model:` (agent-only), got %q", f.Path, fm.Model)
		}

		// The raw frontmatter text must not even carry a `skills:` key (guards
		// against a value the struct happened not to map).
		for _, line := range strings.Split(fmText, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "skills:") {
				t.Errorf("%s: frontmatter contains a `skills:` key: %q", f.Path, line)
			}
		}
	}
}

// TestOpenCodeSkillBodyParity asserts the OpenCode skill body (the text between
// the BEGIN/END markers) is byte-identical to the Claude Code body for every
// role and command skill. The two targets diverge ONLY in frontmatter (OpenCode
// drops the `skills:` list); the body markdown is shared. This is the
// define-once drift guard: if the Claude Code template body and the OpenCode
// template body ever diverge, this test fails.
func TestOpenCodeSkillBodyParity(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")

	for _, item := range roleSkillItems() {
		item := item
		t.Run("role/"+item.dir, func(t *testing.T) {
			t.Parallel()
			claude, err := renderSkill(item.role, figuresDir, TemplateSkill)
			if err != nil {
				t.Fatalf("renderSkill(claude, %s): %v", item.dir, err)
			}
			opencode, err := renderSkill(item.role, figuresDir, OpenCodeTarget.SkillTemplate)
			if err != nil {
				t.Fatalf("renderSkill(opencode, %s): %v", item.dir, err)
			}
			if got, want := bodyBetweenMarkers(t, item.dir, opencode), bodyBetweenMarkers(t, item.dir, claude); got != want {
				t.Errorf("role %q: OpenCode body diverges from Claude Code body", item.dir)
			}
		})
	}

	for _, item := range commandSkillItems() {
		item := item
		t.Run("command/"+item.dir, func(t *testing.T) {
			t.Parallel()
			claude, err := renderSubSkill(item.commandID, figuresDir, TemplateSubSkill)
			if err != nil {
				t.Fatalf("renderSubSkill(claude, %s): %v", item.dir, err)
			}
			opencode, err := renderSubSkill(item.commandID, figuresDir, OpenCodeTarget.SubSkillTemplate)
			if err != nil {
				t.Fatalf("renderSubSkill(opencode, %s): %v", item.dir, err)
			}
			if got, want := bodyBetweenMarkers(t, item.dir, opencode), bodyBetweenMarkers(t, item.dir, claude); got != want {
				t.Errorf("command %q: OpenCode body diverges from Claude Code body", item.dir)
			}
		})
	}
}

// TestOpenCodeSkillWritesToDisk asserts the OpenCode skill emission actually
// materializes the per-skill subdir tree when Write is enabled, exercising the
// production WriteFullFile path end-to-end into an isolated temp tree.
func TestOpenCodeSkillWritesToDisk(t *testing.T) {
	t.Parallel()

	root := testModuleRoot(t)
	figuresDir := filepath.Join(root, "skills", "protocol", "figures")
	out := t.TempDir()

	if _, err := EmitHarness(out, OpenCodeTarget, figuresDir, GenerateOptions{Diff: false, Write: true}); err != nil {
		t.Fatalf("EmitHarness(%s, write): %v", HarnessOpenCode, err)
	}

	for _, item := range roleSkillItems() {
		path := filepath.Join(out, ".opencode", "skill", item.dir, "SKILL.md")
		assertWrittenSkill(t, path, item.dir)
	}
	for _, item := range commandSkillItems() {
		path := filepath.Join(out, ".opencode", "skill", item.dir, "SKILL.md")
		assertWrittenSkill(t, path, item.dir)
	}
}

func assertWrittenSkill(t *testing.T, path, dir string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("OpenCode skill not written to %q: %v", path, err)
	}
	fmText, _ := splitFrontmatter(t, path, string(data))
	if !strings.Contains(fmText, "name: "+dir+"\n") && !strings.HasSuffix(fmText, "name: "+dir) {
		t.Errorf("%s: frontmatter does not declare name: %s", path, dir)
	}
	if strings.Contains(fmText, "skills:") {
		t.Errorf("%s: written skill carries the Claude-only `skills:` key", path)
	}
}

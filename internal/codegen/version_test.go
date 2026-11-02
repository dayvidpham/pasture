package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen"
)

func writePluginVersion(t *testing.T, root, version string) {
	t.Helper()
	dir := filepath.Join(root, ".claude-plugin")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"version":"`+version+`"}`), 0o644))
}

func TestSourceVersionReadsDeclaredReleaseVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePluginVersion(t, root, "1.2.3")
	require.Equal(t, "v1.2.3", codegen.SourceVersion(root))
}

func TestSourceVersionKeepsAnAlreadyPrefixedVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePluginVersion(t, root, "v1.2.3")
	require.Equal(t, "v1.2.3", codegen.SourceVersion(root))
}

func TestSourceVersionFallsBackWhenNoDeclaredVersionExists(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		root func(t *testing.T) string
	}{
		{name: "empty root", root: func(*testing.T) string { return "" }},
		{name: "missing manifest", root: func(t *testing.T) string { return t.TempDir() }},
		{name: "malformed manifest", root: func(t *testing.T) string {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte("{"), 0o644))
			return dir
		}},
		{name: "blank version", root: func(t *testing.T) string {
			dir := t.TempDir()
			writePluginVersion(t, dir, "")
			return dir
		}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, codegen.DefaultSourceVersion, codegen.SourceVersion(tc.root(t)))
		})
	}
}

// A direct GenerateSubSkill call stamps the same declared release the harness
// pipeline stamps, resolved from the module that owns the skill file. The stamp
// lives in the generated comment fence, never in harness-parsed frontmatter.
func TestGenerateSubSkillStampsDeclaredReleaseVersion(t *testing.T) {
	root := t.TempDir()
	writePluginVersion(t, root, "1.2.3")
	skillDir := filepath.Join(root, "skills", "supervisor-plan-tasks")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillPath := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillPath, []byte(skillFileWithMarkers()), 0o644))

	result, err := codegen.GenerateSubSkill("cmd-sup-plan", skillPath, "", codegen.GenerateOptions{Diff: false, Write: false})
	require.NoError(t, err)
	require.Contains(t, result, "<!-- GENERATED FROM pasture v1.2.3 artifact v1 -->\n")
	require.NotContains(t, result, "pasture_version", "provenance must not enter harness-parsed frontmatter")
}

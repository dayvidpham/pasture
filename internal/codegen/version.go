package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// GeneratedArtifactFormat is the artifact bundle/manifest format version the
// generated payload is published under (artifact.bundle.v1, artifact.manifest.v1).
// The bundle identity itself cannot appear in a skill: it is a content hash over
// the skill bytes, so embedding it would be self-referential.
const GeneratedArtifactFormat = "v1"

// DefaultSourceVersion is stamped when no declared release version is
// available, matching the unstamped `pasture version` default.
const DefaultSourceVersion = "devel"

// pluginVersionFile is the declared-release source of truth: the release
// workflow tags v<version> from this value and stamps it into the binary.
const pluginVersionFile = ".claude-plugin/plugin.json"

type pluginVersionManifest struct {
	Version string `json:"version"`
}

// SourceVersion returns the declared Pasture release version for the module at
// root, spelled as the release tag (vX.Y.Z). Every generated skill carries this
// value in its frontmatter, so an installed SKILL.md names the Pasture release
// whose generator produced its bytes.
func SourceVersion(root string) string {
	if root == "" {
		return DefaultSourceVersion
	}
	raw, err := os.ReadFile(filepath.Join(root, pluginVersionFile))
	if err != nil {
		return DefaultSourceVersion
	}
	var manifest pluginVersionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return DefaultSourceVersion
	}
	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		return DefaultSourceVersion
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// versionForSkillPath resolves the same value for a direct GenerateSkill or
// GenerateSubSkill call, which receives a skill file but not the module root.
func versionForSkillPath(path string) string {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, pluginVersionFile)); err == nil {
			return SourceVersion(dir)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return DefaultSourceVersion
		}
	}
}

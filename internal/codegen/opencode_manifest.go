package codegen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// openCodeManifestEmitter emits the minimal opencode.json manifest at the repo
// root. The manifest tells OpenCode where to discover skills; agents are
// auto-discovered from .opencode/agent/ and are NOT listed here.
//
// The emitted file is committed at the repo root (not under .opencode/):
//
//	{
//	  "$schema": "https://opencode.ai/config.json",
//	  "skills": { "paths": [".opencode/skill"] }
//	}
//
// Only the opencode target emits this file. ClaudeCodeTarget.Manifest is nil,
// so the claude-code path never reaches this emitter.
type openCodeManifestEmitter struct{}

// openCodeManifestConfig is the typed representation of opencode.json. Using a
// named struct rather than map[string]any gives a deterministic field order in
// the MarshalIndent output and makes the schema contract explicit.
type openCodeManifestConfig struct {
	Schema string                 `json:"$schema"`
	Skills openCodeManifestSkills `json:"skills"`
}

type openCodeManifestSkills struct {
	Paths []string `json:"paths"`
}

// Emit writes opencode.json to <root>/opencode.json and returns a single
// GeneratedFile. Only the opencode target calls this; the claude-code target
// sets Manifest to nil, so this method is never invoked on that path.
func (openCodeManifestEmitter) Emit(root string, opts GenerateOptions) ([]GeneratedFile, error) {
	cfg := openCodeManifestConfig{
		Schema: "https://opencode.ai/config.json",
		Skills: openCodeManifestSkills{
			Paths: []string{".opencode/skill"},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf(
			"codegen.openCodeManifestEmitter.Emit: marshal opencode.json failed — "+
				"this is a bug in the manifest struct definition: %w",
			err,
		)
	}
	// Ensure single trailing newline (consistent with all other emitted files).
	content := string(data) + "\n"

	path := filepath.Join(root, "opencode.json")
	generated, err := writeFullGeneratedFile(path, content, opts)
	if err != nil {
		return nil, fmt.Errorf(
			"codegen.openCodeManifestEmitter.Emit: write %q failed — "+
				"check that the output root directory %q exists and is writable: %w",
			path, root, err,
		)
	}
	return []GeneratedFile{generated}, nil
}

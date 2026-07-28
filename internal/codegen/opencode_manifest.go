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
// Only the opencode target emits this file. Other targets use their own
// manifest emitters and never reach this emitter.
type openCodeManifestEmitter struct{}

// openCodeManifestConfig is the typed representation of opencode.json. Using a
// named struct rather than map[string]any gives a deterministic field order in
// the MarshalIndent output and makes the schema contract explicit.
type openCodeManifestConfig struct {
	Schema string                 `json:"$schema"`
	Plugin []string               `json:"plugin"`
	Skills openCodeManifestSkills `json:"skills"`
}

type openCodeManifestSkills struct {
	Paths []string `json:"paths"`
}

// Emit writes opencode.json to <root>/opencode.json and returns a single
// GeneratedFile. Only the opencode target calls this method.
func (openCodeManifestEmitter) Emit(root string, opts GenerateOptions) ([]GeneratedFile, error) {
	cfg := openCodeManifestConfig{
		Schema: "https://opencode.ai/config.json",
		Plugin: []string{"./" + filepath.ToSlash(OpenCodeHooksModulePath)},
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

	descriptor, err := NewOpenCodeTargetDescriptor()
	if err != nil {
		return nil, fmt.Errorf("codegen.openCodeManifestEmitter.Emit: build target descriptor: %w", err)
	}
	targetManifest, err := descriptor.Manifest()
	if err != nil {
		return nil, fmt.Errorf("codegen.openCodeManifestEmitter.Emit: render target manifest: %w", err)
	}
	outputs := []struct {
		path    string
		content string
	}{
		{path: filepath.Join(root, "opencode.json"), content: content},
		{path: filepath.Join(root, filepath.FromSlash(OpenCodeHooksModulePath)), content: descriptor.HooksModule()},
		{path: filepath.Join(root, filepath.FromSlash(OpenCodeTargetManifestPath)), content: targetManifest},
	}
	files := make([]GeneratedFile, 0, len(outputs))
	for _, output := range outputs {
		generated, err := writeFullGeneratedFile(output.path, output.content, opts)
		if err != nil {
			return nil, fmt.Errorf(
				"codegen.openCodeManifestEmitter.Emit: write %q failed - check that output root %q is writable: %w",
				output.path, root, err,
			)
		}
		files = append(files, generated)
	}
	return files, nil
}

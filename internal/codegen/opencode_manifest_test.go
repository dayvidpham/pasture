package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// openCodeConfig mirrors the structure of the emitted opencode.json for test
// decoding. The schema and skills fields are the only expected top-level keys.
// The agents field must be ABSENT (agents are auto-discovered by OpenCode from
// .opencode/agent/ and must NOT be listed in opencode.json).
type openCodeConfig struct {
	Schema string `json:"$schema"`
	Skills struct {
		Paths []string `json:"paths"`
	} `json:"skills"`
}

// TestOpenCodeManifestEmittedContent asserts the opencode.json manifest that
// the OpenCode target emits has exactly the minimal committed structure:
// $schema + skills.paths=[".opencode/skill"], no agents key, valid JSON.
func TestOpenCodeManifestEmittedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	emitter := openCodeManifestEmitter{}
	files, err := emitter.Emit(root, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("openCodeManifestEmitter.Emit: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("openCodeManifestEmitter.Emit returned %d files, want exactly 1 (opencode.json)", len(files))
	}

	f := files[0]

	// File path: <root>/opencode.json (repo root, not under .opencode/).
	wantPath := filepath.Join(root, "opencode.json")
	if f.Path != wantPath {
		t.Errorf("manifest path = %q, want %q", f.Path, wantPath)
	}

	// Content must be valid JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.Content), &raw); err != nil {
		t.Fatalf("opencode.json content is not valid JSON: %v\n---\n%s", err, f.Content)
	}

	// Decode into the typed struct to assert exact field values.
	var cfg openCodeConfig
	if err := json.Unmarshal([]byte(f.Content), &cfg); err != nil {
		t.Fatalf("opencode.json decode: %v", err)
	}

	const wantSchema = "https://opencode.ai/config.json"
	if cfg.Schema != wantSchema {
		t.Errorf("$schema = %q, want %q", cfg.Schema, wantSchema)
	}

	if len(cfg.Skills.Paths) != 1 || cfg.Skills.Paths[0] != ".opencode/skill" {
		t.Errorf("skills.paths = %v, want [%q]", cfg.Skills.Paths, ".opencode/skill")
	}

	// The agents key must be ABSENT: agents are auto-discovered from
	// .opencode/agent/ and must not be listed in the manifest.
	if _, hasAgents := raw["agents"]; hasAgents {
		t.Errorf("opencode.json must not carry an 'agents' key (agents are auto-discovered), got raw keys: %v", keys(raw))
	}

	// The only top-level keys expected are $schema and skills.
	for k := range raw {
		if k != "$schema" && k != "skills" {
			t.Errorf("unexpected top-level key %q in opencode.json — only $schema and skills are expected", k)
		}
	}
}

// TestOpenCodeManifestWritesToDisk asserts the Write path materializes
// opencode.json on disk in the temp root.
func TestOpenCodeManifestWritesToDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	emitter := openCodeManifestEmitter{}
	if _, err := emitter.Emit(root, GenerateOptions{Diff: false, Write: true}); err != nil {
		t.Fatalf("openCodeManifestEmitter.Emit(write): %v", err)
	}

	manifestPath := filepath.Join(root, "opencode.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("opencode.json not written to disk at %q: %v", manifestPath, err)
	}
	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("opencode.json on disk is not valid JSON: %v", err)
	}
	if cfg.Schema != "https://opencode.ai/config.json" {
		t.Errorf("on-disk $schema = %q, want https://opencode.ai/config.json", cfg.Schema)
	}
}

// TestClaudeCodeTargetEmitsNoManifest asserts the claude-code harness path
// does NOT emit opencode.json (ClaudeCodeTarget.Manifest == nil).
func TestClaudeCodeTargetEmitsNoManifest(t *testing.T) {
	t.Parallel()

	sourceRoot := testModuleRoot(t)
	outputRoot := t.TempDir()
	figuresDir := filepath.Join(sourceRoot, "skills", "protocol", "figures")
	files, err := EmitHarness(sourceRoot, outputRoot, ClaudeCodeTarget, figuresDir, GenerateOptions{Diff: false, Write: false})
	if err != nil {
		t.Fatalf("EmitHarness(claude-code): %v", err)
	}

	for _, f := range files {
		if filepath.Base(f.Path) == "opencode.json" {
			t.Errorf("claude-code target must not emit opencode.json, but got %q", f.Path)
		}
	}
}

// keys returns the keys of a map[string]json.RawMessage for error messages.
func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dayvidpham/pasture/internal/codegen"
)

type generatedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	files, err := codegen.EmitHarness(codegen.RepoRoots(root), codegen.CodexTarget, codegen.GenerateOptions{Write: false})
	if err != nil {
		return fmt.Errorf("generate Codex target snapshot from canonical emitter: %w", err)
	}
	wire := make([]generatedFile, 0, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generated Codex path %q is outside module root %q; emit every target file under the supplied root", file.Path, root)
		}
		wire = append(wire, generatedFile{Path: filepath.ToSlash(rel), Content: file.Content})
	}
	sort.Slice(wire, func(i, j int) bool { return wire[i].Path < wire[j].Path })
	encoded, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("encode generated Codex target snapshot: %w", err)
	}
	var compressed bytes.Buffer
	zipper, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("construct deterministic Codex snapshot compressor: %w", err)
	}
	zipper.Header.ModTime = zipper.Header.ModTime.UTC()
	zipper.Header.OS = 255
	if _, err := zipper.Write(encoded); err != nil {
		return fmt.Errorf("compress generated Codex target snapshot: %w", err)
	}
	if err := zipper.Close(); err != nil {
		return fmt.Errorf("finish generated Codex target snapshot: %w", err)
	}
	assets := filepath.Join(root, "internal", "target", "codex", "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		return fmt.Errorf("create Codex target asset directory %q: %w", assets, err)
	}
	destination := filepath.Join(assets, "codex-generated.json.gz")
	if err := os.WriteFile(destination, compressed.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write generated Codex target snapshot %q: %w", destination, err)
	}
	return nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve generator working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("find module root from %q: no go.mod exists in this directory or any parent; run go generate inside the Pasture module", dir)
		}
		dir = parent
	}
}

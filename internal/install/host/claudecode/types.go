package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
)

// CommandResult is the complete bounded native process result.
type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

// Runner is the injected native process boundary. Implementations execute the
// literal CommandSchema directly and never through a shell.
type Runner interface {
	Run(context.Context, activation.CommandSchema) (CommandResult, error)
}

// OSRunner is the production runner.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, schema activation.CommandSchema) (CommandResult, error) {
	const (
		maxStdout = 8 << 20
		maxStderr = 1 << 20
	)
	cmd := exec.CommandContext(ctx, schema.Program(), schema.Args()...)
	stdout := &boundedBuffer{limit: maxStdout}
	stderr := &boundedBuffer{limit: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if stdout.exceeded || stderr.exceeded {
		limitErr := fmt.Errorf("native command output exceeded the reviewed stdout/stderr limits (%d/%d bytes)", maxStdout, maxStderr)
		return result, fault("Claude native command", "bounded manager output", limitErr.Error(), schema.String(), "capturing Claude manager output", "the response was truncated and cannot prove state", "repair the noisy manager command and retry", limitErr)
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, fault("Claude native command", "completion before cancellation or deadline", fmt.Sprintf("%s stopped: %v", schema, ctxErr), schema.String(), "executing a bounded Claude manager step", "the requested state is unconfirmed and later actions must stop", "retry with a live context after repairing a manager timeout", errors.Join(err, ctxErr))
		}
		return result, fault("Claude native command", "zero exit with bounded output from reviewed argv", fmt.Sprintf("%s failed: %v; stderr: %s", schema, err, strings.TrimSpace(string(stderr.Bytes()))), schema.String(), "executing a Claude manager step", "the requested cell was not confirmed and later actions must stop", "repair the Claude installation or manager state, then rerun the full selection", err)
	}
	return result, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.exceeded = true
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) Bytes() []byte { return append([]byte(nil), b.buffer.Bytes()...) }

// ManifestReader reads version evidence from a native-reported install path.
// It is separate from Runner because versionless rows require exact filesystem
// evidence and no manager output may substitute for it.
type ManifestReader interface {
	ReadPluginManifest(installPath string) ([]byte, error)
}

type OSManifestReader struct{}

func (OSManifestReader) ReadPluginManifest(installPath string) ([]byte, error) {
	if !filepath.IsAbs(installPath) {
		return nil, fmt.Errorf("native install path %q is not absolute", installPath)
	}
	cleanRoot := filepath.Clean(installPath)
	if cleanRoot != installPath {
		return nil, fmt.Errorf("native install path %q is not clean (canonical form %q)", installPath, cleanRoot)
	}
	rootInfo, err := os.Lstat(cleanRoot)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("native install root %q is not a non-symlink directory", cleanRoot)
	}
	pluginDir := filepath.Join(cleanRoot, ".claude-plugin")
	dirInfo, err := os.Lstat(pluginDir)
	if err != nil {
		return nil, err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, fmt.Errorf("plugin metadata directory %q is not a non-symlink directory", pluginDir)
	}
	path := filepath.Join(cleanRoot, ".claude-plugin", "plugin.json")
	rel, err := filepath.Rel(cleanRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("manifest path %q escapes native install root %q", path, cleanRoot)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest %q is not a regular non-symlink file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	const maxManifestBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest %q exceeds the reviewed %d-byte limit", path, maxManifestBytes)
	}
	return data, nil
}

func fault(operation, expected, why, where, when, impact, fix string, cause error) error {
	return cell.NewFault(operation, expected, why, "internal/install/host/claudecode."+where, when, impact, fix, cause)
}

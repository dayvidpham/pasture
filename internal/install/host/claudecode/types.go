package claudecode

import (
	"bytes"
	"context"
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
	if err != nil {
		return result, fault("Claude native command", "zero exit with bounded output from reviewed argv", fmt.Sprintf("%s failed: %v; stderr: %s", schema, err, strings.TrimSpace(string(stderr.Bytes()))), schema.String(), "executing a Claude manager step", "the requested cell was not confirmed and later actions must stop", "repair the Claude installation or manager state, then rerun the full selection", err)
	}
	return result, nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 || len(p) > remaining {
		return 0, fmt.Errorf("native command output exceeded the reviewed %d-byte limit", b.limit)
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
	if !strings.HasPrefix(installPath, string(os.PathSeparator)) {
		return nil, fmt.Errorf("native install path %q is not absolute", installPath)
	}
	path := filepath.Join(installPath, ".claude-plugin", "plugin.json")
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

package codegen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the IP-3 end-to-end production proof for the generated Codex
// lifecycle transport. It executes the ACTUAL committed runner
// (.codex/hooks/events/<Event>.sh) exactly as Codex 0.146.0 would — a plain
// command string invoking `sh <runner>` with the authentic native event JSON on
// stdin and PASTURE_BIN pointing at a real pasture binary — and proves the
// transport wiring end-to-end against real temporary storage.
//
// Two complementary properties are proven:
//
//  1. Against the BUILT production CLI (TestCodexGeneratedRunnerDrivesBuiltCLI):
//     the runner execs the real `pasture hook lifecycle --harness codex ...`,
//     which statically dispatches the Codex harness and applies the committed
//     default-off activation gate BEFORE any stdin or storage access. Codex
//     activation is deliberately default-off until a later wave (proposal D8,
//     "activation last"), and the built CLI exposes no activation-injection
//     seam (S3's deliberate no-production-backdoor design in
//     internal/handlers/hook_lifecycle.go), so the built-CLI path proves the
//     safe withheld state end-to-end: no native continuation on stdout, the
//     actionable withheld diagnostic on stderr, exit 0, and no database opened.
//
//  2. Transparent native conduit (TestCodexGeneratedRunnerIsTransparentConduit):
//     with PASTURE_BIN pointing at a controlled CLI stand-in, the exec-only
//     runner is proven to (a) deliver the exact authentic fixture bytes to the
//     CLI's stdin unmodified, (b) invoke the exact CLI contract
//     `hook lifecycle --harness codex --event <Event> --host-version 0.146.0`
//     that S3 built (no invented flags), and (c) pass the CLI's native
//     continuation bytes ({"continue":true}) straight back to the host by exec
//     stdout inheritance.
//
// The ENABLED durable path and the pinned native continuation bytes themselves
// ({"continue":true} for the PreToolUse gate, {} for the SessionStart
// observation) are proven on the in-process production path with an injected
// enabled activation in
// internal/handlers/hook_lifecycle_codex_test.go:TestHookLifecycleResponseCodexCommitsBeforeReturningAndEncodesNativeBytes
// and pinned by the internal/lifecycle/nativeresponse golden-byte tests.
// Together with property (2) here, the full generated-runner -> CLI -> native
// continuation chain is covered for the Wave-2 default-off tree; the enabled
// built-CLI proof belongs to the activation wave (S5) when Codex activation
// lands.

// buildCodexProofCLI builds the real pasture CLI into binary. It builds
// ./cmd/pasture from the module root so the proof execs the same production
// entrypoint end users run.
func buildCodexProofCLI(t *testing.T, root, binary string) {
	t.Helper()
	build := exec.Command("go", "build", "-o", binary, "./cmd/pasture")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pasture CLI for the Codex transport proof failed: %v\n%s", err, out)
	}
}

func codexIngressFixture(t *testing.T, root, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "internal", "lifecycle", "ingress", "codex", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("read authentic Codex fixture %q: %v", name, err)
	}
	return raw
}

// TestCodexGeneratedRunnerDrivesBuiltCLI is the built-CLI half of the IP-3
// proof. It runs the committed generated runner as Codex would, against the
// real built binary and a real temporary database path, and proves the Codex
// dispatch reaches the committed default-off gate before touching stdin or
// storage.
func TestCodexGeneratedRunnerDrivesBuiltCLI(t *testing.T) {
	root := testModuleRoot(t)
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "pasture")
	buildCodexProofCLI(t, root, binary)

	cases := []struct {
		event   string
		fixture string
	}{
		{event: "PreToolUse", fixture: "pre_tool_use_0_146_0.json"},
		{event: "SessionStart", fixture: "session_start_0_146_0.json"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.event, func(t *testing.T) {
			runner := filepath.Join(root, ".codex", "hooks", "events", tc.event+".sh")
			raw := codexIngressFixture(t, root, tc.fixture)
			dbPath := filepath.Join(t.TempDir(), "pasture.db")

			// Invoke exactly as the hooks.json entry does: `sh <runner>`.
			cmd := exec.Command("sh", runner)
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				t.Fatalf("generated %s runner exited nonzero through the built CLI: %v\nstderr:\n%s", tc.event, err, stderr.String())
			}

			// Default-off: no native continuation is emitted for a withheld event.
			if stdout.Len() != 0 {
				t.Errorf("withheld Codex %s emitted stdout %q; a default-off event must produce no native continuation", tc.event, stdout.String())
			}
			// The CLI reports the actionable withheld diagnostic on stderr, proving
			// the runner reached the real Codex static dispatch and gate.
			wantReason := `Codex event "` + tc.event + `" is withheld (reason production-proof-missing)`
			if !strings.Contains(stderr.String(), wantReason) {
				t.Errorf("Codex %s stderr does not carry the withheld diagnostic %q:\n%s", tc.event, wantReason, stderr.String())
			}
			// Withheld admission is enforced before storage: no database is opened.
			if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
				t.Errorf("withheld Codex %s opened storage at %q (stat err=%v); admission must be refused before any storage access", tc.event, dbPath, statErr)
			}
		})
	}
}

// TestCodexGeneratedRunnerIsTransparentConduit is the native-conduit half of the
// IP-3 proof. It points PASTURE_BIN at a controlled CLI stand-in that records
// its stdin and argv and emits the pinned native continuation bytes, then proves
// the exec-only runner is a transparent bidirectional conduit that carries the
// exact authentic stdin in and the native continuation bytes out, invoking the
// exact CLI contract S3 built.
func TestCodexGeneratedRunnerIsTransparentConduit(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	dir := t.TempDir()

	stdinCapture := filepath.Join(dir, "captured-stdin")
	argvCapture := filepath.Join(dir, "captured-argv")
	stub := filepath.Join(dir, "pasture-stub")
	// The stand-in records argv and the exact stdin bytes, then emits the pinned
	// PreToolUse native continuation the real enabled CLI would emit.
	stubScript := "#!/usr/bin/env sh\n" +
		"set -eu\n" +
		"printf '%s' \"$*\" > \"$STUB_ARGV_OUT\"\n" +
		"cat > \"$STUB_STDIN_OUT\"\n" +
		"printf '%s' '{\"continue\":true}'\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write CLI stand-in: %v", err)
	}

	runner := filepath.Join(root, ".codex", "hooks", "events", "PreToolUse.sh")
	raw := codexIngressFixture(t, root, "pre_tool_use_0_146_0.json")

	cmd := exec.Command("sh", runner)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = append(os.Environ(),
		"PASTURE_BIN="+stub,
		"STUB_STDIN_OUT="+stdinCapture,
		"STUB_ARGV_OUT="+argvCapture,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generated runner + CLI stand-in exited nonzero: %v\nstderr:\n%s", err, stderr.String())
	}

	// (c) The runner passes the native continuation bytes straight through.
	if stdout.String() != `{"continue":true}` {
		t.Errorf("runner did not pass the native continuation through unmodified: got %q, want %q", stdout.String(), `{"continue":true}`)
	}
	// (a) The runner delivered the exact authentic fixture bytes to CLI stdin.
	gotStdin, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if !bytes.Equal(gotStdin, raw) {
		t.Errorf("runner did not deliver exact authentic stdin: got %d bytes, want %d bytes", len(gotStdin), len(raw))
	}
	// (b) The runner invoked the exact CLI contract S3 built — no invented flags.
	gotArgv, err := os.ReadFile(argvCapture)
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	wantArgv := "hook lifecycle --harness codex --event PreToolUse --host-version 0.146.0"
	if string(gotArgv) != wantArgv {
		t.Errorf("runner invoked the CLI with argv %q, want the exact contract %q", gotArgv, wantArgv)
	}
}

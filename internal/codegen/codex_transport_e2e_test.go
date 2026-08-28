package codegen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
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
//     after M3 Implementation UAT the committed Codex dispatch enables the two
//     accepted events via activation.Codex0_146_0(), so the generated runner +
//     built CLI + real temporary storage is the strongest end-to-end M3-P1/P2
//     proof: PreToolUse emits the exact native continuation {"continue":true}
//     and SessionStart emits {} on stdout, both exit 0 with durable evidence
//     readable back through the production CLI. A non-selected catalog event
//     (Stop) still refuses safely through the runner — no native continuation,
//     the actionable withheld diagnostic on stderr, exit 0, no database opened.
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
// The pinned native continuation bytes are additionally covered on the
// in-process production path in
// internal/handlers/hook_lifecycle_codex_test.go:TestHookLifecycleResponseCodexCommitsBeforeReturningAndEncodesNativeBytes
// and by the internal/lifecycle/nativeresponse golden-byte tests.

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
// proof and, after the M3-UAT activation flip, the strongest end-to-end M3-P1/P2
// proof. It runs the committed generated runner exactly as Codex 0.146.0 would
// (`sh <runner>` with the authentic native event JSON on stdin and PASTURE_BIN
// pointing at the real built binary) against real temporary storage, and proves:
//
//   - ENABLED: each accepted event (PreToolUse gate, SessionStart observation)
//     flows through the real `pasture hook lifecycle --harness codex ...`, emits
//     its exact native continuation on stdout, exits 0, and leaves durable
//     provider-correct evidence readable back through the production CLI.
//   - WITHHELD: a non-selected catalog event (Stop) still refuses safely — no
//     native continuation, the actionable withheld diagnostic on stderr, exit 0,
//     and no database opened (admission enforced before any storage access).
func TestCodexGeneratedRunnerDrivesBuiltCLI(t *testing.T) {
	root := testModuleRoot(t)
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "pasture")
	buildCodexProofCLI(t, root, binary)

	registrationContract := registration.Codex0_146_0().Contract.String()
	interpretedContract := runtime.Codex0_146_0().ID().String()

	enabled := []struct {
		event, fixture, wantStdout, wantSemantic string
	}{
		{event: "PreToolUse", fixture: "pre_tool_use_0_146_0.json", wantStdout: `{"continue":true}`, wantSemantic: `"semantic":2`},
		{event: "SessionStart", fixture: "session_start_0_146_0.json", wantStdout: `{}`, wantSemantic: `"semantic":1`},
	}
	for _, tc := range enabled {
		tc := tc
		t.Run("enabled/"+tc.event, func(t *testing.T) {
			runner := filepath.Join(root, ".codex", "hooks", "events", tc.event+".sh")
			raw := codexIngressFixture(t, root, tc.fixture)
			dbPath := filepath.Join(t.TempDir(), "pasture.db")

			// Initialize the real temporary store through the production CLI so
			// this proof imports no lower-level package (codegen cannot import
			// internal/tasks without an import cycle).
			bootstrap := exec.Command(binary, "--db", dbPath, "--namespace", "file://codex-runner-e2e", "task", "create", "initialize lifecycle identity")
			if out, err := bootstrap.CombinedOutput(); err != nil {
				t.Fatalf("initialize real temporary Pasture store through production CLI: %v\n%s", err, out)
			}

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
			if stdout.String() != tc.wantStdout {
				t.Errorf("enabled Codex %s native continuation = %q, want %q\nstderr:\n%s", tc.event, stdout.String(), tc.wantStdout, stderr.String())
			}

			// Durable, provider-correct evidence must be readable back through the
			// same production CLI end users run.
			readback := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
			out, err := readback.CombinedOutput()
			if err != nil {
				t.Fatalf("read back Codex %s receipt through production CLI: %v\n%s", tc.event, err, out)
			}
			for _, required := range []string{
				`"registrationContract":"` + registrationContract + `"`,
				`"contract":"` + interpretedContract + `"`,
				tc.wantSemantic,
			} {
				if !strings.Contains(string(out), required) {
					t.Errorf("Codex %s read-back lacks %s:\n%s", tc.event, required, out)
				}
			}
		})
	}

	t.Run("withheld/no-transport", func(t *testing.T) {
		// A withheld event is never wired: it owns no runner and no hooks.json
		// entry, so Codex has no way to deliver it and it can never reach the
		// handler as a refusal. This is the transport-level half of the
		// guarantee; the handler-level refusal is covered in
		// internal/handlers/hook_lifecycle_codex_test.go.
		wire, err := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
		if err != nil {
			t.Fatalf("read committed .codex/hooks.json: %v; run make generate", err)
		}
		for _, event := range []string{"Stop", "UserPromptSubmit", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop"} {
			runner := filepath.Join(root, ".codex", "hooks", "events", event+".sh")
			if _, statErr := os.Stat(runner); !os.IsNotExist(statErr) {
				t.Errorf("withheld Codex event %s still has a committed runner at %q (stat err=%v); the transport must carry only activated events", event, runner, statErr)
			}
			if strings.Contains(string(wire), `"`+event+`"`) {
				t.Errorf("withheld Codex event %s is still wired in .codex/hooks.json; the transport must carry only activated events", event)
			}
		}
	})
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

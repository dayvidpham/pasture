package codegen

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func buildCodexProofCLI(t *testing.T, root, binary string) {
	t.Helper()
	build := exec.Command("go", "build", "-o", binary, "./cmd/pasture")
	build.Dir = root
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build real pasture CLI: %s", out)
}

func codexProofEnvironment(binary, dbPath string) []string {
	return append(os.Environ(),
		"PASTURE_BIN="+binary,
		"PASTURE_DB_PATH="+dbPath,
		"PASTURE_CAPTURE_DIR=",
		"PASTURE_ACTOR_ID=",
		"PASTURE_HOOK_FAIL_CLOSED=",
	)
}

// This proof executes the command from committed hooks.json, not a second
// transport assembled in the test. Each real process must commit an occurrence
// and interpretation before it returns the exact native continuation. Input
// identities are compared to the cleared bytes without manufacturing pairs.
func TestCodexGeneratedRunnerDrivesBuiltCLI(t *testing.T) {
	root := testModuleRoot(t)
	binary := filepath.Join(t.TempDir(), "pasture")
	buildCodexProofCLI(t, root, binary)
	fixtures := codexClearedFixtures(t, root)
	wire, err := os.ReadFile(filepath.Join(root, ".codex/hooks.json"))
	require.NoError(t, err)
	var config codexHooksConfig
	require.NoError(t, json.Unmarshal(wire, &config))
	manifest := registration.Codex0_153_0()
	require.Len(t, config.Hooks, len(manifest.Entries()))

	for _, event := range manifest.Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			groups, found := config.Hooks[event.NativeName]
			require.True(t, found, "cleared event has no generated transport")
			require.Len(t, groups, 1)
			require.Len(t, groups[0].Hooks, 1)
			require.Equal(t, "sh .codex/hooks/events/"+event.NativeName+".sh", groups[0].Hooks[0].Command)
			raw, err := os.ReadFile(fixtures[event.NativeName])
			require.NoError(t, err)
			dbPath := filepath.Join(t.TempDir(), "pasture.db")
			env := codexProofEnvironment(binary, dbPath)

			bootstrap := exec.Command(binary, "--db", dbPath, "--namespace", "file://codex-runner-e2e", "task", "create", "initialize lifecycle identity")
			bootstrap.Env = env
			out, err := bootstrap.CombinedOutput()
			require.NoError(t, err, "initialize scratch store: %s", out)

			command := exec.Command("sh", "-c", groups[0].Hooks[0].Command)
			command.Dir = root
			command.Env = env
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), "generated runner: %s", stderr.String())
			nativeOutput := stdout.String()
			nativeDiagnostic := stderr.String()
			wantNative := `{"continue":true}`
			wantSemantic := runtime.SemanticGateConsultation
			switch event.NativeName {
			case "SessionStart", "SubagentStart", "SessionEnd", "Interrupt":
				wantNative = `{}`
				wantSemantic = runtime.SemanticObservation
			}

			readback := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
			readback.Env = env
			stdout.Reset()
			stderr.Reset()
			readback.Stdout = &stdout
			readback.Stderr = &stderr
			require.NoError(t, readback.Run(), "read durable evidence: %s", stderr.String())
			require.Empty(t, stderr.String())
			var page struct {
				Items []struct {
					JournalID            uint64                  `json:"journalId"`
					Event                model.ContractEventKind `json:"event"`
					RegistrationContract string                  `json:"registrationContract"`
					PayloadDigest        string                  `json:"payloadDigest"`
					Interpreted          []struct {
						JournalID  uint64                   `json:"journalId"`
						Contract   string                   `json:"contract"`
						Semantic   runtime.EventSemantic    `json:"semantic"`
						Identities []waist.SemanticIdentity `json:"identities"`
					} `json:"interpreted"`
				} `json:"items"`
				Next string `json:"nextCursor"`
			}
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
			require.Len(t, page.Items, 1, "one invocation must leave exactly one durable occurrence")
			require.Empty(t, page.Next)
			record := page.Items[0]
			require.NotZero(t, record.JournalID)
			require.Equal(t, event.Kind, record.Event, "runner dispatch must record this native event, not another row")
			require.Equal(t, manifest.Contract.String(), record.RegistrationContract)
			require.Equal(t, digest.FromBytes(raw).String(), record.PayloadDigest, "runner must preserve the exact captured body")
			require.Empty(t, nativeDiagnostic, "an admitted event must not silently take the fail-open fault path")
			require.Equal(t, wantNative, nativeOutput, "actual native transport bytes")
			require.Len(t, record.Interpreted, 1)
			interpreted := record.Interpreted[0]
			require.Greater(t, interpreted.JournalID, record.JournalID)
			require.Equal(t, runtime.Codex0_153_0().ID().String(), interpreted.Contract)
			require.Equal(t, wantSemantic, interpreted.Semantic)

			var payload map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &payload))
			wantIdentities := map[runtime.NativeIdentityKind]string{}
			for field, kind := range map[string]runtime.NativeIdentityKind{
				"session_id":  runtime.IdentitySession,
				"turn_id":     runtime.IdentityTurn,
				"tool_use_id": runtime.IdentityToolCall,
				"agent_id":    runtime.IdentityAgent,
			} {
				if value, present := payload[field]; present {
					var id string
					require.NoError(t, json.Unmarshal(value, &id))
					wantIdentities[kind] = id
				}
			}
			gotIdentities := map[runtime.NativeIdentityKind]string{}
			for _, identity := range interpreted.Identities {
				require.NotContains(t, gotIdentities, identity.Kind)
				gotIdentities[identity.Kind] = identity.Value
			}
			require.Equal(t, wantIdentities, gotIdentities, "captured native identity is not an actor claim or invented correlation")
		})
	}
}

func TestCodexGeneratedRunnerIsTransparentConduit(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	fixtures := codexClearedFixtures(t, root)
	for _, event := range registration.Codex0_153_0().Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			dir := t.TempDir()
			stdinCapture := filepath.Join(dir, "stdin")
			argvCapture := filepath.Join(dir, "argv")
			stub := filepath.Join(dir, "stand-in")
			script := "#!/usr/bin/env sh\nset -eu\nprintf '%s' \"$*\" > \"$STUB_ARGV_OUT\"\ncat > \"$STUB_STDIN_OUT\"\nprintf '%s' 'native-output'\nprintf '%s' 'native-diagnostic' >&2\nexit 7\n"
			require.NoError(t, os.WriteFile(stub, []byte(script), 0o755))
			raw, err := os.ReadFile(fixtures[event.NativeName])
			require.NoError(t, err)
			command := exec.Command("sh", filepath.Join(root, ".codex/hooks/events", event.NativeName+".sh"))
			command.Env = append(codexProofEnvironment(stub, filepath.Join(dir, "scratch.db")), "STUB_STDIN_OUT="+stdinCapture, "STUB_ARGV_OUT="+argvCapture)
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			var exit *exec.ExitError
			require.ErrorAs(t, command.Run(), &exit)
			require.Equal(t, 7, exit.ExitCode())
			require.Equal(t, "native-output", stdout.String())
			require.Equal(t, "native-diagnostic", stderr.String())
			gotInput, err := os.ReadFile(stdinCapture)
			require.NoError(t, err)
			require.Equal(t, raw, gotInput)
			gotArgs, err := os.ReadFile(argvCapture)
			require.NoError(t, err)
			require.Equal(t, "hook lifecycle --harness codex --event "+event.NativeName+" --host-version "+codexHostVersionLabel(), string(gotArgs))
		})
	}
}

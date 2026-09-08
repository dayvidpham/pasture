package codegen

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
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
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "pasture")
	buildCodexProofCLI(t, root, binary)
	fixtures := codexClearedFixtures(t, root)
	wire, err := os.ReadFile(filepath.Join(root, ".codex/hooks.json"))
	require.NoError(t, err)
	var config codexHooksConfig
	require.NoError(t, json.Unmarshal(wire, &config))
	manifest := registration.Codex0_153_0()
	require.Len(t, config.Hooks, len(manifest.Entries()))
	// Copy the embedded production runners once into a realistic global layout.
	// Only the executable observed at the version boundary changes thereafter.
	compressed, err := os.ReadFile(filepath.Join(root, "internal/target/codex/assets/codex-generated.json.gz"))
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	snapshot, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close())
	var files []struct{ Path, Content string }
	require.NoError(t, json.Unmarshal(snapshot, &files))
	home := t.TempDir()
	installed := map[string]string{}
	for _, file := range files {
		if filepath.Dir(file.Path) != ".codex/hooks/events" {
			continue
		}
		original, err := os.ReadFile(filepath.Join(root, file.Path))
		require.NoError(t, err)
		require.Equal(t, string(original), file.Content, "embedded runner must equal generated runner")
		path := filepath.Join(home, file.Path)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(file.Content), 0o700))
		installed[path] = digest.FromString(file.Content).String()
	}
	require.Len(t, installed, len(manifest.Entries()))
	global, err := EmitCodexGlobalHooksConfig()
	require.NoError(t, err)
	var globalConfig codexHooksConfig
	require.NoError(t, json.Unmarshal([]byte(global.Content), &globalConfig))
	path := t.TempDir()
	require.NoError(t, os.Symlink(shell, filepath.Join(path, "sh")))
	hostExecutable := filepath.Join(path, "codex")
	marker := filepath.Join(t.TempDir(), "queries")
	for _, version := range []string{"0.154.1", "0.155.2-beta.1+update.7"} {
		script := "#!" + shell + "\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 91\nprintf x >> '" + marker + "'\nprintf '%s\\n' 'codex-cli " + version + "'\n"
		require.NoError(t, os.WriteFile(hostExecutable, []byte(script), 0o700))
		for _, event := range manifest.Entries() {
			t.Run(event.NativeName, func(t *testing.T) {
				t.Logf("installed executable observation: %s", version)
				groups, found := config.Hooks[event.NativeName]
				require.True(t, found, "cleared event has no generated transport")
				require.Len(t, groups, 1)
				require.Len(t, groups[0].Hooks, 1)
				require.Equal(t, "sh .codex/hooks/events/"+event.NativeName+".sh", groups[0].Hooks[0].Command)
				raw, err := os.ReadFile(fixtures[event.NativeName])
				require.NoError(t, err)
				dbPath := filepath.Join(t.TempDir(), "pasture.db")
				env := append(codexProofEnvironment(binary, dbPath), "PATH="+path, "HOME="+home)

				bootstrap := exec.Command(binary, "--db", dbPath, "--namespace", "file://codex-runner-e2e", "task", "create", "initialize lifecycle identity")
				bootstrap.Env = env
				out, err := bootstrap.CombinedOutput()
				require.NoError(t, err, "initialize scratch store: %s", out)

				require.Equal(t, "sh ~/.codex/hooks/events/"+event.NativeName+".sh", globalConfig.Hooks[event.NativeName][0].Hooks[0].Command)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, shell, "-c", globalConfig.Hooks[event.NativeName][0].Hooks[0].Command)
				command.Dir = home
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
				tracker, err := tasks.OpenTaskTracker(dbPath)
				require.NoError(t, err)
				defer tracker.Close()
				require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
				reader, err := tasks.NewLifecycleReader(tracker)
				require.NoError(t, err)
				size, err := model.NewPageSize(1)
				require.NoError(t, err)
				observed, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
				require.NoError(t, err)
				require.Len(t, observed.Records(), 1)
				occurrence := observed.Records()[0].Occurrence
				require.Equal(t, version, occurrence.Envelope.HostVersion, "installed runner must observe the current executable, not its generation baseline")
				require.Equal(t, model.HostVersionExecutableQuery, occurrence.Envelope.HostVersionSource)
				require.Equal(t, model.CaptureValid, occurrence.Capture)
				body, err := reader.Payload(context.Background(), occurrence.Payload.Digest)
				require.NoError(t, err)
				require.Equal(t, raw, body, "durable raw body must remain byte-identical")
			})
		}
		for path, hash := range installed {
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, hash, digest.FromBytes(body).String(), "host update must not rewrite installed artifacts")
		}
	}
	queries, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, bytes.Repeat([]byte("x"), 2*len(manifest.Entries())), queries, "one version query per installed route per update")
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
			require.Equal(t, "hook lifecycle --harness codex --event "+event.NativeName, string(gotArgs))
		})
	}
}

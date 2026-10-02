package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	opencodetarget "github.com/dayvidpham/pasture/internal/target/opencode"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// This is an installed-transport compatibility proof, not a live host capture.
// Version executables and in-memory metadata/identity variants are test controls.
func TestInstalledOpenCodePluginObservesUpdatesWithoutReinstall(t *testing.T) {
	bun, err := exec.LookPath("bun")
	require.NoError(t, err)
	binary := lifecycleBinary(t)
	target, err := opencodetarget.Descriptor()
	require.NoError(t, err)
	module, err := fs.ReadFile(target.Hooks().Bundle(), "pasture-hooks.ts")
	require.NoError(t, err)
	dir := t.TempDir()
	installed := filepath.Join(dir, ".config", "opencode", "plugins", "pasture-hooks.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o700))
	require.NoError(t, os.WriteFile(installed, module, 0o600)) // exactly one install
	installedHash := sha256.Sum256(module)
	fixtureDir := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	sessionRaw, err := os.ReadFile(filepath.Join(fixtureDir, "session_created_1_18_29.json"))
	require.NoError(t, err)
	toolRaw, err := os.ReadFile(filepath.Join(fixtureDir, "tool_execute_before_1_18_29.json"))
	require.NoError(t, err)

	for _, variant := range []string{"accepted", "new creation version", "absent", "empty", "whitespace", "number", "null", "object", "array", "bool", "missing session", "unusable session", "missing call"} {
		t.Run(variant, func(t *testing.T) {
			var session, tool map[string]any
			require.NoError(t, json.Unmarshal(sessionRaw, &session))
			require.NoError(t, json.Unmarshal(toolRaw, &tool))
			properties := session["event"].(map[string]any)["properties"].(map[string]any)
			creationVersion := "1.18.29"
			creationSource := model.HostVersionCallerSupplied
			creationValid, toolValid := true, true
			// The bus event carries the v2 data envelope the generated
			// helper reads the occurrence-local flag from, alongside the v1
			// nested shape the 1.18.29 row under test parses. Variants
			// mutate data.version; the nested fixture shape stays
			// byte-identical except where the variant removes an identity.
			data := map[string]any{"version": creationVersion}
			session["data"] = data
			switch variant {
			case "new creation version":
				data["version"], creationVersion = "1.22.0+runtime", "1.22.0+runtime"
			case "absent":
				delete(data, "version")
			case "empty":
				data["version"] = ""
			case "whitespace":
				data["version"] = " \t\n"
			case "number":
				data["version"] = 42
			case "null":
				data["version"] = nil
			case "object":
				data["version"] = map[string]any{"version": "1.18.29"}
			case "array":
				data["version"] = []any{"1.18.29"}
			case "bool":
				data["version"] = true
			case "missing session":
				delete(properties, "sessionID")
				creationValid = false
			case "unusable session":
				properties["sessionID"] = " "
				creationValid = false
			case "missing call":
				delete(tool["input"].(map[string]any), "callID")
				toolValid = false
			}
			if value, ok := data["version"].(string); !ok || strings.TrimSpace(value) == "" {
				creationSource = model.HostVersionExecutableQuery
			}
			sessionBytes, err := json.Marshal(session)
			require.NoError(t, err)
			toolBytes, err := json.Marshal(tool)
			require.NoError(t, err)
			// The bus event always carries the v2 data envelope beside the
			// v1 nested shape, so every route uses a newly serialized
			// payload; the nested fixture content stays byte-identical
			// except where the variant removes an identity.
			if variant != "missing call" {
				toolBytes = toolRaw
			}
			scratch := t.TempDir()
			path := filepath.Join(scratch, "bin")
			require.NoError(t, os.Mkdir(path, 0o700))
			executable := filepath.Join(path, "opencode")
			marker := filepath.Join(scratch, "queries")
			// The executable's path stays fixed. Each query appends, so exact counts
			// detect a cached value, a second query, or an unexpected metadata query.
			banners := []string{"1.19.0", "1.20.0+updated"}
			var scripts []string
			dbPath := filepath.Join(scratch, tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			for _, version := range banners {
				scripts = append(scripts, "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 9\nprintf 'x' >> '"+marker+"'\nprintf '"+version+"\\n'\n")
			}
			require.NoError(t, os.WriteFile(executable, []byte(scripts[0]), 0o700))
			runner := filepath.Join(scratch, "installed.ts")
			jsonScripts, err := json.Marshal(scripts)
			require.NoError(t, err)
			// The installed plugin is driven through its v2 shape: setup on
			// a capturing context, then the exported helpers with the same
			// v1 fixture objects the retired server factory received. The
			// helpers forward verbatim, so the bytes the CLI parses are the
			// fixture bytes; the bus event additionally carries the v2
			// top-level version the helper forwards as the occurrence-local
			// flag. The plugin performs no runtime imports, so it loads with no
			// stub beside the fake home.
			code := fmt.Sprintf(`
import assert from "node:assert/strict";
import {writeFileSync} from "node:fs";
const {default: plugin} = await import(%q);
assert.ok(plugin && typeof plugin === "object" && !Array.isArray(plugin), "installed V2 default must be an object");
assert.equal(plugin.id, "pasture-lifecycle");
assert.equal(typeof plugin.setup, "function", "installed V2 default must expose setup()");
const hooks = {};
const ctx = {
  session: { hook: async (name, cb) => { hooks["session." + name] = cb; return { dispose: async () => {} }; } },
  tool: { hook: async (name, cb) => { hooks["tool." + name] = cb; return { dispose: async () => {} }; } },
  permission: { hook: async (name, cb) => { hooks["permission." + name] = cb; return { dispose: async () => {} }; } },
  shell: { hook: async (name, cb) => { hooks["shell." + name] = cb; return { dispose: async () => {} }; } },
  event: { subscribe: () => (async function* () {})() },
};
const cleanup = await plugin.setup(ctx);
assert.equal(typeof cleanup, "function", "setup returns its cleanup");
const { sessionCreated, toolExecuteBefore } = await import(%q);
const session = %s, tool = %s;
const freeze = value => { if (value && typeof value === "object") { Object.values(value).forEach(freeze); Object.freeze(value); } return value; };
freeze(session); freeze(tool);
const sessionBefore = JSON.stringify(session), toolBefore = JSON.stringify(tool);
const scripts = %s;
const calls = [];
const spawn = Bun.spawn;
// Observe real child bytes with a tee; never replace the CLI or its response.
Bun.spawn = options => {
  const child = spawn(options);
  const [stdout, observedOut] = child.stdout.tee();
  const [stderr, observedErr] = child.stderr.tee();
  const observed = Promise.all([options.stdin.text(), new Response(observedOut).text(), new Response(observedErr).text(), child.exited]);
  calls.push(observed);
  return {stdout, stderr, exited:child.exited, get exitCode(){return child.exitCode;}, kill:signal=>child.kill(signal)};
};
try {
  for (let index = 0; index < scripts.length; index++) {
    writeFileSync(%q, scripts[index], {mode:0o700});
    await sessionCreated(session);
    await toolExecuteBefore(tool);
    assert.equal(JSON.stringify(session), sessionBefore);
    assert.equal(JSON.stringify(tool), toolBefore);
  }
  console.log(JSON.stringify(await Promise.all(calls)));
} finally { Bun.spawn = spawn; }
`, installed, installed, sessionBytes, toolBytes, jsonScripts, executable)
			require.NoError(t, os.WriteFile(runner, []byte(code), 0o600))
			command := exec.Command(bun, runner)
			command.Env = discoveryChildEnv(map[string]*string{"PATH": &path, "PASTURE_BIN": &binary,
				"PASTURE_DB_PATH": &dbPath, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			var calls [][]json.RawMessage
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &calls), stdout.String())
			require.Len(t, calls, 4)
			queryCount := 2
			if creationSource == model.HostVersionExecutableQuery {
				queryCount = 4
			}
			queries, err := os.ReadFile(marker)
			require.NoError(t, err, "tool callbacks must query the installed executable, not supply a compiled or cached version")
			require.Equal(t, strings.Repeat("x", queryCount), string(queries), "each selected executable must be queried exactly once; creation metadata bypasses queries")
			for index := range banners {
				observedCreationVersion := creationVersion
				if creationSource == model.HostVersionExecutableQuery {
					observedCreationVersion = banners[index]
				}
				assertInstalledOpenCodeOccurrence(t, binary, dbPath, index, sessionBytes, toolBytes, observedCreationVersion, banners[index], creationSource, creationValid, toolValid)
				for eventIndex, raw := range [][]byte{sessionBytes, toolBytes} {
					call := calls[index*2+eventIndex]
					require.Len(t, call, 4)
					var body, native, diagnostic string
					var exit int
					require.NoError(t, json.Unmarshal(call[0], &body))
					require.NoError(t, json.Unmarshal(call[1], &native))
					require.NoError(t, json.Unmarshal(call[2], &diagnostic))
					require.NoError(t, json.Unmarshal(call[3], &exit))
					require.Equal(t, string(raw), body)
					require.Zero(t, exit)
					if eventIndex == 0 {
						require.Empty(t, native)
					} else {
						require.Equal(t, `{"decision":"proceed"}`, native)
					}
					valid := (eventIndex == 0 && creationValid) || (eventIndex == 1 && toolValid)
					if valid {
						require.Empty(t, diagnostic)
					} else {
						require.Contains(t, diagnostic, "WAS NOT EVALUATED")
					}
				}
			}
			current, err := os.ReadFile(installed)
			require.NoError(t, err)
			require.Equal(t, installedHash, sha256.Sum256(current), "host update must not rewrite the installed plugin")
		})
	}
}

func assertInstalledOpenCodeOccurrence(t *testing.T, binary, dbPath string, updateIndex int, sessionRaw, toolRaw []byte, creationVersion, toolVersion string, creationSource model.HostVersionSource, creationValid, toolValid bool) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 4, "both updates must append distinct occurrences in the same scratch store")
	sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].JournalID < occurrences[j].JournalID })
	occurrences = occurrences[updateIndex*2 : updateIndex*2+2]
	require.Equal(t, registration.EventOpenCodeSessionCreated, decodeOccurrencePayload(t, occurrences[0].Payload).Event)
	require.Equal(t, registration.EventOpenCodeToolExecuteBefore, decodeOccurrencePayload(t, occurrences[1].Payload).Event)
	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	consultations := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	wantInterpreted, wantConsultations := 0, 0
	if creationValid {
		wantInterpreted++
	}
	if toolValid {
		wantInterpreted++
		wantConsultations++
	}
	require.Len(t, interpreted, 2*wantInterpreted)
	require.Len(t, consultations, 2*wantConsultations)
	sort.Slice(consultations, func(i, j int) bool { return consultations[i].JournalID < consultations[j].JournalID })
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	size, err := model.NewPageSize(4)
	require.NoError(t, err)
	page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
	require.NoError(t, err)
	require.Len(t, page.Records(), 4)
	for _, row := range occurrences {
		payload := decodeOccurrencePayload(t, row.Payload)
		raw, version, source, valid := toolRaw, toolVersion, model.HostVersionExecutableQuery, toolValid
		semantic := runtime.SemanticGateConsultation
		identities := []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "ses_f8e723e13ffeUWnfE2vlRBO1xN"}, {Kind: uint8(runtime.IdentityToolCall), Value: "call_4zMdLgUBV12aE7yHvuYQolSx"}}
		if payload.Event == registration.EventOpenCodeSessionCreated {
			raw, version, source, valid = sessionRaw, creationVersion, creationSource, creationValid
			semantic = runtime.SemanticObservation
			identities = identities[:1]
		} else {
			require.Equal(t, registration.EventOpenCodeToolExecuteBefore, payload.Event)
		}
		require.Equal(t, registration.OpenCode1_18_29().Contract.String(), payload.Contract)
		require.Equal(t, registration.OpenCode1_18_29().Contract, payload.Envelope.Runtime.Contract)
		require.Equal(t, version, payload.Envelope.HostVersion)
		require.Equal(t, source, payload.Envelope.HostVersionSource)
		require.Equal(t, digest.FromBytes(raw).String(), payload.Body)
		body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
		require.NoError(t, err)
		require.Equal(t, raw, body)
		matches := 0
		for _, irRow := range interpreted {
			if irRow.ProducingOperationID != row.ProducingOperationID {
				continue
			}
			matches++
			assertSharedOperation(t, row, irRow)
			value := decodeInterpretedPayload(t, irRow.Payload)
			require.Equal(t, runtime.OpenCode1_18_29().ID().String(), value.Contract)
			require.Equal(t, uint8(semantic), value.Semantic)
			require.Equal(t, identities, value.Identities)
			require.Empty(t, value.UnresolvedFacts)
		}
		if valid {
			require.Equal(t, model.CaptureValid, payload.Capture)
			require.Equal(t, 1, matches)
			bindings := []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "sessionID", Value: identities[0].Value}}
			if payload.Event == registration.EventOpenCodeToolExecuteBefore {
				bindings = append(bindings, lifecycleBindingPayload{Kind: model.BindingToolCall, NativeName: "callID", Value: identities[1].Value})
			}
			require.Equal(t, bindings, payload.Bindings)
		} else {
			require.Equal(t, model.CaptureUnsupportedSchema, payload.Capture)
			require.Empty(t, payload.Bindings)
			require.Zero(t, matches)
		}
		projected := 0
		for _, record := range page.Records() {
			if record.Occurrence.JournalID() != row.JournalID {
				continue
			}
			projected++
			require.Equal(t, payload.Envelope, record.Occurrence.Envelope)
			require.Len(t, record.Interpreted(), matches)
		}
		require.Equal(t, 1, projected)
		if valid && payload.Event == registration.EventOpenCodeToolExecuteBefore {
			require.Equal(t, row.ProducingOperationID, consultations[updateIndex].ProducingOperationID)
			require.Equal(t, row.ProducingOperationJournalID, consultations[updateIndex].ProducingOperationJournalID)
			assertUnboundProceedConsultation(t, consultations[updateIndex].Payload)
		}
	}
	faultPath := filepath.Join(filepath.Dir(dbPath), lifecycleFaultRecordFile)
	if creationValid && toolValid {
		_, err := os.Stat(faultPath)
		require.ErrorIs(t, err, os.ErrNotExist, "native continuation must reflect real evaluation, not a fail-open fault")
	} else {
		fault, err := os.ReadFile(faultPath)
		require.NoError(t, err)
		var record struct {
			HostVersion string                  `json:"hostVersion"`
			Source      model.HostVersionSource `json:"hostVersionSource"`
			Outcome     string                  `json:"outcomeClass"`
		}
		lines := bytes.Split(bytes.TrimSpace(fault), []byte("\n"))
		require.Len(t, lines, 2, "each refused update must retain a fault record")
		require.NoError(t, json.Unmarshal(lines[updateIndex], &record))
		version, source := creationVersion, creationSource
		if !toolValid {
			version, source = toolVersion, model.HostVersionExecutableQuery
		}
		require.Equal(t, version, record.HostVersion)
		require.Equal(t, source, record.Source)
		require.Equal(t, "fault", record.Outcome)
	}
	require.NoError(t, tracker.Close())
	list := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "list", "--format", "json")
	var out, diagnostic bytes.Buffer
	list.Stdout, list.Stderr = &out, &diagnostic
	require.NoError(t, list.Run(), diagnostic.String())
	require.Empty(t, diagnostic.String())
	var public struct {
		Items []struct {
			Event       model.ContractEventKind  `json:"event"`
			Capture     model.CaptureDisposition `json:"capture"`
			Digest      string                   `json:"payloadDigest"`
			Interpreted []json.RawMessage        `json:"interpreted"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &public))
	require.Len(t, public.Items, 4)
	for _, item := range public.Items {
		raw, valid := toolRaw, toolValid
		if item.Event == registration.EventOpenCodeSessionCreated {
			raw, valid = sessionRaw, creationValid
		} else {
			require.Equal(t, registration.EventOpenCodeToolExecuteBefore, item.Event)
		}
		require.Equal(t, digest.FromBytes(raw).String(), item.Digest)
		if valid {
			require.Equal(t, model.CaptureValid, item.Capture)
			require.Len(t, item.Interpreted, 1)
		} else {
			require.Equal(t, model.CaptureUnsupportedSchema, item.Capture)
			require.Empty(t, item.Interpreted)
		}
	}
}

func TestEnabledOpenCodeHandlersToDurableReadBack(t *testing.T) {
	t.Parallel()

	// The v1 plugin shape that used to deliver these bytes no longer exists;
	// the proof drives the same authentic bytes straight into the built CLI.
	// What it proves is unchanged: the two enabled 1.18.29 rows evaluate
	// through the production binary and commit durable, provider-correct
	// evidence. The version coordinates mirror the retired plugin: the
	// creation occurrence carries the fixture's own info.version as the
	// caller-supplied observation, and the tool gate resolves its host
	// version from the stubbed executable.
	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	fixtureDir := filepath.Join(root, "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	sessionRaw, err := os.ReadFile(filepath.Join(fixtureDir, "session_created_1_18_29.json"))
	require.NoError(t, err)
	toolRaw, err := os.ReadFile(filepath.Join(fixtureDir, "tool_execute_before_1_18_29.json"))
	require.NoError(t, err)
	var sessionCapture struct {
		Event struct {
			Properties struct {
				Info struct {
					Version string `json:"version"`
				} `json:"info"`
			} `json:"properties"`
		} `json:"event"`
	}
	require.NoError(t, json.Unmarshal(sessionRaw, &sessionCapture))
	require.NotEmpty(t, sessionCapture.Event.Properties.Info.Version)
	var toolCapture struct {
		Input  json.RawMessage `json:"input"`
		Output struct {
			Args json.RawMessage `json:"args"`
		} `json:"output"`
	}
	require.NoError(t, json.Unmarshal(toolRaw, &toolCapture))
	toolPayload, err := json.Marshal(map[string]any{"input": toolCapture.Input, "output": map[string]any{"args": toolCapture.Output.Args}})
	require.NoError(t, err)
	// A controlled installed-executable observation, not a new host capture.
	path := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "opencode"), []byte("#!/bin/sh\nprintf '1.19.0\\n'\n"), 0o700))
	runLifecycleStdin := func(args []string, stdin []byte, extraEnv map[string]*string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		base := map[string]*string{"PASTURE_DB_PATH": &dbPath, "PATH": &path,
			"PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil}
		for key, value := range extraEnv {
			base[key] = value
		}
		command.Env = discoveryChildEnv(base)
		command.Stdin = bytes.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		require.NoError(t, command.Run(), stderr.String())
	}
	runLifecycleStdin([]string{"hook", "lifecycle", "--harness", "opencode", "--event", "session.created",
		"--host-version", sessionCapture.Event.Properties.Info.Version}, sessionRaw, nil)
	runLifecycleStdin([]string{"hook", "lifecycle", "--harness", "opencode", "--event", "tool.execute.before"}, toolPayload, nil)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	pageSize, err := model.NewPageSize(2)
	require.NoError(t, err)
	page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
	require.NoError(t, err)
	require.Len(t, page.Records(), 2)

	semantics := make(map[model.ContractEventKind]runtime.EventSemantic, 2)
	identities := make(map[model.ContractEventKind]map[runtime.NativeIdentityKind]string, 2)
	for _, record := range page.Records() {
		require.Equal(t, registration.OpenCode1_18_29().Contract, record.Occurrence.RuntimeContract)
		if record.Occurrence.Kind == registration.EventOpenCodeSessionCreated {
			require.Equal(t, registration.OpenCode1_18_29().Version, record.Occurrence.Envelope.HostVersion)
			require.Equal(t, model.HostVersionCallerSupplied, record.Occurrence.Envelope.HostVersionSource)
		} else {
			require.Equal(t, "1.19.0", record.Occurrence.Envelope.HostVersion)
			require.Equal(t, model.HostVersionExecutableQuery, record.Occurrence.Envelope.HostVersionSource)
		}
		require.Len(t, record.Interpreted(), 1)
		interpreted := record.Interpreted()[0]
		require.Equal(t, runtime.OpenCode1_18_29().ID(), interpreted.Contract())
		semantics[record.Occurrence.Kind] = interpreted.Semantic()
		identities[record.Occurrence.Kind] = make(map[runtime.NativeIdentityKind]string)
		for _, identity := range interpreted.Identities() {
			identities[record.Occurrence.Kind][identity.Kind] = identity.Value
		}
	}
	require.Equal(t, runtime.SemanticObservation, semantics[registration.EventOpenCodeSessionCreated])
	require.Equal(t, runtime.SemanticGateConsultation, semantics[registration.EventOpenCodeToolExecuteBefore])
	t.Run("session.created", func(t *testing.T) {
		require.Equal(t, runtime.SemanticObservation, semantics[registration.EventOpenCodeSessionCreated])
		require.Equal(t, "ses_f8e723e13ffeUWnfE2vlRBO1xN", identities[registration.EventOpenCodeSessionCreated][runtime.IdentitySession])
	})
	t.Run("tool.execute.before", func(t *testing.T) {
		require.Equal(t, runtime.SemanticGateConsultation, semantics[registration.EventOpenCodeToolExecuteBefore])
		require.Equal(t, "ses_f8e723e13ffeUWnfE2vlRBO1xN", identities[registration.EventOpenCodeToolExecuteBefore][runtime.IdentitySession])
		require.Equal(t, "call_4zMdLgUBV12aE7yHvuYQolSx", identities[registration.EventOpenCodeToolExecuteBefore][runtime.IdentityToolCall])
	})

	interpretedRows := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	consultationRows := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Len(t, interpretedRows, 2)
	require.Len(t, consultationRows, 1)
	consultation := consultationRows[0]
	var gateInterpreted provenance.EvidenceRow
	for _, row := range interpretedRows {
		if row.ProducingOperationID == consultation.ProducingOperationID {
			gateInterpreted = row
		}
	}
	require.NotZero(t, gateInterpreted.JournalID)
	require.Equal(t, gateInterpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID)
	require.Less(t, gateInterpreted.JournalID, consultation.JournalID, "one durable gate operation must order interpreted before consultation evidence")
	require.Contains(t, string(consultation.Payload), `"decision":{"decision":"proceed","reason":"unbound-session"}`,
		"a transport gate with no session-start claim records the unbound-session reason")

	// Claude is deliberately non-live regression evidence here. Compare only the
	// shared gate semantic, blocking mode, and canonical Proceed decision.
	openCodeGate, err := runtime.OpenCode1_18_29Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	claudeGate, err := runtime.ClaudeCode2_1_261Lifecycle().Mapping(runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	require.Equal(t, claudeGate.Semantic(), openCodeGate.Semantic())
	require.Equal(t, claudeGate.Blocking(), openCodeGate.Blocking())
	require.NotEqual(t, runtime.ClaudeCode2_1_261().ID(), runtime.OpenCode1_18_29().ID())
}

// openCode2EnabledFixtures pairs each enabled OpenCode 2.0.20-contract
// coordinate with the committed capture its activation row cites, in
// registration order. bus marks a coordinate the host delivers through the
// plugin's event subscription rather than a registered hook; hostVersion is
// the version the durable envelope must carry for that row.
var openCode2EnabledFixtures = []struct {
	event       model.ContractEventKind
	native      string
	fixture     string
	bus         bool
	hostVersion string
}{
	// The bus event carries its own release in data.version (2.0.21 in the
	// capture), which the transport forwards ahead of the setup version.
	{registration.EventOpenCode2SessionCreated, "session.created", "opencode_session_created_2_0_21.1.json", true, "2.0.21"},
	{registration.EventOpenCode2SessionPrompt, "session.prompt", "opencode_session_prompt_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2SessionContext, "session.context", "opencode_session_context_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2SessionTitle, "session.title", "opencode_session_title_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2SessionModelRequest, "session.model.request", "opencode_session_model_request_2_0_20.2.json", false, "2.0.20"},
	{registration.EventOpenCode2SessionHttpRequest, "session.http.request", "opencode_session_http_request_2_0_20.2.json", false, "2.0.20"},
	{registration.EventOpenCode2SessionHttpResponse, "session.http.response", "opencode_session_http_response_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2ToolExecuteBefore, "tool.execute.before", "opencode_tool_execute_before_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2ToolExecuteAfter, "tool.execute.after", "opencode_tool_execute_after_2_0_20.1.json", false, "2.0.20"},
	{registration.EventOpenCode2PermissionEvaluate, "permission.evaluate", "opencode_permission_evaluate_2_0_20.2.json", false, "2.0.20"},
}

// citedCapturePath returns the repository-relative path a capture citation
// names: the text before any trailing parenthetical annotation, trimmed.
func citedCapturePath(citation string) string {
	if index := strings.Index(citation, " ("); index >= 0 {
		citation = citation[:index]
	}
	return strings.TrimSpace(citation)
}

// TestEnabledOpenCode2HandlersToDurableReadBack is the production proof for
// every enabled OpenCode 2.0.20-contract coordinate. Bun loads the shipped
// generated transport (.opencode/plugins/pasture-lifecycle.ts), runs its setup
// against a host context that reports version 2.0.20, and calls each
// registered hook callback with the authentic committed capture bytes. A bus
// coordinate (session.created) is not a hook: the driver's ctx.event.subscribe
// stream yields its committed capture as a bus event, so it reaches the
// transport's own subscription filter and observation path, and the driver
// waits until the transport has pulled past it before cleanup. The transport spawns
// the built binary for real; nothing is stubbed between the callback and the
// durable store. Each coordinate must then read back as exactly one durable
// occurrence under the 2.0.20 contract, interpreted by the 2.0.20 runtime
// profile, with a host version the transport carried from setup.
func TestEnabledOpenCode2HandlersToDurableReadBack(t *testing.T) {
	t.Parallel()
	bun, err := exec.LookPath("bun")
	require.NoError(t, err, "Bun is required to drive the generated OpenCode 2.0.20 transport the way the host does; enter the flake dev shell or install the flake-locked Bun package")

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	fixtureDir := filepath.Join(root, "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	transport := filepath.Join(root, ".opencode", "plugins", "pasture-lifecycle.ts")
	_, err = os.Stat(transport)
	require.NoError(t, err, "the shipped OpenCode transport is missing; run make generate")

	// Bind this proof to the activation data it is cited by, before any call
	// is driven. The fixture table above is the independent expectation: a
	// capture citation that names another file, an enabled set that drifts,
	// or a table row that drives the wrong capture all fail here.
	entries, err := activation.OpenCode2_0_20()
	require.NoError(t, err)
	byEvent := make(map[model.ContractEventKind]activation.Entry, len(entries))
	var enabledKinds []model.ContractEventKind
	for _, entry := range entries {
		byEvent[entry.Event] = entry
		if entry.State == activation.Enabled {
			enabledKinds = append(enabledKinds, entry.Event)
		}
	}
	wantKinds := make([]model.ContractEventKind, 0, len(openCode2EnabledFixtures))
	for _, row := range openCode2EnabledFixtures {
		wantKinds = append(wantKinds, row.event)
	}
	require.Equal(t, wantKinds, enabledKinds, "the 2.0.20 enabled set must be exactly the ten coordinates this proof drives")
	for _, row := range openCode2EnabledFixtures {
		entry := byEvent[row.event]
		require.Equal(t, activation.Enabled, entry.State, "%s must be enabled", row.native)
		require.NotZero(t, entry.CaptureProof, "%s has no capture proof", row.native)
		require.NotZero(t, entry.ProductionProof, "%s has no production proof", row.native)
		require.Equal(t, filepath.Join(fixtureDir, row.fixture), filepath.Join(root, citedCapturePath(entry.CaptureProof.Name())),
			"%s: the activation capture citation %q does not name the fixture this proof drives", row.native, entry.CaptureProof.Name())
		require.Equal(t, "cmd/pasture/hook_lifecycle_production_test.go:"+t.Name()+"/"+row.native, entry.ProductionProof.Name(),
			"%s: the activation production citation does not name this proof and subtest", row.native)
	}

	var calls, busEvents strings.Builder
	for _, row := range openCode2EnabledFixtures {
		raw, err := os.ReadFile(filepath.Join(fixtureDir, row.fixture))
		require.NoError(t, err)
		require.True(t, json.Valid(raw), "fixture %s is not JSON", row.fixture)
		if row.bus {
			fmt.Fprintf(&busEvents, "%s,\n", raw)
			continue
		}
		fmt.Fprintf(&calls, "await invoke(%q, %s);\n", row.native, raw)
	}
	runner := filepath.Join(dir, "drive.ts")
	script := fmt.Sprintf(`
const {default: plugin} = await import(%q);
const hooks = {};
const busEvents = [
%s];
let busDrained;
const drained = new Promise((resolve) => { busDrained = resolve; });
const register = (prefix) => ({ hook: async (name, cb) => { hooks[prefix + "." + name] = cb; return { dispose: async () => {} }; } });
const ctx = {
  app: { name: "opencode", version: "2.0.20" },
  session: register("session"), tool: register("tool"), permission: register("permission"), shell: register("shell"),
  event: { subscribe: ({ signal } = {}) => (async function* () {
    for (const busEvent of busEvents) yield busEvent;
    // The transport asks for the next event only after it has handled the
    // last one, so reaching here means every bus event was observed.
    busDrained();
    if (signal && !signal.aborted) await new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
  })() },
};
const failures = [];
const originalError = console.error;
console.error = (...args) => { failures.push(args.join(" ")); };
async function invoke(name, payload) {
  const cb = hooks[name];
  if (typeof cb !== "function") throw new Error("the transport registered no callback for " + name);
  const before = failures.length;
  try { await cb(payload); } catch (error) { failures.push(name + " threw: " + error); }
  if (failures.length !== before) throw new Error(name + " did not proceed cleanly: " + failures.slice(before).join("; "));
}
try {
  const cleanup = await plugin.setup(ctx);
%s
  await drained;
  if (failures.length !== 0) throw new Error("bus observation did not proceed cleanly: " + failures.join("; "));
  await cleanup();
} finally { console.error = originalError; }
console.log("opencode 2.0.20 production drive passed");
`, transport, busEvents.String(), calls.String())
	require.NoError(t, os.WriteFile(runner, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, runner)
	command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath,
		"PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
	out, err := command.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "opencode 2.0.20 production drive passed")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	pageSize, err := model.NewPageSize(uint16(len(openCode2EnabledFixtures) + 1))
	require.NoError(t, err)
	page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
	require.NoError(t, err)
	require.Len(t, page.Records(), len(openCode2EnabledFixtures))

	wantVersion := make(map[model.ContractEventKind]string, len(openCode2EnabledFixtures))
	for _, row := range openCode2EnabledFixtures {
		wantVersion[row.event] = row.hostVersion
	}
	byKind := make(map[model.ContractEventKind]int, len(openCode2EnabledFixtures))
	for _, record := range page.Records() {
		byKind[record.Occurrence.Kind]++
		require.Equal(t, registration.OpenCode2_0_20().Contract, record.Occurrence.RuntimeContract)
		require.Equal(t, wantVersion[record.Occurrence.Kind], record.Occurrence.Envelope.HostVersion)
		require.Equal(t, model.HostVersionCallerSupplied, record.Occurrence.Envelope.HostVersionSource)
		require.Len(t, record.Interpreted(), 1)
		require.Equal(t, runtime.OpenCode2_0_20().ID(), record.Interpreted()[0].Contract())
	}
	for _, row := range openCode2EnabledFixtures {
		t.Run(row.native, func(t *testing.T) {
			require.Equal(t, 1, byKind[row.event], "coordinate %s must read back as exactly one durable occurrence", row.native)
		})
	}
}

// The two contract ids every durable record must carry, read from the
// registration manifest and the runtime contract so a moved host version
// moves every expectation in this file with it.
var (
	occurrenceLifecycleContract  = registration.ClaudeCode2_1_261().Contract.String()
	interpretedLifecycleContract = runtime.ClaudeCode2_1_261().ID().String()
	consultationEvidenceKind     = receipt.CurrentConsultationEvidenceKind()
)

const (
	occurrenceEvidenceKind        = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")
	interpretedEvidenceKind       = provenance.EvidenceKind("pasture.lifecycle.interpreted.v2")
	expectedSessionIdentity       = "c02859c0-10ab-49c3-9b93-29280bd45fbb"
	expectedInterpretedIdentities = `[{"kind":1,"value":"c02859c0-10ab-49c3-9b93-29280bd45fbb"}]`
)

var expectedEnabledClaudeEvents = []model.ContractEventKind{
	registration.EventSessionStart,
	registration.EventSessionEnd,
	registration.EventPreToolUse,
	registration.EventPostToolUse,
	registration.EventPostToolUseFailure,
	registration.EventPostToolBatch,
	registration.EventFileChanged,
	registration.EventPreCompact,
	registration.EventPostCompact,
}

type claudeProductionFixture struct {
	captureVersion string
	captureSource  string
	name           string
	fixture        string
	event          model.ContractEventKind
	bindings       []lifecycleBindingPayload
	semantic       runtime.EventSemantic
	identities     []interpretedIdentityPayload
	unresolved     []interpretedUnresolvedPayload
	blocking       bool
}

var claudeProductionFixtures = []claudeProductionFixture{
	{
		name: "SessionStart", fixture: "session_start_2_1_261.json", event: registration.EventSessionStart,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
	},
	{
		name: "SessionEnd", fixture: "session_end_2_1_261.json", event: registration.EventSessionEnd,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
	},
	{
		name: "PreToolUse", fixture: "pre_tool_use_2_1_261.json", event: registration.EventPreToolUse,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01DECpiEtdNZxsYNXCTg5tb1"},
		},
		semantic: runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01DECpiEtdNZxsYNXCTg5tb1"},
		},
		blocking: true,
	},
	{
		name: "PostToolUse", fixture: "post_tool_use_2_1_261.json", event: registration.EventPostToolUse,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01DECpiEtdNZxsYNXCTg5tb1"},
		},
		semantic: runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01DECpiEtdNZxsYNXCTg5tb1"},
		},
	},
	{
		name: "PostToolUseFailure", fixture: "post_tool_use_failure_2_1_261.json", event: registration.EventPostToolUseFailure,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings: []lifecycleBindingPayload{
			{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: "toolu_01JnozEijp6Ly4oYGZ478HGD"},
		},
		semantic: runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{
			{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"},
			{Kind: uint8(runtime.IdentityToolCall), Value: "toolu_01JnozEijp6Ly4oYGZ478HGD"},
		},
	},
	{
		name: "PostToolBatch", fixture: "post_tool_batch_2_1_261.json", event: registration.EventPostToolBatch,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		semantic:   runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		unresolved: []interpretedUnresolvedPayload{{Reason: uint8(waist.UnresolvedToolCall)}},
		blocking:   true,
	},
	{
		name: "PreCompact", fixture: "pre_compact_2_1_261.json", event: registration.EventPreCompact,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		semantic:   runtime.SemanticGateConsultation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		blocking:   true,
	},
	{
		name: "PostCompact", fixture: "post_compact_2_1_261.json", event: registration.EventPostCompact,
		captureVersion: "2.1.261", captureSource: "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "c02859c0-10ab-49c3-9b93-29280bd45fbb"}},
	},
	{
		name: "FileChanged", fixture: "file_changed_2_1_263.json", event: registration.EventFileChanged,
		captureVersion: "2.1.263", captureSource: "cmd/pasture/hook_lifecycle.go:406 -> internal/handlers/capture_sink.go:DirectoryCaptureSink.Record (PASTURE_CAPTURE_DIR)",
		bindings:   []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "d4c368f4-ca7b-41ff-b9b1-9b3489c34f2e"}},
		semantic:   runtime.SemanticObservation,
		identities: []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "d4c368f4-ca7b-41ff-b9b1-9b3489c34f2e"}},
	},
}

func TestEnabledClaudeEventToOccurrenceAndInterpretedEvidence(t *testing.T) {
	t.Parallel()

	manifest, err := activation.ClaudeCode2_1_261()
	require.NoError(t, err)
	enabled := make([]model.ContractEventKind, 0, len(expectedEnabledClaudeEvents))
	for _, entry := range manifest {
		if entry.State == activation.Enabled {
			enabled = append(enabled, entry.Event)
		}
	}
	require.Equal(t, expectedEnabledClaudeEvents, enabled, "literal production rows must equal the complete static enabled set")

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())

	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()

	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1)
	occurrence := occurrences[0]
	occurrencePayload := assertOccurrencePayload(t, occurrence.Payload, raw, model.CaptureValid, registration.EventSessionStart)

	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Len(t, interpreted, 1)
	assertInterpretedEvidence(t, interpreted[0].Payload)
	assertSharedOperation(t, occurrence, interpreted[0])

	require.Equal(t, registration.EventSessionStart, occurrencePayload.Event)
	require.Equal(t, "2.1.261", occurrencePayload.Envelope.HostVersion)
	// The occurrence-side binding is retained independently from the waist
	// identity assertion above; it is not a substitute for interpreted evidence.
	require.Len(t, occurrencePayload.Bindings, 1)
	require.Equal(t, model.BindingSession, occurrencePayload.Bindings[0].Kind)
	require.Equal(t, "session_id", occurrencePayload.Bindings[0].NativeName)
	require.Equal(t, expectedSessionIdentity, occurrencePayload.Bindings[0].Value)
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())
	require.Contains(t, stdout.String(), `"registrationContract":"`+occurrenceLifecycleContract+`"`)
	require.Contains(t, stdout.String(), `"contract":"`+interpretedLifecycleContract+`"`)
	require.Contains(t, stdout.String(), `"interpreted":[`)
	require.NotContains(t, stdout.String(), string(raw))
	require.NotContains(t, stdout.String(), dbPath)

	ingestAgain := func(payload []byte) {
		cmd := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261")
		cmd.Stdin = bytes.NewReader(payload)
		require.NoError(t, cmd.Run())
	}
	ingestAgain(raw)
	ingestAgain([]byte(`{"session_id":`))
	ingestAgain(raw)
	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	duplicateOccurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	duplicateInterpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	require.Len(t, duplicateOccurrences, 4)
	require.Len(t, duplicateInterpreted, 3)
	operationIDs := make(map[string]struct{}, len(duplicateOccurrences))
	journalIDs := make(map[provenance.JournalID]struct{}, len(duplicateOccurrences))
	occurrenceByOperation := make(map[string]provenance.EvidenceRow, len(duplicateOccurrences))
	for _, row := range duplicateOccurrences {
		operationID := string(row.ProducingOperationID)
		operationIDs[operationID] = struct{}{}
		journalIDs[row.JournalID] = struct{}{}
		occurrenceByOperation[operationID] = row
	}
	require.Len(t, operationIDs, 4, "byte-identical deliveries need distinct operation identities")
	require.Len(t, journalIDs, 4, "byte-identical deliveries need distinct occurrence records")
	interpretedOperations := make(map[string]struct{}, len(duplicateInterpreted))
	for _, interpretedRow := range duplicateInterpreted {
		operationID := string(interpretedRow.ProducingOperationID)
		occurrenceRow, found := occurrenceByOperation[operationID]
		require.True(t, found, "every interpreted row must share a producing operation with its occurrence")
		require.Equal(t, occurrenceRow.ProducingOperationJournalID, interpretedRow.ProducingOperationJournalID, "occurrence and interpreted evidence must share one operation journal identity")
		interpretedOperations[operationID] = struct{}{}
	}
	require.Len(t, interpretedOperations, 3, "each valid delivery needs one interpreted operation while the malformed occurrence remains occurrence-only")
	require.NoError(t, tracker.Close())
	pageOne := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "1", "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	pageOne.Stdout = &stdout
	pageOne.Stderr = &stderr
	require.NoError(t, pageOne.Run(), stderr.String())
	var firstPage struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &firstPage))
	require.Len(t, firstPage.Items, 1)
	require.NotEmpty(t, firstPage.Next)
	ingestAgain(raw)
	continuation := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--page-size", "10", "--cursor", firstPage.Next, "--binding", "session:session_id="+expectedSessionIdentity, "--binding", "session:session_id="+expectedSessionIdentity)
	stdout.Reset()
	stderr.Reset()
	continuation.Stdout = &stdout
	continuation.Stderr = &stderr
	require.NoError(t, continuation.Run(), stderr.String())
	var rest struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rest))
	require.Len(t, rest.Items, 2, "continuation must exclude malformed nonmatch and post-snapshot append")
	require.Empty(t, rest.Next)
	changed := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--cursor", firstPage.Next, "--binding", "session:session_id=changed")
	err = changed.Run()
	require.Error(t, err)
	require.Equal(t, 1, changed.ProcessState.ExitCode())
}

func TestEvaluatedClaudeProceedHasEmptyStdoutAndTypedConsultationV2(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readProductionClaudeFixture(t, "pre_tool_use_2_1_261.json", "PreToolUse")
	// PRESERVE. This subject is the durable policy reason of an EVALUATED
	// Proceed, so it must keep evaluating: seed the claim and the owning
	// assignment, and the reason stays "legal" from a real evaluation rather
	// than the unbound-session default a transport subject carries.
	seedBoundActorOwningAMappedTask(t, dbPath, ir.HarnessClaudeCode, raw)
	command := exec.Command(
		binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "claude-code", "--event", "PreToolUse", "--host-version", "2.1.261",
	)
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	require.NoError(t, err, stderr.String())
	require.Empty(t, stdout.Bytes(), "an evaluated Claude Proceed is exit 0 with EMPTY stdout, not a non-enum decision object")
	require.Empty(t, stderr.String())

	// Empty stdout must follow a real consultation with an explicit reason.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	consultations := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Len(t, consultations, 1, "empty stdout must still follow a real committed consultation")
	require.Equal(t, "pasture.lifecycle.consultation.v2", string(consultationEvidenceKind))
	members := decodeJSONObject(t, consultations[0].Payload)
	require.JSONEq(t, `{"decision":"proceed","reason":"legal"}`, string(members["decision"]), "durable policy reason is independent of the empty host response")
}

func TestEnabledClaudeAuthenticFixturesToDurableEvidence(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)

	for _, testCase := range claudeProductionFixtures {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			for _, hintState := range []string{"absent", "empty"} {
				t.Run(hintState, func(t *testing.T) {
					dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
					initializeLifecycleTestDatabase(t, dbPath)
					raw := readProductionClaudeFixtureAt(t, testCase.fixture, testCase.name, testCase.captureVersion, testCase.captureSource)

					path, _ := discoveryPathExecutable(t, "printf '"+testCase.captureVersion+" (Claude Code)\\n'")
					command := exec.Command(shell, "-c", generatedClaudeLifecycleCommand(t, testCase.name))
					var hint *string
					if hintState == "empty" {
						hint = discoveryValue("")
					}
					command.Env = discoveryChildEnv(map[string]*string{"PASTURE_BIN": &binary, "PASTURE_DB_PATH": &dbPath,
						"PATH": &path, "CLAUDE_CODE_EXECPATH": hint, "PASTURE_CAPTURE_DIR": nil, "PASTURE_ACTOR_ID": nil, "PASTURE_HOOK_FAIL_CLOSED": nil})
					command.Stdin = bytes.NewReader(raw)
					var stdout, stderr bytes.Buffer
					command.Stdout = &stdout
					command.Stderr = &stderr
					require.NoError(t, command.Run(), stdout.String()+stderr.String())
					require.Empty(t, stderr.String())
					require.Empty(t, stdout.String(), "an evaluated Claude Proceed or observation emits no hook directive")

					tracker, err := tasks.OpenTaskTracker(dbPath)
					require.NoError(t, err)
					occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
					interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
					consultations := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
					require.Len(t, occurrences, 1)
					require.Len(t, interpreted, 1)
					if testCase.blocking {
						require.Len(t, consultations, 1)
					} else {
						require.Empty(t, consultations)
					}

					occurrencePayload := decodeOccurrencePayload(t, occurrences[0].Payload)
					require.Equal(t, occurrenceLifecycleContract, occurrencePayload.Contract)
					require.Equal(t, testCase.event, occurrencePayload.Event)
					require.Equal(t, occurrenceLifecycleContract, occurrencePayload.Envelope.Runtime.Contract.String())
					require.Equal(t, testCase.captureVersion, occurrencePayload.Envelope.HostVersion)
					require.Equal(t, model.HostVersionExecutableQuery, occurrencePayload.Envelope.HostVersionSource)
					require.Equal(t, model.CaptureValid, occurrencePayload.Capture)
					require.Equal(t, digest.FromBytes(raw).String(), occurrencePayload.Body)
					require.Equal(t, testCase.bindings, occurrencePayload.Bindings)
					reader, err := tasks.NewLifecycleReader(tracker)
					require.NoError(t, err)
					body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
					require.NoError(t, err)
					require.Equal(t, raw, body, "durable payload bytes must match the accepted capture")

					interpretedPayload := decodeInterpretedPayload(t, interpreted[0].Payload)
					require.Equal(t, uint8(testCase.semantic), interpretedPayload.Semantic)
					require.Equal(t, testCase.identities, interpretedPayload.Identities)
					require.ElementsMatch(t, testCase.unresolved, interpretedPayload.UnresolvedFacts)
					require.Equal(t, interpretedLifecycleContract, interpretedPayload.Contract)
					assertSharedOperation(t, occurrences[0], interpreted[0])
					require.Less(t, occurrences[0].JournalID, interpreted[0].JournalID)
					if testCase.blocking {
						require.Equal(t, interpreted[0].ProducingOperationID, consultations[0].ProducingOperationID)
						require.Equal(t, interpreted[0].ProducingOperationJournalID, consultations[0].ProducingOperationJournalID)
						require.Less(t, interpreted[0].JournalID, consultations[0].JournalID)
						assertUnboundProceedConsultation(t, consultations[0].Payload)
					}
					require.NoError(t, tracker.Close())

					binding := testCase.bindings[0]
					list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json", "--binding", binding.Kind.String()+":"+binding.NativeName+"="+binding.Value)
					stdout.Reset()
					stderr.Reset()
					list.Stdout = &stdout
					list.Stderr = &stderr
					require.NoError(t, list.Run(), stdout.String()+stderr.String())
					require.Empty(t, stderr.String())
					var page struct {
						Items []struct {
							Event                model.ContractEventKind  `json:"event"`
							RegistrationContract string                   `json:"registrationContract"`
							Capture              model.CaptureDisposition `json:"capture"`
							PayloadDigest        string                   `json:"payloadDigest"`
							Interpreted          []struct {
								Semantic   runtime.EventSemantic    `json:"semantic"`
								Identities []waist.SemanticIdentity `json:"identities"`
								Unresolved []waist.UnresolvedFact   `json:"unresolved"`
								Contract   string                   `json:"contract"`
							} `json:"interpreted"`
						} `json:"items"`
					}
					require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
					require.Len(t, page.Items, 1)
					require.Equal(t, testCase.event, page.Items[0].Event)
					require.Equal(t, occurrenceLifecycleContract, page.Items[0].RegistrationContract)
					require.Equal(t, model.CaptureValid, page.Items[0].Capture)
					require.Equal(t, digest.FromBytes(raw).String(), page.Items[0].PayloadDigest)
					require.Len(t, page.Items[0].Interpreted, 1)
					require.Equal(t, testCase.semantic, page.Items[0].Interpreted[0].Semantic)
					require.Equal(t, testCase.identities, publicIdentityPayloads(page.Items[0].Interpreted[0].Identities))
					require.ElementsMatch(t, testCase.unresolved, publicUnresolvedPayloads(page.Items[0].Interpreted[0].Unresolved))
					require.Equal(t, interpretedLifecycleContract, page.Items[0].Interpreted[0].Contract)
					for _, privateValue := range []string{string(raw), dbPath, "/home/user", "authentic-capture", "tools/capture-claude-hook.sh", "home-path-v1"} {
						require.NotContains(t, stdout.String(), privateValue)
					}
				})
			}
		})
	}
}

func TestFileChangedGeneratedCommandCompatibleObservationAndIdentityRefusal(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	generated := generatedClaudeLifecycleCommand(t, "FileChanged")
	authentic := readProductionClaudeFixtureAt(t, "file_changed_2_1_263.json", "FileChanged", "2.1.263",
		"cmd/pasture/hook_lifecycle.go:406 -> internal/handlers/capture_sink.go:DirectoryCaptureSink.Record (PASTURE_CAPTURE_DIR)")
	for _, control := range []string{"compatible newer executable", "missing session", "non-string session"} {
		t.Run(control, func(t *testing.T) {
			raw := authentic
			valid := control == "compatible newer executable"
			if !valid {
				var members map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(authentic, &members))
				if control == "missing session" {
					delete(members, "session_id")
				} else {
					members["session_id"] = json.RawMessage(`42`)
				}
				var err error
				raw, err = json.Marshal(members)
				require.NoError(t, err)
				require.NotEqual(t, authentic, raw, "the negative control must change required identity")
			}
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			executable := versionExecutable(t, "printf '2.1.299 (Claude Code)\\n'")
			command := exec.Command("sh", "-c", generated)
			command.Env = append(os.Environ(), "PASTURE_BIN="+binary, "PASTURE_DB_PATH="+dbPath,
				"CLAUDE_CODE_EXECPATH="+executable, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=", "PASTURE_HOOK_FAIL_CLOSED=")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stdout.Bytes())
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, occurrences, 1)
			occurrence := decodeOccurrencePayload(t, occurrences[0].Payload)
			require.Equal(t, registration.EventFileChanged, occurrence.Event)
			require.Equal(t, occurrenceLifecycleContract, occurrence.Contract)
			require.Equal(t, "2.1.299", occurrence.Envelope.HostVersion)
			require.Equal(t, model.HostVersionExecutableQuery, occurrence.Envelope.HostVersionSource)
			require.Equal(t, digest.FromBytes(raw).String(), occurrence.Body)
			require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind), "FileChanged never consults policy")
			interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
			if valid {
				require.Empty(t, stderr.Bytes())
				require.Equal(t, model.CaptureValid, occurrence.Capture)
				require.Equal(t, []lifecycleBindingPayload{{Kind: model.BindingSession, NativeName: "session_id", Value: "d4c368f4-ca7b-41ff-b9b1-9b3489c34f2e"}}, occurrence.Bindings)
				require.Len(t, interpreted, 1)
				payload := decodeInterpretedPayload(t, interpreted[0].Payload)
				require.Equal(t, interpretedLifecycleContract, payload.Contract)
				require.Equal(t, uint8(runtime.SemanticObservation), payload.Semantic)
				require.Equal(t, []interpretedIdentityPayload{{Kind: uint8(runtime.IdentitySession), Value: "d4c368f4-ca7b-41ff-b9b1-9b3489c34f2e"}}, payload.Identities)
				require.Empty(t, payload.UnresolvedFacts)
				assertSharedOperation(t, occurrences[0], interpreted[0])
			} else {
				require.NotEmpty(t, stderr.Bytes(), "required identity refusal must be diagnosed, not silently treated as an observation")
				require.Equal(t, model.CaptureUnsupportedSchema, occurrence.Capture)
				require.Empty(t, occurrence.Bindings)
				require.Empty(t, interpreted)
			}
			require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
			reader, err := tasks.NewLifecycleReader(tracker)
			require.NoError(t, err)
			body, err := reader.Payload(context.Background(), digest.FromBytes(raw))
			require.NoError(t, err)
			require.Equal(t, raw, body)
			size, err := model.NewPageSize(1)
			require.NoError(t, err)
			page, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: size}})
			require.NoError(t, err)
			require.Len(t, page.Records(), 1)
			require.Equal(t, occurrence.Envelope, page.Records()[0].Occurrence.Envelope)
		})
	}
}

func TestClaudePayloadEventCannotOverrideRegisteredCLIEvent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	authentic := readProductionClaudeFixture(t, "session_start_2_1_261.json", "SessionStart")
	raw := bytes.Replace(authentic, []byte(`"hook_event_name":"SessionStart"`), []byte(`"hook_event_name":"SessionEnd"`), 1)
	require.NotEqual(t, authentic, raw, "the negative control must change only the payload's event claim")

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stdout.String())
	// THE OLD REQUIREMENT HERE WAS require.Empty ON STDERR, and it pinned the
	// same silence as the malformed case above. MEASURED BEFORE CHANGING IT,
	// because this input is not obviously the same case: a payload whose event
	// claim is overridden COULD have been a genuinely evaluated event, and
	// making an evaluated event announce that it was not would have been a new
	// false sentence. It is not evaluated — ingress returns no bindings on an
	// event mismatch, and the assertions below require an empty interpreted set
	// and no consultation. The delivery is refused, not reinterpreted.
	//
	// stdout stays empty above because Claude's continuation IS the empty body.
	require.Contains(t, stderr.String(), "could not be bound, so the event WAS NOT EVALUATED",
		"a payload that declares a different event is REFUSED, not reinterpreted, so the event was "+
			"not evaluated and the operator must be told rather than left with silence")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1)
	payload := decodeOccurrencePayload(t, occurrences[0].Payload)
	require.Equal(t, registration.EventSessionStart, payload.Event, "the generated CLI coordinate remains authoritative")
	require.Equal(t, model.CaptureEventMismatch, payload.Capture)
	require.Empty(t, payload.Bindings)
	require.Equal(t, digest.FromBytes(raw).String(), payload.Body)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())
	require.Contains(t, stdout.String(), `"capture":8`)
	require.Contains(t, stdout.String(), `"interpreted":[]`)
	require.NotContains(t, stdout.String(), string(raw))
}

func TestWithheldClaudeElicitationIsNotAdmittedByBuiltCLI(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)
	// No Elicitation or ElicitationResult payload was captured at this host
	// version (the pair needs an MCP elicitation the capture session did not
	// drive), so the bytes below are a stand-in shaped like the host's payload.
	// The refusal under test happens before standard input is read, so the
	// payload's content cannot reach any assertion here.
	cases := []struct{ event string }{
		{event: "Elicitation"},
		{event: "ElicitationResult"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
			raw := []byte(`{"session_id":"stand-in","hook_event_name":"` + testCase.event + `","mcp_server_name":"stand-in","mode":"form"}`)
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", testCase.event, "--host-version", "2.1.261")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stdout.String()+stderr.String())
			require.Empty(t, stdout.String(), "withheld elicitation must emit no host response")
			require.Contains(t, stderr.String(), `Claude event "`+testCase.event+`" is withheld (reason missing-request-correlation)`)
			require.NotContains(t, stderr.String(), "capture-approved")
			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "withheld elicitation must create no durable or public authority")
		})
	}
}

func TestMalformedClaudeEventToOccurrenceOnly(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)
	raw := []byte(`{"session_id":`)
	for _, testCase := range []struct {
		name  string
		event model.ContractEventKind
	}{
		{name: "SessionStart", event: registration.EventSessionStart},
		{name: "PreToolUse", event: registration.EventPreToolUse},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", testCase.name, "--host-version", "2.1.261")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stdout.String()+stderr.String())
			require.Empty(t, stdout.String(), "malformed lifecycle input must never emit a host decision")
			// THE OLD REQUIREMENT HERE WAS require.Empty ON STDERR, AND IT
			// PINNED THE DEFECT RATHER THAN A CONTRACT. It required an event
			// that pasture could not read to say NOTHING, and that silence was
			// the whole bug: the handler returned a nil error, which is how it
			// says "evaluated", so the command took its success path and
			// answered the host with a decision for an event nobody evaluated.
			// This is not the requirement relaxed; it is the requirement
			// inverted, because it was the wrong way round.
			//
			// Claude's continuation IS the empty body, so the stdout check
			// above is unchanged and still fires. On this harness the
			// diagnostic is the whole of what an operator has, and there was
			// none. Every occurrence, interpreted and consultation assertion
			// below is untouched: the delivery row is still the durable
			// evidence, and it is still the only durable effect.
			require.Contains(t, stderr.String(), "could not be bound, so the event WAS NOT EVALUATED",
				"a payload pasture cannot read is an event that was NOT evaluated, and it must say so; "+
					"staying silent is what let an unevaluated event leave as a decision")

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, occurrences, 1)
			occurrencePayload := assertOccurrencePayload(t, occurrences[0].Payload, raw, model.CaptureMalformed, testCase.event)
			require.Empty(t, occurrencePayload.Bindings)
			require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
			require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
			require.NoError(t, tracker.Close())

			list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
			var output bytes.Buffer
			list.Stdout = &output
			require.NoError(t, list.Run())
			require.Contains(t, output.String(), `"interpreted":[]`)
		})
	}
}

func TestLifecycleLeafFaultsExitZeroAndReport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	base := []string{databaseFlagName.Argument(), filepath.Join(dir, tasks.DefaultDBFilename.String()), "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220"}
	databaseDirectory := filepath.Join(dir, "database-directory")
	require.NoError(t, os.Mkdir(databaseDirectory, 0o700))
	cases := []struct {
		name string
		args []string
		in   []byte
		want string
	}{
		{name: "unwritable database", args: append([]string{databaseFlagName.Argument(), databaseDirectory}, base[2:]...), in: fixture, want: "open"},
		{name: "missing flag", args: []string{databaseFlagName.Argument(), filepath.Join(dir, "missing.db"), "hook", "lifecycle", "--harness", "claude-code", "--host-version", "2.1.220"}, in: fixture, want: `declares no native event named ""`},
		{name: "unknown flag", args: append(append([]string(nil), base...), "--unknown-lifecycle-flag"), in: fixture, want: "flag error"},
		{name: "extra positional", args: append(append([]string(nil), base...), "unexpected"), in: fixture, want: "unexpected positional arguments"},
		{name: "oversized payload", args: base, in: []byte(strings.Repeat("x", model.MaxNativePayloadBytes+1)), want: "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(binary, tc.args...)
			command.Stdin = bytes.NewReader(tc.in)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Contains(t, stderr.String(), tc.want)
		})
	}

	list := exec.Command(binary, databaseFlagName.Argument(), filepath.Join(dir, "list.db"), "hook", "lifecycle", "list", "--unknown-list-flag")
	err = list.Run()
	require.Error(t, err)
	require.Equal(t, 1, list.ProcessState.ExitCode(), "human list command must retain standard non-zero flag errors")
}

func TestInvalidLifecycleInvocationCreatesNoDatabase(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "missing", tasks.DefaultDBFilename.String())
	err := handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{DBPath: dbPath, Harness: "claude-code", Event: "Unknown", HostVersion: "2.1.220", Input: bytes.NewBufferString("{}"), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{}})
	require.Error(t, err)
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWithheldOpenCodeEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "opencode", "--event", "session.updated", "--host-version", registration.OpenCode1_18_29().Version)
	command.Stdin = bytes.NewBufferString(`{"event":{"type":"session.updated"}}`)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stderr.String())
	// A withheld event is a FAULT, and the fail-open default must not stop the
	// host. On OpenCode HOW a host is let through depends on the SURFACE of the
	// event, not on the harness alone:
	//
	//   - A GATE reaches the plugin's NAMED-OUTPUT callback, which validates
	//     exactly the canonical response object, so a gate fault emits those
	//     bytes. That arm is pinned elsewhere in this command's tests.
	//   - "session.updated" is an OBSERVATION on the catch-all event stream.
	//     Nothing on that surface reads standard output at all, so there is no
	//     reader to satisfy and no callback to abort. Writing a decision word
	//     there would tell the host MORE after a failure than after a success,
	//     because a SUCCESSFUL observation writes nothing.
	//
	// So the fault writes nothing, which is exactly what the success path
	// writes. This is safe on both sides of the plugin version skew: an older
	// plugin ignores observation output, and a newer one treats an empty body
	// at exit 0 as "not evaluated, continue" and says so on standard error.
	require.Empty(t, stdout.String(),
		"a withheld OpenCode observation must say no more after a failure than after a success, "+
			"and its surface has no reader of standard output to satisfy")
	require.Contains(t, stderr.String(), `OpenCode event "session.updated" is withheld (reason outside-target-set)`)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind), "withheld direct CLI ingress must persist no occurrence evidence")
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stderr.String())
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
	require.Empty(t, page.Items, "withheld direct CLI ingress must persist no public lifecycle record")
}

func TestLifecycleListRejectsCursorBeforeDatabaseOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, "missing", tasks.DefaultDBFilename.String())
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--cursor", "not-base64!")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	require.Error(t, err)
	require.Equal(t, 1, command.ProcessState.ExitCode())
	require.Contains(t, stderr.String(), "invalid cursor before database open")
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLifecycleListStandardExitCategories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	connectionPath := filepath.Join(dir, "database-directory")
	storagePath := filepath.Join(dir, "future.db")
	cases := []struct {
		name, path string
		prepare    func()
		want       int
	}{{"connection", connectionPath, func() { require.NoError(t, os.Mkdir(connectionPath, 0o700)) }, 2}, {"storage", storagePath, func() {
		initializeLifecycleTestDatabase(t, storagePath)
		db, err := sql.Open("sqlite", storagePath)
		require.NoError(t, err)
		// A schema version newer than this build knows, derived from the
		// constant. The audit schema ceiling moves with every migration, so a
		// literal that encodes "newer than known" stops being newer once the
		// ceiling passes it, and the case would then prove nothing; the
		// derivation keeps it one above the ceiling without an edit.
		_, err = db.Exec(fmt.Sprintf(`DELETE FROM audit_schema_meta; INSERT INTO audit_schema_meta(version,applied_at) VALUES(%d,1)`, audit.MaxKnownSchemaVersion+1))
		require.NoError(t, err)
		require.NoError(t, db.Close())
	}, 5}}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.prepare()
			command := exec.Command(binary, databaseFlagName.Argument(), tc.path, "hook", "lifecycle", "list")
			err := command.Run()
			require.Error(t, err)
			require.Equal(t, tc.want, command.ProcessState.ExitCode())
		})
	}
}

func TestLifecycleProjectionRebuildOccurrenceAndBindingsAreAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	ingest = exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_lifecycle_binding BEFORE INSERT ON lifecycle_occurrence_bindings BEGIN SELECT RAISE(ABORT,'binding fault'); END`)
	require.NoError(t, err)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	err = tasks.RebuildLifecycleOccurrences(context.Background(), tracker)
	require.Error(t, err)
	require.NoError(t, tracker.Close())
	var occurrences, bindings int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.Zero(t, occurrences)
	require.Zero(t, bindings)
	_, err = db.Exec(`DROP TRIGGER reject_lifecycle_binding`)
	require.NoError(t, err)
	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	ref := digest.FromBytes(raw)
	body, err := reader.Payload(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, raw, body)
	body[0] ^= 0xff
	again, err := reader.Payload(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, raw, again)
	_, err = reader.Payload(context.Background(), digest.FromString("missing"))
	require.Error(t, err)
	_, err = db.Exec(`PRAGMA ignore_check_constraints=ON; UPDATE lifecycle_payload_blobs SET byte_count=byte_count+1 WHERE digest=?`, ref.String())
	require.NoError(t, err)
	_, err = reader.Payload(context.Background(), ref)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE lifecycle_payload_blobs SET byte_count=?,body=zeroblob(?) WHERE digest=?`, len(raw), len(raw), ref.String())
	require.NoError(t, err)
	_, err = reader.Payload(context.Background(), ref)
	require.Error(t, err)
	require.NoError(t, tracker.Close())
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.Equal(t, 2, occurrences)
	require.Equal(t, 2, bindings)
}

type lifecycleOccurrencePayload struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []lifecycleBindingPayload   `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

type lifecycleBindingPayload struct {
	Kind       model.NativeBindingKind `json:"Kind"`
	NativeName string                  `json:"NativeName"`
	Value      string                  `json:"Value"`
}

type interpretedIdentityPayload struct {
	Kind  uint8  `json:"kind"`
	Value string `json:"value"`
}

type interpretedUnresolvedPayload struct {
	Reason uint8 `json:"reason"`
}

type interpretedEvidencePayload struct {
	Semantic        uint8                          `json:"semantic"`
	Identities      []interpretedIdentityPayload   `json:"identities"`
	UnresolvedFacts []interpretedUnresolvedPayload `json:"unresolved_facts"`
	Contract        string                         `json:"contract"`
	Metamodel       interpretedMetamodelPayload    `json:"manifest"`
}

type interpretedMetamodelPayload struct {
	ID      string `json:"id"`
	Version uint32 `json:"version"`
	Content string `json:"content"`
}

// childInstrumentation says whether a shared child build carries the race
// detector. It is an enum and not a bool so a call site reads as what it asks
// for.
type childInstrumentation int

const (
	// plainChild is an ordinary `go build`: the binary an operator installs.
	plainChild childInstrumentation = iota
	// raceChild is `go build -race`, for the one proof that needs a
	// race-instrumented separate process.
	raceChild
)

// buildArguments returns the `go` arguments that build this command into
// output with this instrumentation.
func (instrumentation childInstrumentation) buildArguments(output string) []string {
	arguments := []string{"build"}
	if instrumentation == raceChild {
		arguments = append(arguments, "-race")
	}
	return append(arguments, "-o", output, ".")
}

// sharedChildBinary is one build of this command that every test in the
// process shares. The build runs once, on first request, and the directory
// that holds it is registered with testutil so TestMain removes it after the
// last test has run; no single test may own it, because it outlives them all.
type sharedChildBinary struct {
	instrumentation childInstrumentation
	name            string

	once sync.Once
	path string
	err  error
}

// resolve returns the path of the built binary, building it on the first call.
// A failed build fails every caller with the same diagnostic, not just the
// first one.
func (binary *sharedChildBinary) resolve(t *testing.T) string {
	t.Helper()
	binary.once.Do(binary.build)
	require.NoError(t, binary.err)
	return binary.path
}

func (binary *sharedChildBinary) build() {
	dir, err := os.MkdirTemp("", "pasture-"+binary.name+"-*")
	if err != nil {
		binary.err = fmt.Errorf("could not create a directory for the shared %s binary before the first "+
			"built-binary test ran (cmd/pasture, sharedChildBinary.build): %w; every test that runs the "+
			"built CLI will fail until the temporary directory can be created", binary.name, err)
		return
	}
	testutil.RegisterFixtureDir(dir)
	binary.path = filepath.Join(dir, "pasture")

	build := exec.Command("go", binary.instrumentation.buildArguments(binary.path)...)
	build.Dir = "."
	if binary.instrumentation == raceChild {
		// The repository's standard test target keeps the outer suite CGO-free.
		// The race detector requires CGO, so this build turns it on for itself.
		build.Env = append(build.Environ(), "CGO_ENABLED=1")
	}
	output, err := build.CombinedOutput()
	if err != nil {
		binary.err = fmt.Errorf("could not build the shared %s binary with `go %s` before the first "+
			"built-binary test ran (cmd/pasture, sharedChildBinary.build): %w; every test that runs the "+
			"built CLI will fail until the command compiles again. Build output:\n%s",
			binary.name, strings.Join(binary.instrumentation.buildArguments(binary.path), " "), err, output)
	}
}

var (
	plainLifecycleChild = &sharedChildBinary{instrumentation: plainChild, name: "lifecycle-child"}
	raceLifecycleChild  = &sharedChildBinary{instrumentation: raceChild, name: "lifecycle-race-child"}
)

// lifecycleBinary returns the ONE plain build of this command that every
// built-binary proof in this package runs. The first caller builds it; every
// later caller receives the same path. Each test keeps its own store in its
// own t.TempDir, so the binary is the only thing the tests share, and no test
// writes to or deletes the path it receives.
//
// WHY ONE BUILD. Every call site used to build its own copy into its TempDir,
// 51 builds per run of the package, for a binary that is the same bytes each
// time. One build per test process is the same proof at a fraction of the
// cost.
//
// WHY NOT -race. The copies were built with the race detector, and one hook
// invocation of a race-instrumented child costs 1.1-1.3 s where the plain
// child does the same store open, bootstrap and journal write in 0.01-0.06 s.
// Every CONCURRENT path a child runs (the hook's work goroutine, its deadline
// select and the fault writer, all inside lifecycleOutcome) also runs
// IN-PROCESS in this package under the outer `go test -race`: the fault, panic
// and abandonment proofs drive lifecycleOutcome and the handler entry points
// directly, so the race detector already reads those paths once per run. Two
// subcommands reach the child only: `hook lifecycle raw` and `hook lifecycle
// manifest`. Both RunE bodies are sequential and neither handler starts a
// goroutine, so the race detector had nothing to observe on either of them.
// The manifest handler is also exercised under -race in internal/handlers; the
// raw handler is exercised in this module only through the plain child in this
// package, and a plain child loses nothing on a sequential path. Instrumenting
// the child as well duplicated that coverage at 20x per invocation, across
// hundreds of invocations per run, and it was the largest part of the
// package's wall time. The one proof that needs a race-instrumented SEPARATE PROCESS, because
// a second opener contends for the real SQLite lock while the hook waits on
// its deadline, runs raceLifecycleBinary instead.
//
// The build needs no cgo: the SQLite driver is pure Go, and the smoke binary
// that TestMain builds for the command tests is built the same way.
func lifecycleBinary(t *testing.T) string {
	t.Helper()
	return plainLifecycleChild.resolve(t)
}

// raceLifecycleBinary returns the ONE race-instrumented build of this command,
// built on first request and shared for the rest of the process. It exists for
// the held-lock deadline proof alone: that proof measures a live process that
// contends with a second opener for the real SQLite write lock, and the race
// detector reading that contention in a separate process is what no in-process
// proof can give. Every other built-binary proof runs lifecycleBinary; see its
// doc for why a race-instrumented child there bought nothing at 20x the cost.
func raceLifecycleBinary(t *testing.T) string {
	t.Helper()
	return raceLifecycleChild.resolve(t)
}

func initializeLifecycleTestDatabase(t *testing.T, dbPath string) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://lifecycle-production-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
}

func queryLifecycleEvidence(t *testing.T, journal provenance.Journal, kind provenance.EvidenceKind) []provenance.EvidenceRow {
	t.Helper()
	page, err := journal.Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{kind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize},
	})
	require.NoError(t, err)
	return page.Rows
}

func readProductionClaudeFixture(t *testing.T, fixture, expectedEvent string) []byte {
	t.Helper()
	return readProductionClaudeFixtureAt(t, fixture, expectedEvent, "2.1.261", "internal/handlers/capture_sink.go (PASTURE_CAPTURE_DIR)")
}

func readProductionClaudeFixtureAt(t *testing.T, fixture, expectedEvent, expectedVersion, expectedSource string) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata")
	relativeFixture := filepath.Join("fixtures", fixture)
	raw, err := os.ReadFile(filepath.Join(root, relativeFixture))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))

	provenancePath := filepath.Join(root, "fixtures", strings.TrimSuffix(fixture, ".json")+".provenance.json")
	provenanceBytes, err := os.ReadFile(provenancePath)
	require.NoError(t, err)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(provenanceBytes, &members))
	require.ElementsMatch(t, []string{"origin", "harness", "harnessVersion", "captureSource", "rawFileDigest", "capturedAt", "redaction", "event", "clearance"}, mapKeys(members))
	var sidecar acceptance.CaptureProvenance
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
	require.Equal(t, acceptance.HarnessClaudeCode, sidecar.Harness)
	require.NotEmpty(t, expectedVersion)
	require.NotEmpty(t, expectedSource)
	require.Equal(t, expectedVersion, sidecar.HarnessVersion, "capture version is independent of the registration root")
	require.Equal(t, expectedSource, sidecar.CaptureSource)
	rules, err := acceptance.ParseRedaction(sidecar.Redaction)
	require.NoError(t, err)
	require.Equal(t, acceptance.RedactionHomePath, rules[0], "every Claude fixture carries the home path, so home-path-v1 is applied first")
	require.Equal(t, expectedEvent, sidecar.Event)
	require.Equal(t, digest.FromBytes(raw).String(), sidecar.RawFileDigest)
	require.NoError(t, sidecar.ValidateFixture(root, relativeFixture))
	return raw
}

func decodeOccurrencePayload(t *testing.T, raw []byte) lifecycleOccurrencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"contract", "event", "envelope", "bindings", "capture", "body_digest"}, mapKeys(members))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload lifecycleOccurrencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return payload
}

func decodeInterpretedPayload(t *testing.T, raw []byte) interpretedEvidencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"semantic", "identities", "unresolved_facts", "contract", "manifest"}, mapKeys(members))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload interpretedEvidencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return payload
}

// assertUnboundProceedConsultation pins the durable reason of a Proceed that the
// gate read from an UNCLAIMED session. This is a transport subject: it drives a
// real host event with no session-start claim, so the policy's rule for an
// unbound session is the truth it records. The reason is deliberately NOT
// "legal": "legal" was the middle-end default for a policy evaluation that
// never ran, and an unclaimed session has no session identity for the gate to
// evaluate. A transport subject that needs the evaluated-Proceed reason seeds
// a real claim and an owning assignment instead, and asserts "legal" there.
func assertUnboundProceedConsultation(t *testing.T, raw []byte) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"legalized", "decision", "interpreted"}, mapKeys(members))
	require.JSONEq(t, `{"decision":"proceed","reason":"unbound-session"}`, string(members["decision"]))
	interpreted := decodeJSONObject(t, members["interpreted"])
	require.ElementsMatch(t, []string{"result_slot", "content_digest"}, mapKeys(interpreted))
	require.JSONEq(t, `"interpreted"`, string(interpreted["result_slot"]))
}

func publicIdentityPayloads(values []waist.SemanticIdentity) []interpretedIdentityPayload {
	out := make([]interpretedIdentityPayload, len(values))
	for index, value := range values {
		out[index] = interpretedIdentityPayload{Kind: uint8(value.Kind), Value: value.Value}
	}
	return out
}

func publicUnresolvedPayloads(values []waist.UnresolvedFact) []interpretedUnresolvedPayload {
	out := make([]interpretedUnresolvedPayload, len(values))
	for index, value := range values {
		out[index] = interpretedUnresolvedPayload{Reason: uint8(value.Reason)}
	}
	return out
}

func assertOccurrencePayload(t *testing.T, raw []byte, body []byte, capture model.CaptureDisposition, event model.ContractEventKind) lifecycleOccurrencePayload {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"contract", "event", "envelope", "bindings", "capture", "body_digest"}, mapKeys(members))
	require.JSONEq(t, strconv.Quote(occurrenceLifecycleContract), string(members["contract"]))
	require.JSONEq(t, fmt.Sprintf("%d", event), string(members["event"]))
	if capture == model.CaptureMalformed {
		require.JSONEq(t, `2`, string(members["capture"]))
	} else {
		require.JSONEq(t, `1`, string(members["capture"]))
	}
	assertOccurrenceEnvelope(t, members["envelope"])
	if capture == model.CaptureMalformed {
		require.JSONEq(t, `null`, string(members["bindings"]))
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload lifecycleOccurrencePayload
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.Equal(t, occurrenceLifecycleContract, payload.Contract)
	require.Equal(t, event, payload.Event)
	require.Equal(t, occurrenceLifecycleContract, payload.Envelope.Runtime.Contract.String())
	require.Equal(t, capture, payload.Capture)
	require.Equal(t, "2.1.261", payload.Envelope.HostVersion)
	require.Equal(t, model.HostVersionCallerSupplied, payload.Envelope.HostVersionSource)
	sum := sha256.Sum256(body)
	require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), payload.Body)
	return payload
}

func decodeJSONObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var members map[string]json.RawMessage
	require.NoError(t, decoder.Decode(&members))
	require.NotNil(t, members)
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return members
}

func assertOccurrenceEnvelope(t *testing.T, raw json.RawMessage) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"Runtime", "HostVersion", "hostVersionSource", "Schema", "Implementation", "Retention"}, mapKeys(members))
	require.JSONEq(t, `"2.1.261"`, string(members["HostVersion"]))
	require.JSONEq(t, strconv.Quote(string(model.HostVersionCallerSupplied)), string(members["hostVersionSource"]))

	runtime := decodeJSONObject(t, members["Runtime"])
	require.ElementsMatch(t, []string{"Definition", "Contract"}, mapKeys(runtime))
	require.JSONEq(t, strconv.Quote(occurrenceLifecycleContract), string(runtime["Contract"]))
	assertZeroDefinitionRef(t, runtime["Definition"])

	for _, wrapper := range []string{"Schema", "Implementation", "Retention"} {
		definition := decodeJSONObject(t, members[wrapper])
		require.ElementsMatch(t, []string{"Definition"}, mapKeys(definition))
		assertZeroDefinitionRef(t, definition["Definition"])
	}
}

func assertZeroDefinitionRef(t *testing.T, raw json.RawMessage) {
	t.Helper()
	members := decodeJSONObject(t, raw)
	require.ElementsMatch(t, []string{"Definition", "Kind", "Content"}, mapKeys(members))
	require.JSONEq(t, `0`, string(members["Definition"]))
	require.JSONEq(t, `0`, string(members["Kind"]))
	require.JSONEq(t, `[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]`, string(members["Content"]))
}

func assertInterpretedEvidence(t *testing.T, raw []byte) {
	t.Helper()
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	require.NoError(t, decoder.Decode(&members))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.ElementsMatch(t, []string{"semantic", "identities", "unresolved_facts", "contract", "manifest"}, mapKeys(members))
	require.Equal(t, json.RawMessage(`1`), members["semantic"])
	require.Equal(t, json.RawMessage(expectedInterpretedIdentities), members["identities"])
	require.Equal(t, json.RawMessage(strconv.Quote(interpretedLifecycleContract)), members["contract"])

	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload interpretedEvidencePayload
	require.NoError(t, decoder.Decode(&payload))
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	require.Equal(t, uint8(runtime.SemanticObservation), payload.Semantic)
	require.Len(t, payload.Identities, 1)
	require.Equal(t, uint8(runtime.IdentitySession), payload.Identities[0].Kind)
	require.Equal(t, expectedSessionIdentity, payload.Identities[0].Value)
	require.Empty(t, payload.UnresolvedFacts)
	require.Equal(t, interpretedLifecycleContract, payload.Contract)
	// interpreted.v2 carries the metamodel coordinate it was interpreted against.
	active := metamodel.Active()
	require.Equal(t, string(active.ID), payload.Metamodel.ID)
	require.Equal(t, active.Version, payload.Metamodel.Version)
	require.Equal(t, hex.EncodeToString(active.Content[:]), payload.Metamodel.Content)
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func assertSharedOperation(t *testing.T, occurrence, interpreted provenance.EvidenceRow) {
	t.Helper()
	require.NotEmpty(t, occurrence.ProducingOperationID)
	require.NotEmpty(t, interpreted.ProducingOperationID)
	require.Equal(t, occurrence.ProducingOperationID, interpreted.ProducingOperationID)
	require.NotZero(t, occurrence.ProducingOperationJournalID)
	require.Equal(t, occurrence.ProducingOperationJournalID, interpreted.ProducingOperationJournalID)
}

// --- activation-last integrated Codex production proof --------------------
//
// The committed Codex handler dispatch was default-off through implementation and
// review ("activation last"): the two selected events
// became enabled in the committed default only after acceptance review. The
// proofs below exercise the enabled path NOW, on the real production handler
// path, by injecting the committed activation catalog activation.Codex0_153_0()
// through the sanctioned HookLifecycleInput.Activations pre-activation seam
// (documented in internal/handlers/hook_lifecycle.go as "not a separate
// test-only code path"). Native continuation bytes are produced by the exact
// per-target encoder the CLI RunE invokes through handlers.HookLifecycleNative
// via the registry encode member (nativeresponse.CodexContinuation for Codex,
// nativeresponse.CanonicalProceed for the canonical-object harnesses).

type codexProductionFixture struct {
	name            string // subtest name; must equal activation.ProductionProofCodex*.Name()'s test suffix
	fixture         string
	event           string
	kind            model.ContractEventKind
	semantic        runtime.EventSemantic
	wantResponse    bool
	wantNative      []byte
	wantEvidence    []provenance.EvidenceKind
	identities      map[runtime.NativeIdentityKind]string
	captureProof    activation.CaptureProof    // must cite the fixture this proof actually reads
	productionProof activation.ProductionProof // event-bound accepted proof; this test also checks durable read-back
}

var codexProductionFixtures = []codexProductionFixture{
	{
		name: "SessionStart", fixture: "session_start_0_153_0.json", event: "SessionStart",
		kind: registration.EventCodexSessionStart, semantic: runtime.SemanticObservation,
		wantResponse: false, wantNative: []byte(`{}`),
		wantEvidence:    []provenance.EvidenceKind{occurrenceEvidenceKind, interpretedEvidenceKind},
		identities:      map[runtime.NativeIdentityKind]string{runtime.IdentitySession: "01a07188-cfc9-7bc2-b51b-4f53cf18f9f5"},
		captureProof:    activation.CaptureProofCodexSessionStart,
		productionProof: activation.ProductionProofCodexSessionStart,
	},
	{
		name: "PreToolUse", fixture: "pre_tool_use_0_153_0.json", event: "PreToolUse",
		kind: registration.EventCodexPreToolUse, semantic: runtime.SemanticGateConsultation,
		wantResponse: true, wantNative: []byte(`{"continue":true}`),
		wantEvidence: []provenance.EvidenceKind{occurrenceEvidenceKind, interpretedEvidenceKind, consultationEvidenceKind},
		identities: map[runtime.NativeIdentityKind]string{
			runtime.IdentitySession:  "01a07188-cfc9-7bc2-b51b-4f53cf18f9f5",
			runtime.IdentityTurn:     "01a0718c-6f88-73c2-8d82-013e647cc02f",
			runtime.IdentityToolCall: "exec-8d2f7dd7-3b46-40af-af0a-252dad7bbbec",
		},
		captureProof:    activation.CaptureProofCodexPreToolUse,
		productionProof: activation.ProductionProofCodexPreToolUse,
	},
}

// TestEnabledCodexHandlersToDurableReadBack is the SessionStart-ingress and
// PreToolUse-gate integrated production proof. For each
// authentic Codex 0.153.0 fixture it drives the real durable handler path with
// the committed activation catalog injected, proves the durable receipt commits
// before the native continuation bytes are available, and proves the persisted
// evidence is provider-correct on bounded public read-back. It mirrors
// TestEnabledOpenCodeHandlersToDurableReadBack for the Codex provider.
func TestEnabledCodexHandlersToDurableReadBack(t *testing.T) {
	t.Parallel()

	activations, err := activation.Codex0_153_0()
	require.NoError(t, err)
	for _, tc := range codexProductionFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// The legacy accepted pair cited this running test. The generated
			// runner campaign owns the stronger transport proof after expansion.
			// Keep both real source owners explicit, without claiming this direct
			// handler test executes the generated runner on its owner's behalf.
			proofEvent, bound := tc.productionProof.Event()
			require.True(t, bound)
			require.Equal(t, tc.kind, proofEvent)
			proofHarness, bound := tc.productionProof.Harness()
			require.True(t, bound)
			require.Equal(t, ir.HarnessCodex, proofHarness)
			reference := tc.productionProof.Name()
			legacyReference := "cmd/pasture/hook_lifecycle_production_test.go:" + t.Name()
			runnerReference := "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/" + tc.event
			require.Contains(t, []string{legacyReference, runnerReference}, reference,
				"the proof must cite the actual event-bound legacy or generated-runner proof, never an invented owner")
			parts := strings.SplitN(reference, ":", 2)
			require.Len(t, parts, 2)
			proofSource, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", parts[0]), nil, 0)
			require.NoError(t, err)
			proofFunction := strings.SplitN(parts[1], "/", 2)[0]
			found := false
			for _, declaration := range proofSource.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				found = found || ok && function.Name.Name == proofFunction
			}
			require.True(t, found, "the cited production proof function must exist in its real source file")

			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			raw := readCodexProductionFixture(t, tc.fixture, tc.event, tc.captureProof)

			response, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
				DBPath: dbPath, Harness: ir.HarnessCodex, Event: tc.event, HostVersion: registration.Codex0_153_0().Version,
				Input: bytes.NewReader(raw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
				Activations: activations,
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantResponse, response.IsValid())

			// Native continuation bytes are the exact host stdout for this event,
			// through the same per-target encoder the CLI RunE invokes (the Codex
			// registry encode member, via handlers.HookLifecycleNative).
			native, err := nativeresponse.CodexContinuation(response)
			require.NoError(t, err)
			require.Equal(t, tc.wantNative, native, "native continuation bytes must equal the pinned golden shape")

			// Returning from the handler must imply every expected effect is
			// already durably readable (commit precedes response/native encoding).
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			for _, kind := range tc.wantEvidence {
				require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), kind), 1, "durable %s evidence must be committed before the handler returns", kind)
			}
			if tc.wantResponse {
				interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)[0]
				consultation := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)[0]
				require.Equal(t, interpreted.ProducingOperationJournalID, consultation.ProducingOperationJournalID, "one durable operation groups interpreted and consultation evidence")
				require.Less(t, interpreted.JournalID, consultation.JournalID, "interpreted evidence precedes consultation evidence")
				require.Contains(t, string(consultation.Payload), `"decision":{"decision":"proceed","reason":"unbound-session"}`,
					"a transport gate with no session-start claim records the unbound-session reason")
			} else {
				require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind), "an observation produces no consultation evidence")
			}
			require.NoError(t, tracker.Close())

			// Bounded provider-correct public read-back.
			identities, semantic := readBackGate(t, dbPath, registration.Codex0_153_0().Contract, runtime.Codex0_153_0().ID(), tc.kind)
			require.Equal(t, tc.semantic, semantic)
			require.Equal(t, tc.identities, identities, "read-back identities must be the provider-correct Codex correlation set")
		})
	}
}

// TestCodexAndOpenCodeGateDifferentialPreservesProviderFacts is the
// two-live-provider differential. It drives the authentic Codex PreToolUse gate
// and the authentic OpenCode tool.execute.before gate through their real
// production handler paths, then asserts their common Proceed gate semantics
// agree while provider-specific identity, contract, event-name, and native
// continuation facts stay distinct. It never asserts whole-payload identity.
func TestCodexAndOpenCodeGateDifferentialPreservesProviderFacts(t *testing.T) {
	t.Parallel()

	// --- Codex PreToolUse gate: live production path, injected committed catalog.
	codexActivations, err := activation.Codex0_153_0()
	require.NoError(t, err)
	codexRaw := readCodexProductionFixture(t, "pre_tool_use_0_153_0.json", "PreToolUse", activation.CaptureProofCodexPreToolUse)
	codexDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, codexDB)
	codexResponse, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: codexDB, Harness: ir.HarnessCodex, Event: "PreToolUse", HostVersion: registration.Codex0_153_0().Version,
		Input: bytes.NewReader(codexRaw), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
		Activations: codexActivations,
	})
	require.NoError(t, err)
	codexIdentities, codexSemantic := readBackGate(t, codexDB, registration.Codex0_153_0().Contract, runtime.Codex0_153_0().ID(), registration.EventCodexPreToolUse)
	codexNative, err := nativeresponse.CodexContinuation(codexResponse)
	require.NoError(t, err)

	// --- OpenCode tool.execute.before gate: live production path, default-enabled.
	openCodeWire := openCodeToolExecuteBeforeWire(t)
	openCodeDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, openCodeDB)
	openCodeResponse, err := handlers.HookLifecycleResponse(context.Background(), handlers.HookLifecycleInput{
		DBPath: openCodeDB, Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: registration.OpenCode1_18_29().Version,
		Input: bytes.NewReader(openCodeWire), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{},
	})
	require.NoError(t, err)
	openCodeIdentities, openCodeSemantic := readBackGate(t, openCodeDB, registration.OpenCode1_18_29().Contract, runtime.OpenCode1_18_29().ID(), registration.EventOpenCodeToolExecuteBefore)
	openCodeNative, err := nativeresponse.CanonicalProceed(openCodeResponse)
	require.NoError(t, err)

	// EQUAL: the common Proceed gate semantic agrees across both live providers.
	require.True(t, codexResponse.IsValid(), "the Codex gate must produce a valid Proceed response")
	require.True(t, openCodeResponse.IsValid(), "the OpenCode gate must produce a valid Proceed response")
	require.Equal(t, runtime.SemanticGateConsultation, codexSemantic)
	require.Equal(t, codexSemantic, openCodeSemantic, "both live providers derive the same gate-consultation semantic")
	codexGate, err := runtime.Codex0_153_0Lifecycle().Mapping(runtime.CodexEventPreToolUse)
	require.NoError(t, err)
	openCodeGate, err := runtime.OpenCode1_18_29Lifecycle().Mapping(runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	require.Equal(t, codexGate.Semantic(), openCodeGate.Semantic(), "the shared gate semantic is provider-neutral")
	require.Equal(t, codexGate.Blocking(), openCodeGate.Blocking(), "both gate mappings share the same blocking mode")

	// DISTINCT: provider-specific facts stay separate; no whole-payload identity.
	require.NotEqual(t, runtime.Codex0_153_0().ID(), runtime.OpenCode1_18_29().ID(), "interpreted runtime contracts remain provider-correct")
	require.NotEqual(t, registration.Codex0_153_0().Contract, registration.OpenCode1_18_29().Contract, "registration contracts remain provider-correct")
	require.Equal(t, []byte(`{"continue":true}`), codexNative)
	require.Equal(t, []byte(`{"decision":"proceed"}`), openCodeNative)
	require.NotEqual(t, codexNative, openCodeNative, "provider-specific native continuation shapes remain distinct")
	require.Equal(t, "PreToolUse", codexGate.NativeName())
	require.Equal(t, "tool.execute.before", openCodeGate.NativeName())

	// Provider-correct correlation: distinct native identity paths and values.
	require.Equal(t, []string{"session_id", "turn_id", "tool_use_id"}, gateIdentityNativeNames(codexGate))
	require.Equal(t, []string{"sessionID", "callID"}, gateIdentityNativeNames(openCodeGate))
	require.Contains(t, codexIdentities, runtime.IdentityTurn, "only the Codex gate carries a turn correlation")
	require.NotContains(t, openCodeIdentities, runtime.IdentityTurn, "the OpenCode gate carries no turn correlation")
	require.NotEqual(t, codexIdentities[runtime.IdentitySession], openCodeIdentities[runtime.IdentitySession], "session identity values are provider-specific")
	require.NotEqual(t, codexIdentities[runtime.IdentityToolCall], openCodeIdentities[runtime.IdentityToolCall], "tool-call identity values are provider-specific")
}

// TestCodexActivationLeavesClaudeAndOpenCodeArtifactsIsolated is the
// activation-isolation obligation at the committed-artifact layer. Codex now
// publishes its OWN committed activation audit report at
// .codex/pasture-codex-activation.json (Stage 1 #24, mirroring the Claude
// precedent): its presence is asserted here, but landing it must not write
// Codex provenance into the Claude or OpenCode activation artifacts, and it must
// not resurrect the legacy .codex/pasture-activation.json filename. The
// byte-identity of the Claude and OpenCode artifacts across regeneration is
// additionally guaranteed by the L3 zero-diff `make generate` gate.
func TestCodexActivationLeavesClaudeAndOpenCodeArtifactsIsolated(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	claudeActivation, err := os.ReadFile(filepath.Join(root, "hooks", "pasture-activation.json"))
	require.NoError(t, err)
	require.Contains(t, string(claudeActivation), `"harness": "claude-code"`, "the shared activation report remains the Claude-only artifact")
	require.NotContains(t, string(claudeActivation), "ingress/codex", "no Codex capture proof may leak into the Claude activation artifact")
	require.NotContains(t, string(claudeActivation), "TestEnabledCodexHandlersToDurableReadBack", "no Codex production proof may leak into the Claude activation artifact")
	require.NotContains(t, string(claudeActivation), "codex_transport_e2e_test.go", "no generated-runner Codex proof may leak into the Claude artifact")

	openCodeManifest, err := os.ReadFile(filepath.Join(root, ".opencode", "pasture-opencode.json"))
	require.NoError(t, err)
	require.Contains(t, string(openCodeManifest), `"target": "opencode"`)
	require.NotContains(t, string(openCodeManifest), "ingress/codex", "no Codex capture proof may leak into the OpenCode manifest")
	require.NotContains(t, string(openCodeManifest), "codex", "the OpenCode manifest carries no Codex activation entry")

	// Codex now emits its own committed activation audit report, unconditionally,
	// derived from registration.Codex0_153_0() + activation.Codex0_153_0().
	codexReportPath := filepath.Join(root, ".codex", "pasture-codex-activation.json")
	codexReport, err := os.ReadFile(codexReportPath)
	require.NoError(t, err, "the Codex activation audit report must be a committed artifact at %s", codexReportPath)

	// Content equality against a freshly derived report — no golden literals for
	// content, so catalog drift in registration.Codex0_153_0() or
	// activation.Codex0_153_0() is caught here rather than silently accepted.
	wantReport := deriveCodexActivationReport(t)
	require.Equal(t, string(wantReport), string(codexReport), "the committed Codex activation report must equal the report freshly derived from the pinned Codex registration + activation catalogs")

	// Exhaustive registration order and exact enabled membership follow the
	// actual validated activation declarations, not a stale two-event list.
	var parsed struct {
		Harness string `json:"harness"`
		Events  []struct {
			Event string `json:"event"`
			State string `json:"state"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(codexReport, &parsed))
	require.Equal(t, "codex", parsed.Harness, "the Codex audit report is the Codex-only artifact")
	require.Len(t, parsed.Events, len(registration.Codex0_153_0().Entries()), "the Codex activation report is exhaustive over every generated Codex event")
	states, err := activation.Codex0_153_0()
	require.NoError(t, err)
	byKind := make(map[model.ContractEventKind]activation.Entry, len(states))
	for _, state := range states {
		require.True(t, state.IsValid())
		_, duplicate := byKind[state.Event]
		require.False(t, duplicate)
		byKind[state.Event] = state
	}
	var expectedEnabled []string
	for index, event := range registration.Codex0_153_0().Entries() {
		require.Equal(t, event.NativeName, parsed.Events[index].Event, "report order follows registration")
		state, declared := byKind[event.Kind]
		require.True(t, declared)
		require.Equal(t, state.State.String(), parsed.Events[index].State)
		if state.State == activation.Enabled {
			proofEvent, bound := state.ProductionProof.Event()
			require.True(t, bound)
			require.Equal(t, event.Kind, proofEvent)
			captureEvent, bound := state.CaptureProof.Event()
			require.True(t, bound)
			require.Equal(t, event.Kind, captureEvent)
			expectedEnabled = append(expectedEnabled, event.NativeName)
		}
	}
	require.NotEmpty(t, expectedEnabled, "the enabled-membership proof must not be vacuous")
	var enabled []string
	for _, entry := range parsed.Events {
		if entry.State == "enabled" {
			enabled = append(enabled, entry.Event)
		}
	}
	require.Equal(t, expectedEnabled, enabled, "report membership must equal the event-bound activation declarations")

	// The legacy Codex activation filename must never be emitted: the report
	// lives only at pasture-codex-activation.json.
	legacy := filepath.Join(root, ".codex", "pasture-activation.json")
	_, statErr := os.Stat(legacy)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the legacy Codex activation filename must not be emitted at %s", legacy)
}

// deriveCodexActivationReport renders the exact bytes the Codex activation
// audit report must contain through the product's own emitter, which reads the
// pinned registration manifest and activation catalog live. It carries no
// golden literals: a catalog change forces the committed artifact to change in
// lockstep or this test fails. The report's row shape has one builder in
// internal/codegen and this test does not copy it, so the shape cannot drift
// between the emitter and the test.
func deriveCodexActivationReport(t *testing.T) []byte {
	t.Helper()
	report, err := codegen.RenderCodexActivationReport()
	require.NoError(t, err)
	return []byte(report)
}

// readBackGate rebuilds and reads back the single committed occurrence for a
// live gate, asserts its provider-correct registration/runtime contract and
// event kind, and returns its interpreted correlation identities and semantic.
func readBackGate(t *testing.T, dbPath string, wantRegistrationContract ir.RuntimeContractID, wantRuntimeContract any, wantKind model.ContractEventKind) (map[runtime.NativeIdentityKind]string, runtime.EventSemantic) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	pageSize, err := model.NewPageSize(4)
	require.NoError(t, err)
	records, err := reader.Records(context.Background(), model.OccurrenceQuery{Page: model.PageRequest{Size: pageSize}})
	require.NoError(t, err)
	require.Len(t, records.Records(), 1, "each provider's live gate must persist exactly one occurrence")
	record := records.Records()[0]
	require.Equal(t, wantRegistrationContract, record.Occurrence.RuntimeContract)
	require.Equal(t, wantKind, record.Occurrence.Kind)
	require.Len(t, record.Interpreted(), 1)
	interpreted := record.Interpreted()[0]
	require.Equal(t, wantRuntimeContract, interpreted.Contract())
	out := make(map[runtime.NativeIdentityKind]string, len(interpreted.Identities()))
	for _, identity := range interpreted.Identities() {
		out[identity.Kind] = identity.Value
	}
	return out, interpreted.Semantic()
}

func gateIdentityNativeNames(mapping runtime.LifecycleEventMapping) []string {
	names := make([]string, 0)
	for _, identity := range mapping.Identities() {
		names = append(names, identity.NativeName())
	}
	return names
}

// openCodeToolExecuteBeforeWire reconstructs the exact stdin bytes the generated
// OpenCode plugin sends the CLI for tool.execute.before —
// JSON.stringify({ input, output: { args } }) — from the authentic capture.
func openCodeToolExecuteBeforeWire(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "opencode", "testdata", "fixtures")
	raw, err := os.ReadFile(filepath.Join(root, "tool_execute_before_1_18_29.json"))
	require.NoError(t, err)
	var capture struct {
		Input  json.RawMessage `json:"input"`
		Output struct {
			Args json.RawMessage `json:"args"`
		} `json:"output"`
	}
	require.NoError(t, json.Unmarshal(raw, &capture))
	type outputArgs struct {
		Args json.RawMessage `json:"args"`
	}
	wire, err := json.Marshal(struct {
		Input  json.RawMessage `json:"input"`
		Output outputArgs      `json:"output"`
	}{Input: capture.Input, Output: outputArgs{Args: capture.Output.Args}})
	require.NoError(t, err)
	return wire
}

func readCodexProductionFixture(t *testing.T, fixture, expectedEvent string, captureProof activation.CaptureProof) []byte {
	t.Helper()
	relDir := filepath.Join("internal", "lifecycle", "ingress", "codex", "testdata", "fixtures")
	root := filepath.Join("..", "..", relDir)
	raw, err := os.ReadFile(filepath.Join(root, fixture))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.Contains(t, string(raw), `"hook_event_name":"`+expectedEvent+`"`, "the authentic Codex fixture must carry its native event name")

	provenanceBytes, err := os.ReadFile(filepath.Join(root, strings.TrimSuffix(fixture, ".json")+".provenance.json"))
	require.NoError(t, err)
	var sidecar acceptance.CaptureProvenance
	require.NoError(t, json.Unmarshal(provenanceBytes, &sidecar))
	require.Equal(t, acceptance.HarnessCodexCLI, sidecar.Harness)
	require.Equal(t, registration.Codex0_153_0().Version, sidecar.HarnessVersion, "the Codex fixture was captured at the recorded host version")
	require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
	require.Equal(t, expectedEvent, sidecar.Event)
	require.NoError(t, sidecar.ValidateFixture(root, fixture), "the committed Codex fixture bytes must match the cleared digest its sidecar records")
	sum := sha256.Sum256(raw)
	require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), sidecar.RawFileDigest, "authentic Codex fixture digest must match the cleared digest exactly")

	// Capture-proof linkage (constant -> fixture, path -> bytes -> digest): the
	// activation catalog's CaptureProof referent must cite the EXACT fixture this
	// production proof reads, and the bytes at that cited path must reproduce the
	// cleared digest the proof enforces (sidecar.RawSHA256, the single source of
	// truth — no duplicated digest literal). A moved fixture or an edited referent
	// string breaks this immediately instead of leaving the constant stale.
	citedPath, _, found := strings.Cut(captureProof.Name(), " (")
	require.True(t, found, "CaptureProof.Name() must be 'relative/path (description)'; got %q", captureProof.Name())
	require.Equal(t, filepath.ToSlash(filepath.Join(relDir, fixture)), citedPath,
		"CaptureProof.Name() must cite the exact fixture path this production proof reads")
	citedBytes, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(citedPath)))
	require.NoError(t, err, "the fixture path cited by CaptureProof.Name() must resolve to a real file")
	require.Equal(t, raw, citedBytes, "the fixture cited by CaptureProof.Name() must be exactly the bytes this proof reads")
	citedSum := sha256.Sum256(citedBytes)
	require.Equal(t, sidecar.RawFileDigest, "sha256:"+hex.EncodeToString(citedSum[:]),
		"the fixture cited by CaptureProof.Name() must digest-match the cleared SHA-256 the proof enforces")
	return raw
}

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// build raw CLI exec-binary tests (SLICE-2 L2). These import the production
// path the users run — the built `pasture hook lifecycle raw` binary — and
// assert the ratified M4 contract:
//
//   - a valid raw payload for an enabled event is written through the SAME
//     gate path as native (warrant + Receive), commit-before-stdout, exit 0,
//     then stdout carries the EXACT bytes the native hook would emit for the
//     SAME event (per-event parity: a gate consultation emits the canonical
//     decision, a Claude/OpenCode observation emits nothing, a Codex
//     observation emits {});
//   - the committed record is equivalent to the native commit of the SAME
//     fixture modulo the origin carrier (same contract, event kind, bindings,
//     derivation, body digest; origin raw only on the raw side);
//   - withheld events refuse with the same typed activation refusal;
//   - unknown --schema-version, unknown --harness, malformed stdin, and
//     over-limit stdin refuse BEFORE the store opens (M1 §8 mirror, MINOR-1)
//     with exit 0, empty stdout, diagnostic stderr, and NO database file (nor
//     -wal/-shm);
//   - the 1 MiB payload bound is exact (MINOR-2): a payload of exactly
//     MaxNativePayloadBytes is not refused at the bound (it proceeds to
//     classification), one byte over refuses.
//
// EXPECTED TO FAIL at L2: the raw subcommand and handler land in L3, so the
// binary does not yet accept `hook lifecycle raw`; these tests are the
// contract L3 must satisfy. Do not green-wash them.
func TestRawLifecycleGateFlowMirrorsNative(t *testing.T) {
	binary := lifecycleBinary(t)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)

	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	// The valid path commits a receipt, exactly like the native positive tests
	// (hook_lifecycle_production_test.go): the unified store must be
	// bootstrapped once so the ingress identity resolves before the hook runs.
	initializeLifecycleTestDatabase(t, dbPath)
	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222",
		"--schema-version", "claude-code/2.1.210")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "a valid raw invocation must exit 0: %s", stderr.String())
	require.Empty(t, stderr.String(), "a valid raw invocation must not report a diagnostic")
	// SessionStart is a NON-BLOCKING observation: native Claude emits nothing
	// on stdout for it, and the raw hatch must match byte-for-byte (per-event
	// parity; the gate-event continuation is asserted in
	// TestRawContinuationParityWithNativePerEvent).
	require.Empty(t, stdout.String(),
		"a raw Claude observation must emit no host continuation, exactly like the native hook")

	// The committed occurrence must carry the raw origin on BOTH the receipt
	// payload member and the envelope member (ratified UAT-Q4), and must be the
	// only occurrence.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, occurrences, 1, "a valid raw invocation must commit exactly one occurrence")
	members := decodeJSONObject(t, occurrences[0].Payload)
	require.Contains(t, members, "origin")
	require.JSONEq(t, `"raw"`, string(members["origin"]), "occurrence payload must carry the raw origin")
	envelope := decodeJSONObject(t, members["envelope"])
	require.Contains(t, envelope, "origin")
	require.JSONEq(t, `"raw"`, string(envelope["origin"]), "occurrence envelope must carry the raw origin")
}

// TestRawContinuationParityWithNativePerEvent pins the per-event stdout
// parity between the native hook and the raw hatch for ALL continuation
// classes: a Claude/OpenCode observation emits nothing, a canonical gate
// emits the Proceed object, a Codex observation emits {}, and a Codex gate
// emits {"continue":true}. The ingestion hatch must never invent a reply
// shape the native hook would not have emitted for the same payload (review
// IMPORTANT-1: the former hardcoded rawAcknowledge printed a Proceed object
// for every event including observations).
func TestRawContinuationParityWithNativePerEvent(t *testing.T) {
	binary := lifecycleBinary(t)

	invoke := func(surface string, args []string, payload []byte) (string, string) {
		dbPath := filepath.Join(t.TempDir(), surface, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)
		full := append([]string{databaseFlagName.Argument(), dbPath}, args...)
		command := exec.Command(binary, full...)
		command.Stdin = bytes.NewReader(payload)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		require.NoError(t, command.Run(), "valid %s invocation must exit 0: %s", surface, stderr.String())
		require.Empty(t, stderr.String(), "valid %s invocation must not report a diagnostic", surface)
		return stdout.String(), dbPath
	}

	for _, tc := range []struct {
		name        string
		harness     string
		event       string
		hostVersion string
		schema      string
		fixture     string
		pkg         string
		want        string // the bytes BOTH surfaces must emit; "" means nothing
	}{
		{
			name: "claude session start observation", harness: "claude-code", event: "SessionStart",
			hostVersion: "2.1.222", schema: "claude-code/2.1.210",
			fixture: "session_start_2_1_222.json", pkg: "claude",
		},
		{
			name: "claude pre-tool-use gate", harness: "claude-code", event: "PreToolUse",
			hostVersion: "2.1.222", schema: "claude-code/2.1.210",
			fixture: "pre_tool_use_2_1_222.json", pkg: "claude", want: `{"decision":"proceed"}`,
		},
		{
			name: "codex session start observation", harness: "codex", event: "SessionStart",
			hostVersion: "0.146.0", schema: "codex/0.146.0",
			fixture: "session_start_0_146_0.json", pkg: "codex", want: `{}`,
		},
		{
			name: "codex pre-tool-use gate", harness: "codex", event: "PreToolUse",
			hostVersion: "0.146.0", schema: "codex/0.146.0",
			fixture: "pre_tool_use_0_146_0.json", pkg: "codex", want: `{"continue":true}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", tc.pkg, "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)

			nativeOut, _ := invoke("native", []string{"hook", "lifecycle",
				"--harness", tc.harness, "--event", tc.event, "--host-version", tc.hostVersion}, payload)
			rawOut, _ := invoke("raw", []string{"hook", "lifecycle", "raw",
				"--harness", tc.harness, "--event", tc.event, "--host-version", tc.hostVersion, "--schema-version", tc.schema}, payload)

			require.Equal(t, nativeOut, rawOut, "raw stdout must be byte-identical to native stdout for the same event")
			if tc.want == "" {
				require.Empty(t, rawOut, "%s must emit no host continuation", tc.name)
			} else {
				require.JSONEq(t, tc.want, rawOut, "%s must emit the canonical continuation", tc.name)
			}
		})
	}
}

// TestRawAndNativeCommitEquivalentRecordsModuloOrigin is the AC-3 URD
// must-pass equivalence (REVIEW-B SEV-IMPORTANT): the SAME fixture delivered
// through (a) the native `hook lifecycle` surface and (b) the raw
// `hook lifecycle raw` surface must commit records that differ ONLY in the
// origin carrier — contract, event kind, envelope (modulo origin), bindings,
// capture disposition, body digest, and the derived interpreted/consultation
// evidence are byte-equivalent. A raw-side regression that dispatched a
// different event kind or emitted different bindings would fail here.
func TestRawAndNativeCommitEquivalentRecordsModuloOrigin(t *testing.T) {
	binary := lifecycleBinary(t)

	for _, tc := range []struct {
		name         string
		fixture      string
		event        string
		wantStdout   string
		wantReaction bool // gate consultations emit consultation evidence
	}{
		{name: "observation session start", fixture: "session_start_2_1_222.json", event: "SessionStart"},
		{name: "gate pre-tool-use", fixture: "pre_tool_use_2_1_222.json", event: "PreToolUse", wantStdout: `{"decision":"proceed"}`, wantReaction: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)

			invoke := func(surface string) (stdout string, occurrence, interpreted, consultation []byte) {
				dbPath := filepath.Join(t.TempDir(), surface, tasks.DefaultDBFilename.String())
				initializeLifecycleTestDatabase(t, dbPath)
				var args []string
				if surface == "raw" {
					args = []string{"hook", "lifecycle", "raw",
						"--harness", "claude-code", "--event", tc.event, "--host-version", "2.1.222", "--schema-version", "claude-code/2.1.210"}
				} else {
					args = []string{"hook", "lifecycle",
						"--harness", "claude-code", "--event", tc.event, "--host-version", "2.1.222"}
				}
				command := exec.Command(binary, append([]string{databaseFlagName.Argument(), dbPath}, args...)...)
				command.Stdin = bytes.NewReader(payload)
				var out, errBuf bytes.Buffer
				command.Stdout = &out
				command.Stderr = &errBuf
				require.NoError(t, command.Run(), "%s delivery must exit 0: %s", surface, errBuf.String())
				require.Empty(t, errBuf.String(), "%s delivery must not report a diagnostic", surface)

				tracker, err := tasks.OpenTaskTracker(dbPath)
				require.NoError(t, err)
				defer tracker.Close()
				occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
				require.Len(t, occurrences, 1, "%s must commit exactly one occurrence", surface)
				interpretedRows := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
				require.Len(t, interpretedRows, 1, "%s must commit exactly one interpreted.v2 record", surface)
				consultationRows := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
				if tc.wantReaction {
					require.Len(t, consultationRows, 1, "%s gate must commit exactly one consultation record", surface)
				} else {
					require.Empty(t, consultationRows, "%s observation must not commit consultation evidence", surface)
				}
				var consultationPayload []byte
				if len(consultationRows) == 1 {
					consultationPayload = consultationRows[0].Payload
				}
				return out.String(), occurrences[0].Payload, interpretedRows[0].Payload, consultationPayload
			}

			nativeStdout, nativeOccurrence, nativeInterpreted, nativeConsultation := invoke("native")
			rawStdout, rawOccurrence, rawInterpreted, rawConsultation := invoke("raw")

			// stdout parity per event class.
			if tc.wantStdout != "" {
				require.JSONEq(t, tc.wantStdout, nativeStdout, "native gate must emit the canonical decision")
				require.JSONEq(t, tc.wantStdout, rawStdout, "raw gate must emit the canonical decision")
			} else {
				require.Empty(t, nativeStdout, "native observation must emit nothing")
				require.Empty(t, rawStdout, "raw observation must emit nothing (parity)")
			}

			// committed occurrence equivalence: everything except the origin.
			nativeMembers := decodeJSONObject(t, nativeOccurrence)
			rawMembers := decodeJSONObject(t, rawOccurrence)
			require.JSONEq(t, `"raw"`, string(rawMembers["origin"]), "raw occurrence must carry the raw origin")
			require.NotContains(t, nativeMembers, "origin", "native occurrence must not carry an origin member")
			delete(rawMembers, "origin")
			require.ElementsMatch(t, mapKeys(nativeMembers), mapKeys(rawMembers), "occurrence key sets must match modulo the origin carrier")
			for key := range nativeMembers {
				if key == "envelope" {
					continue
				}
				require.JSONEq(t, string(nativeMembers[key]), string(rawMembers[key]), "occurrence member %q must be equivalent", key)
			}

			// envelope equivalence modulo the origin carrier.
			nativeEnvelope := decodeJSONObject(t, nativeMembers["envelope"])
			rawEnvelope := decodeJSONObject(t, rawMembers["envelope"])
			require.JSONEq(t, `"raw"`, string(rawEnvelope["origin"]), "raw envelope must carry the raw origin")
			require.NotContains(t, nativeEnvelope, "origin", "native envelope must not carry an origin member")
			assertMembersModuloOrigin(t, nativeEnvelope, rawEnvelope)

			// The derived (L2) evidence must be byte-identical: no origin member
			// exists in interpreted.v2, so equivalence is exact.
			require.JSONEq(t, string(nativeInterpreted), string(rawInterpreted), "interpreted.v2 derivation must be byte-identical")
			if tc.wantReaction {
				require.NotEmpty(t, nativeConsultation)
				require.JSONEq(t, string(nativeConsultation), string(rawConsultation), "consultation.v1 derivation must be byte-identical")
			}
		})
	}
}

// assertMembersModuloOrigin asserts two decoded JSON object member maps are
// byte-equivalent except for an "origin" carrier member present only in the
// raw side map.
func assertMembersModuloOrigin(t *testing.T, native, raw map[string]json.RawMessage) {
	t.Helper()
	delete(raw, "origin")
	require.ElementsMatch(t, mapKeys(native), mapKeys(raw), "member key sets must match modulo the origin carrier")
	for key, value := range native {
		require.JSONEq(t, string(value), string(lookup(raw, key)), "member %q must be equivalent modulo the origin carrier", key)
	}
}

// lookup returns the member value or an invalid raw message literal so a
// missing key fails the caller's JSONEq with a clear message.
func lookup(values map[string]json.RawMessage, key string) json.RawMessage {
	if value, found := values[key]; found {
		return value
	}
	return json.RawMessage(`"<missing>"`)
}

func TestRawWithheldEventIsNotAdmitted(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "opencode", "--event", "session.updated", "--host-version", "1.18.10",
		"--schema-version", "opencode/1.18.10")
	command.Stdin = strings.NewReader(`{"event":{"type":"session.updated"}}`)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "a withheld raw event must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "a withheld raw event must emit nothing on stdout")
	require.Contains(t, stderr.String(), "is withheld", "raw must reuse the native activation refusal")
	checkNoDatabaseFiles(t, dbPath)
}

// TestRawSchemaVersionRefusalCreatesNoDatabaseFile is the MINOR-1 raw mirror:
// an unknown --schema-version must refuse BEFORE the store opens and leave no
// database file (nor -wal/-shm sidecar) behind.
func TestRawSchemaVersionRefusalCreatesNoDatabaseFile(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222",
		"--schema-version", "claude-code/9.9.9")
	command.Stdin = bytes.NewReader([]byte(`{"hook_event_name":"SessionStart"}`))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "an unknown wire schema must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "an unknown wire schema must emit nothing on stdout")
	require.NotEmpty(t, stderr.String(), "an unknown wire schema must report an actionable diagnostic")
	require.Contains(t, stderr.String(), "not known to this build", "the refusal must name the wire-identity check")
	checkNoDatabaseFiles(t, dbPath)
}

// TestRawUnknownHarnessCreatesNoDatabaseFile is the raw-surface mirror of the
// native unknown-harness pin (REVIEW-B SEV-MINOR): an unknown --harness must
// refuse at dispatch resolution — before the store opens — leaving no
// database file, no stdout, and an actionable stderr diagnostic.
func TestRawUnknownHarnessCreatesNoDatabaseFile(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "not-a-harness", "--event", "SessionStart", "--host-version", "2.1.222",
		"--schema-version", "claude-code/2.1.210")
	command.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart"}`)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "an unknown raw harness must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "an unknown raw harness must emit nothing on stdout")
	require.Contains(t, stderr.String(), "not supported", "the refusal must name the harness dispatch check")
	checkNoDatabaseFiles(t, dbPath)
}

func TestRawMalformedStdinCreatesNoDatabaseFile(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222",
		"--schema-version", "claude-code/2.1.210")
	command.Stdin = bytes.NewReader([]byte(`not json at all`))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "malformed raw stdin must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "malformed raw stdin must print nothing on stdout")
	require.NotEmpty(t, stderr.String(), "malformed raw stdin must diagnose what went wrong")
	checkNoDatabaseFiles(t, dbPath)
}

func TestRawOverLimitStdinCreatesNoDatabaseFile(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle", "raw",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222",
		"--schema-version", "claude-code/2.1.210")
	command.Stdin = bytes.NewReader([]byte(strings.Repeat("x", model.MaxNativePayloadBytes+1)))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	require.NoError(t, command.Run(), "over-limit raw stdin must exit 0: %s", stderr.String())
	require.Empty(t, stdout.String(), "over-limit raw stdin must print nothing on stdout")
	require.Contains(t, stderr.String(), "exceeds", "the refusal must name the payload bound")
	checkNoDatabaseFiles(t, dbPath)
}

// TestRawPayloadBoundaryReachesClassificationPins the 1 MiB bound exactly
// (MINOR-2): a payload of exactly MaxNativePayloadBytes is NOT refused at the
// bound — it proceeds to classification (and here is refused as malformed),
// while one byte over is refused at the bound. This pins the strict `>` so an
// off-by-one in either direction cannot slip through green.
func TestRawPayloadBoundaryReachesClassification(t *testing.T) {
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

	for _, tc := range []struct {
		name string
		size int
		want string
	}{
		{name: "exactly at bound proceeds to classification", size: model.MaxNativePayloadBytes, want: "not a JSON object"},
		{name: "one byte over is refused at the bound", size: model.MaxNativePayloadBytes + 1, want: "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
				"hook", "lifecycle", "raw",
				"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222",
				"--schema-version", "claude-code/2.1.210")
			command.Stdin = bytes.NewReader(bytes.Repeat([]byte("x"), tc.size))
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), "%s: %s", tc.name, stderr.String())
			require.Empty(t, stdout.String(), "%s: nothing must reach stdout", tc.name)
			require.Contains(t, stderr.String(), tc.want, "%s: stderr must mention %q", tc.name, tc.want)
			checkNoDatabaseFiles(t, dbPath)
		})
	}
}

// checkNoDatabaseFiles asserts neither the database nor its -wal/-shm sidecars
// exist (M1 §8 mirror).
func checkNoDatabaseFiles(t *testing.T, dbPath string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_, statErr := os.Stat(dbPath + suffix)
		require.ErrorIs(t, statErr, os.ErrNotExist, "M1 §8: no %s database file may be created", suffix)
	}
}

// TestRawDryRunPreviewMatchesCommit is the SLICE-5 (UAT FIX-NOW) contract:
// `hook lifecycle raw --dry-run` reports the same contract, effect count, and
// canonical host continuation as a real commit of the same payload, without
// opening the store or issuing a receipt. Both an observation (empty
// continuation) and a gate event (non-empty continuation) pin the parity.
func TestRawDryRunPreviewMatchesCommit(t *testing.T) {
	binary := lifecycleBinary(t)

	for _, tc := range []struct {
		name             string
		fixture          string
		event            string
		wantEffects      int
		wantContinuation string
	}{
		{name: "observation", fixture: "session_start_2_1_222.json", event: "SessionStart", wantEffects: 1},
		{name: "gate", fixture: "pre_tool_use_2_1_222.json", event: "PreToolUse", wantEffects: 2, wantContinuation: `{"decision":"proceed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)

			// Never initialize the preview database: dry-run must not open it.
			previewDB := filepath.Join(t.TempDir(), "preview", tasks.DefaultDBFilename.String())
			previewCmd := exec.Command(binary, databaseFlagName.Argument(), previewDB,
				"hook", "lifecycle", "raw",
				"--harness", "claude-code", "--event", tc.event, "--host-version", "2.1.222",
				"--schema-version", "claude-code/2.1.210", "--dry-run")
			previewCmd.Stdin = bytes.NewReader(payload)
			var previewOut, previewErr bytes.Buffer
			previewCmd.Stdout = &previewOut
			previewCmd.Stderr = &previewErr
			require.NoError(t, previewCmd.Run(), "valid --dry-run must exit 0: %s", previewErr.String())
			require.Empty(t, previewErr.String(), "valid --dry-run must not report a diagnostic")

			var preview struct {
				DryRun      bool   `json:"dryRun"`
				Harness     string `json:"harness"`
				Event       string `json:"event"`
				HostVersion string `json:"hostVersion"`
				Schema      string `json:"schemaVersion"`
				Origin      string `json:"origin"`
				Contract    string `json:"contract"`
				Effects     []struct {
					Sort          string          `json:"sort"`
					ResultSlot    string          `json:"resultSlot"`
					EvidenceKind  string          `json:"evidenceKind"`
					ContentDigest string          `json:"contentDigest"`
					Payload       json.RawMessage `json:"payload"`
				} `json:"effects"`
				Continuation string `json:"continuation"`
			}
			previewMembers := decodeJSONObject(t, previewOut.Bytes())
			require.ElementsMatch(t,
				[]string{"dryRun", "harness", "event", "hostVersion", "schemaVersion", "origin", "contract", "effects", "continuation"},
				mapKeys(previewMembers),
				"preview JSON key set is a public operator contract",
			)
			require.NoError(t, json.Unmarshal(previewOut.Bytes(), &preview), "dry-run stdout must be the preview JSON")
			require.True(t, preview.DryRun, "preview must mark itself as a dry run")
			require.Equal(t, "Claude", preview.Harness)
			require.Equal(t, tc.event, preview.Event)
			require.Equal(t, "2.1.222", preview.HostVersion)
			require.Equal(t, "claude-code/2.1.210", preview.Schema)
			require.Equal(t, "raw", preview.Origin, "preview must disclose the raw origin")
			require.Equal(t, "claude-code/2.1.210", preview.Contract)
			require.Len(t, preview.Effects, tc.wantEffects)
			require.Equal(t, tc.wantContinuation, preview.Continuation)
			for _, effect := range preview.Effects {
				effectBytes, err := json.Marshal(effect)
				require.NoError(t, err)
				effectMembers := decodeJSONObject(t, effectBytes)
				require.ElementsMatch(t,
					[]string{"sort", "resultSlot", "evidenceKind", "contentDigest", "payload"},
					mapKeys(effectMembers),
					"preview effect key set is a public operator contract",
				)
			}
			checkNoDatabaseFiles(t, previewDB)

			commitDB := filepath.Join(t.TempDir(), "commit", tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, commitDB)
			commitCmd := exec.Command(binary, databaseFlagName.Argument(), commitDB,
				"hook", "lifecycle", "raw",
				"--harness", "claude-code", "--event", tc.event, "--host-version", "2.1.222",
				"--schema-version", "claude-code/2.1.210")
			commitCmd.Stdin = bytes.NewReader(payload)
			var commitOut, commitErr bytes.Buffer
			commitCmd.Stdout = &commitOut
			commitCmd.Stderr = &commitErr
			require.NoError(t, commitCmd.Run(), "real commit must exit 0: %s", commitErr.String())
			require.Empty(t, commitErr.String(), "real commit must not report a diagnostic")
			require.Equal(t, commitOut.String(), preview.Continuation, "preview continuation must match the real commit byte-for-byte")

			tracker, err := tasks.OpenTaskTracker(commitDB)
			require.NoError(t, err)
			defer tracker.Close()
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, occurrences, 1, "real ingestion must commit one occurrence")
			members := decodeJSONObject(t, occurrences[0].Payload)
			require.JSONEq(t, `"`+preview.Contract+`"`, string(members["contract"]), "preview contract must match the committed occurrence")
			interpretedRows := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
			consultationRows := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
			committedEffects := append(interpretedRows, consultationRows...)
			require.Len(t, preview.Effects, len(committedEffects), "preview effect count must match committed derivation evidence")
			wantSlots := []string{"interpreted"}
			if len(consultationRows) > 0 {
				wantSlots = append(wantSlots, "consultation")
			}
			for index, row := range committedEffects {
				effect := preview.Effects[index]
				require.Equal(t, "evidence", effect.Sort)
				require.Equal(t, wantSlots[index], effect.ResultSlot)
				require.Equal(t, string(row.EvidenceKind), effect.EvidenceKind)
				require.Equal(t, hex.EncodeToString(row.ContentDigest), effect.ContentDigest)
				require.JSONEq(t, string(row.Payload), string(effect.Payload), "preview effect payload must match committed evidence")
			}
		})
	}
}

// TestRawDryRunRefusesIdentically pins the UAT FIX-NOW invariant that the
// dry-run surface refuses invalid input EXACTLY as the committing surface
// does: the same typed diagnostic on stderr, empty stdout, exit 0, and no
// database file. The dry-run must never widen admission or soften refusal.
func TestRawDryRunRefusesIdentically(t *testing.T) {
	binary := lifecycleBinary(t)

	for _, tc := range []struct {
		name    string
		event   string
		payload []byte
		want    string
	}{
		{
			name:    "withheld activation",
			event:   "InstructionsLoaded",
			payload: []byte(`{"hook_event_name":"InstructionsLoaded","file_path":"/tmp/AGENTS.md","memory_type":"project","load_reason":"startup"}`),
			want:    "withheld",
		},
		{name: "malformed before preview branch", event: "SessionStart", payload: []byte(`not-json{`), want: "not a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoke := func(dryRun bool) (string, string) {
				dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())
				args := []string{databaseFlagName.Argument(), dbPath,
					"hook", "lifecycle", "raw",
					"--harness", "claude-code", "--event", tc.event, "--host-version", "2.1.222",
					"--schema-version", "claude-code/2.1.210"}
				if dryRun {
					args = append(args, "--dry-run")
				}
				command := exec.Command(binary, args...)
				command.Stdin = bytes.NewReader(tc.payload)
				var stdout, stderr bytes.Buffer
				command.Stdout = &stdout
				command.Stderr = &stderr
				require.NoError(t, command.Run(), "refused raw invocation must exit 0: %s", stderr.String())
				require.Empty(t, stdout.String(), "refused raw invocation must print nothing on stdout")
				checkNoDatabaseFiles(t, dbPath)
				return stdout.String(), stderr.String()
			}

			_, commitStderr := invoke(false)
			_, previewStderr := invoke(true)
			require.Equal(t, commitStderr, previewStderr, "dry-run and commit must render the same typed refusal")
			require.Contains(t, previewStderr, tc.want)
		})
	}
}

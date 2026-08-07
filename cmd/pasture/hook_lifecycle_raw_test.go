package main

import (
	"bytes"
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
//     then the canonical Proceed bytes on stdout;
//   - withheld events refuse with the same typed activation refusal;
//   - unknown --schema-version, malformed stdin, and over-limit stdin refuse
//     BEFORE the store opens (M1 §8 mirror, MINOR-1) with exit 0, empty
//     stdout, diagnostic stderr, and NO database file (nor -wal/-shm);
//   - the 1 MiB payload bound is exact (MINOR-2): a payload of exactly
//     MaxNativePayloadBytes is not refused at the bound (it proceeds to
//     classification), one byte over refuses.
//
// EXPECTED TO FAIL at L2: the raw subcommand and handler land in L3, so the
// binary does not yet accept `hook lifecycle raw`; these tests are the
// contract L3 must satisfy. Do not green-wash them.
func TestRawLifecycleGateFlowMirrorsNative(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)

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
	require.JSONEq(t, `{"decision":"proceed"}`, stdout.String(),
		"after the committed receipt the raw hatch must emit the canonical Proceed bytes")

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

func TestRawWithheldEventIsNotAdmitted(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
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
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
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

func TestRawMalformedStdinCreatesNoDatabaseFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
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
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
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
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	claudeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// readbackCLIOperations mirrors the production CLI operation identity source
// (lifecycleCLIOperations) while remaining deterministic, exactly as the
// receipt integration tests do; the committed journal is content-addressed so
// determinism does not alter the durable bytes read back by the built binary.
type readbackCLIOperations struct{ n int }

func (o *readbackCLIOperations) NewOperationID() (string, error) {
	o.n++
	return "pasture.lifecycle.readback-test." + string(rune('a'+o.n)), nil
}

type readbackCLIClock struct{}

func (readbackCLIClock) Now() time.Time { return time.Unix(100, 0).UTC() }

// TestRawOriginReadBackDisclosedInListTextAndJSON proves the SLICE-3
// production read path end-to-end through the BUILT binary: an occurrence
// delivered with the raw ingestion origin (committed through the sole durable
// write — receipt.Service.Receive on the production opener, the exact path the
// SLICE-2 raw subcommand traverses) is read back by `pasture hook lifecycle
// list` and the raw origin is disclosed in BOTH renderers.
//
// The raw CLI subcommand itself is SLICE-2's surface (not merged at this
// slice's base); the write side is seeded here through the production receipt
// service with the SLICE-1 Delivery.Origin carrier, which is the contract the
// raw path populates.
//
// FAILS until L3: neither renderer discloses the origin yet (expected L2
// failure — see the L2 leaf comment for the missing render contract).
func TestRawOriginReadBackDisclosedInListTextAndJSON(t *testing.T) {
	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	seedRawOriginOccurrence(t, dbPath)

	text := runLifecycleList(t, binary, dbPath, "text")
	require.Contains(t, text, "\torigin=raw\n",
		"list text output must disclose the raw origin marking")

	textJSON := runLifecycleList(t, binary, dbPath, "json")
	require.Contains(t, textJSON, `"origin":"raw"`,
		"list JSON output must disclose the raw origin member")
	var page struct {
		Items []struct {
			Origin string `json:"origin"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(textJSON), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, "raw", page.Items[0].Origin)
}

// TestNativeListReadBackGoldenBytesUnchanged pins the native ZERO-diff
// invariant on the PRODUCTION BINARY: a store holding only native captures
// read back through the built binary must render byte-identical list output in
// BOTH formats to the pre-M4 read surface. The golden files were captured from
// the SLICE-3 baseline binary (pre-origin rendering); they must match exactly.
func TestNativeListReadBackGoldenBytesUnchanged(t *testing.T) {
	dir := t.TempDir()
	binary := lifecycleBinary(t)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_222.json"))
	require.NoError(t, err)
	ingest := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	ingest.Stdin = bytes.NewReader(raw)
	require.NoError(t, ingest.Run())

	text := runLifecycleList(t, binary, dbPath, "text")
	require.NotContains(t, text, "origin=", "native text list must not render an origin clause")
	require.Equal(t, readGolden(t, "lifecycle_list_native_text.golden"), text,
		"native text list output must stay byte-identical to the pre-M4 read surface")

	textJSON := runLifecycleList(t, binary, dbPath, "json")
	require.NotContains(t, textJSON, `"origin"`, "native JSON list must not render an origin member")
	require.Equal(t, readGolden(t, "lifecycle_list_native_json.golden"), textJSON,
		"native JSON list output must stay byte-identical to the pre-M4 read surface")
}

func seedRawOriginOccurrence(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, readbackCLIClock{}, &readbackCLIOperations{}, timeouts.TestProfile())
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	delivery := claudeingress.Parse(raw, registration.ClaudeCode2_1_210().Events[0], "2.1.210", model.OccurrenceEnvelopeRef{}).Delivery
	delivery.Origin = acceptance.OriginRaw
	delivery.Envelope.Origin = acceptance.OriginRaw

	intent, refusal := gate.NewDeliveryIntent(delivery.Contract, delivery.Event)
	require.Nil(t, refusal, "delivery intent must legalize for the committed enabled event")
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal, "legalize delivery intent")
	_, err = service.Receive(ctx, warrant, delivery)
	require.NoError(t, err)
}

func runLifecycleList(t *testing.T, binary, dbPath, format string) string {
	t.Helper()
	args := []string{databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", format}
	command := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stdout.String()+stderr.String())
	require.Empty(t, stderr.String())
	return stdout.String()
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(body)
}

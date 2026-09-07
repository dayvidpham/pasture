package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// The fixture population is independent of activation. Removing a target or
// proof must not silently remove that event from the transport proof campaign.
func codexClearedFixtures(t *testing.T, root string) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "internal/lifecycle/ingress/codex/testdata/fixtures/*.provenance.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	fixtures := make(map[string]string)
	for _, path := range paths {
		var provenance acceptance.CaptureProvenance
		wire, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(wire, &provenance))
		fixture := strings.TrimSuffix(path, ".provenance.json") + ".json"
		raw, err := os.ReadFile(fixture)
		require.NoError(t, err)
		require.NoError(t, provenance.ValidateCommittedFixtureBytes(fixture, raw))
		require.Equal(t, acceptance.HarnessCodexCLI, provenance.Harness)
		require.Equal(t, registration.Codex0_153_0().Version, provenance.HarnessVersion)
		var payload struct {
			Event string `json:"hook_event_name"`
		}
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, provenance.Event, payload.Event)
		require.NotContains(t, fixtures, provenance.Event, "one cleared fixture per native event")
		fixtures[provenance.Event] = fixture
	}
	return fixtures
}

func TestCodexClearedEventsHaveRunnerProofClosure(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	fixtures := codexClearedFixtures(t, root)
	states, err := codexActivationByKind()
	require.NoError(t, err)
	manifest := registration.Codex0_153_0()
	require.Len(t, fixtures, len(manifest.Entries()), "fixture and registered populations must agree by event")
	for _, event := range manifest.Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			fixture, found := fixtures[event.NativeName]
			require.True(t, found, "registered event needs its own cleared fixture")
			state := states[event.Kind]
			require.Equal(t, activation.Enabled, state.State, "cleared event lacks production activation")
			require.True(t, state.IsValid())
			rel, err := filepath.Rel(root, fixture)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(state.CaptureProof.Name(), filepath.ToSlash(rel)+" ("), "capture proof must name this event's actual fixture")
			require.Equal(t, "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/"+event.NativeName, state.ProductionProof.Name())
		})
	}
}

func TestCodexPermissionConditionIsSourceBackedAndLocal(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	wire, err := renderCodexActivationReport()
	require.NoError(t, err)
	var report activationSupportReport
	require.NoError(t, json.Unmarshal([]byte(wire), &report))
	var rawReport struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire), &rawReport))
	seen := false
	for i, row := range report.Events {
		require.Equal(t, "none", row.ResponseCapability, row.Event)
		switch row.Event {
		case "SessionStart", "SubagentStart", "SessionEnd", "Interrupt":
			require.Empty(t, row.FailureEvidence, row.Event)
		default:
			require.Equal(t, "unset", row.FailureEvidence, "declared gate %s has no denial evidence", row.Event)
		}
		if row.Event != "PermissionRequest" {
			require.Empty(t, row.Condition, row.Event)
			require.Empty(t, row.ConditionSource, row.Event)
			require.NotContains(t, rawReport.Events[i], "condition", row.Event)
			require.NotContains(t, rawReport.Events[i], "conditionSource", row.Event)
			continue
		}
		seen = true
		require.Equal(t, "Emitted when the session uses Ask for approval; the captured account default Approve for me does not emit this event.", row.Condition)
		require.Equal(t, "internal/lifecycle/ingress/codex/testdata/fixtures/CLEARANCE.md#fixtures", row.ConditionSource)
		source, err := os.ReadFile(filepath.Join(root, strings.Split(row.ConditionSource, "#")[0]))
		require.NoError(t, err)
		require.Contains(t, string(source), `only when the session's permission profile is the host's "Ask for approval"`)
		require.Contains(t, string(source), `The account default is the host's "Approve for me"`)
	}
	require.True(t, seen, "PermissionRequest must have a report row")

	// Generic optional fields must not leak into other harness reports or into
	// native Codex configuration, which has its own host-owned schema.
	for _, path := range []string{"hooks/pasture-activation.json", ".opencode/pasture-opencode-activation.json", ".codex/hooks.json"} {
		body, err := os.ReadFile(filepath.Join(root, path))
		require.NoError(t, err)
		require.NotContains(t, string(body), `"condition"`, path)
		require.NotContains(t, string(body), `"conditionSource"`, path)
	}
	claude, _ := collectHarnessOutputs(t, root, ClaudeCodeTarget)
	assertGeneratedFilesCommitted(t, root, claude)
	openCode, _ := collectHarnessOutputs(t, root, OpenCodeTarget)
	assertGeneratedFilesCommitted(t, root, openCode)
}

// The manifest emitter also owns the package manifest and audit report. Count
// its actual file inventory, rather than treating an event count as a file count.
func TestCodexLifecycleManifestInventory(t *testing.T) {
	t.Parallel()
	root := testModuleRoot(t)
	files, err := (codexManifestEmitter{}).Emit(root, GenerateOptions{})
	require.NoError(t, err)
	want := []string{".codex/codex.toml", ".codex/hooks.json", ".codex/pasture-codex-activation.json"}
	for _, event := range registration.Codex0_153_0().Entries() {
		want = append(want, ".codex/hooks/events/"+event.NativeName+".sh")
	}
	var got []string
	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		require.NoError(t, err)
		got = append(got, filepath.ToSlash(rel))
		committed, err := os.ReadFile(file.Path)
		require.NoError(t, err)
		require.Equal(t, file.Content, string(committed), rel)
	}
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got, "native events and manifest-owned files are different populations")
}

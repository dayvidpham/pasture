package codex_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// TestClearedFixtureBytesMatchTheirSidecars holds every committed Codex
// fixture to the provenance sidecar beside it: the sidecar is the
// CaptureProvenance shape every cleared capture carries, its digest is the
// digest of the committed bytes, it names the native event, the recorded host
// version and the clearance record, and the redaction it lists parses. The
// corpus is the twelve events the capture sessions drove, one fixture per
// event; a thirteenth fixture here means the table below must grow
// deliberately.
func TestClearedFixtureBytesMatchTheirSidecars(t *testing.T) {
	t.Parallel()
	expected := map[string]string{
		"session_start_0_153_0.json":      "SessionStart",
		"pre_tool_use_0_153_0.json":       "PreToolUse",
		"user_prompt_submit_0_153_0.json": "UserPromptSubmit",
		"permission_request_0_153_0.json": "PermissionRequest",
		"post_tool_use_0_153_0.json":      "PostToolUse",
		"pre_compact_0_153_0.json":        "PreCompact",
		"post_compact_0_153_0.json":       "PostCompact",
		"subagent_start_0_153_0.json":     "SubagentStart",
		"subagent_stop_0_153_0.json":      "SubagentStop",
		"stop_0_153_0.json":               "Stop",
		"session_end_0_153_0.json":        "SessionEnd",
		"interrupt_0_153_0.json":          "Interrupt",
	}
	fixtures, err := filepath.Glob(filepath.Join(codexFixtureDir, "*.json"))
	require.NoError(t, err)
	var payloads []string
	for _, fixture := range fixtures {
		if filepath.Ext(fixture) == ".json" && !isSidecar(fixture) {
			payloads = append(payloads, filepath.Base(fixture))
		}
	}
	require.Len(t, payloads, len(expected), "the Codex corpus is exactly the captured events; %v", payloads)
	for _, file := range payloads {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			event, known := expected[file]
			require.True(t, known, "unexpected Codex fixture %s", file)
			raw, err := os.ReadFile(filepath.Join(codexFixtureDir, file))
			require.NoError(t, err)
			require.True(t, json.Valid(raw))
			sidecarBytes, err := os.ReadFile(filepath.Join(codexFixtureDir, file[:len(file)-len(".json")]+".provenance.json"))
			require.NoError(t, err)
			var members map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(sidecarBytes, &members))
			require.Len(t, members, 9, "the sidecar carries exactly the CaptureProvenance members")
			var sidecar acceptance.CaptureProvenance
			require.NoError(t, json.Unmarshal(sidecarBytes, &sidecar))
			require.Equal(t, acceptance.OriginAuthenticCapture, sidecar.Origin)
			require.Equal(t, acceptance.HarnessCodexCLI, sidecar.Harness)
			require.Equal(t, registration.Codex0_153_0().Version, sidecar.HarnessVersion)
			require.Equal(t, event, sidecar.Event)
			require.Equal(t, "sha256:"+sum(raw), sidecar.RawFileDigest, "the sidecar digest is the digest of the committed bytes")
			rules, err := acceptance.ParseRedaction(sidecar.Redaction)
			require.NoError(t, err)
			require.NotEmpty(t, rules)
			require.NoError(t, sidecar.ValidateFixture("testdata", "fixtures/"+file))
		})
	}
}

func isSidecar(path string) bool {
	const suffix = ".provenance.json"
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}

func sum(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

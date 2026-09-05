package codex_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// provenance mirrors the landed OpenCode provenance sidecar shape, adapted to
// Codex command-hook stdin (the raw bytes ARE the payload; there is no parent
// JSONL or nested callback value).
type provenance struct {
	Provider                string `json:"provider"`
	ObservedRuntimeVersion  string `json:"observedRuntimeVersion"`
	InspectedSourceRevision string `json:"inspectedSourceRevision"`
	CapturedAt              string `json:"capturedAt"`
	CaptureMethod           string `json:"captureMethod"`
	SourceSelector          string `json:"sourceSelector"`
	RawBytes                int    `json:"rawBytes"`
	RawSHA256               string `json:"rawSHA256"`
	Origin                  string `json:"origin"`
	Redaction               string `json:"redaction"`
}

// TestClearedFixtureBytesMatchPinnedDigests pins the two irreplaceable
// user-cleared Codex candidates to their exact sizes and SHA-256 digests, and
// verifies each provenance sidecar records the complete D4 chain of custody.
// Codex usage is exhausted; these two payloads are the entire authentic Codex
// evidence base, so this gate is authoritative for any later re-import.
func TestClearedFixtureBytesMatchPinnedDigests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, payload, provenanceFile, sourceSelector, capturedAt string
		size                                                      int
		sha256                                                    string
	}{
		{
			name:           "SessionStart",
			payload:        "session_start_0_146_0.json",
			provenanceFile: "session_start_0_146_0.provenance.json",
			sourceSelector: "1785755740434994199-2327240-SessionStart.json",
			capturedAt:     "2026-08-03T11:15:40.435Z",
			size:           291,
			sha256:         "69f56b0b3f98e7739828d64f1af6749931b750895eec433fa037600a623c7a04",
		},
		{
			name:           "PreToolUse",
			payload:        "pre_tool_use_0_146_0.json",
			provenanceFile: "pre_tool_use_0_146_0.provenance.json",
			sourceSelector: "1785755744328519447-2328094-PreToolUse.json",
			capturedAt:     "2026-08-03T11:15:44.328Z",
			size:           507,
			sha256:         "77ea0aa2a208418a2883db0cdb003e6fcf2c62856af515027dbe46270b7812e1",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("testdata/fixtures/" + tc.payload)
			require.NoError(t, err)
			require.Len(t, raw, tc.size, "cleared fixture byte size must be exact")
			require.Equal(t, tc.sha256, sum(raw), "cleared fixture digest must match the pinned authoritative value")

			provRaw, err := os.ReadFile("testdata/fixtures/" + tc.provenanceFile)
			require.NoError(t, err)
			var prov provenance
			require.NoError(t, json.Unmarshal(provRaw, &prov))
			require.Equal(t, "codex", prov.Provider)
			require.Equal(t, "0.146.0", prov.ObservedRuntimeVersion)
			require.Equal(t, "d6407d735942c7cfc996aa2bc7d0f97fc8f0e4bf", prov.InspectedSourceRevision)
			require.Equal(t, tc.capturedAt, prov.CapturedAt)
			require.Equal(t, "command hook exact stdin bytes", prov.CaptureMethod)
			require.Equal(t, tc.sourceSelector, prov.SourceSelector)
			require.Equal(t, tc.size, prov.RawBytes)
			require.Equal(t, tc.sha256, prov.RawSHA256, "provenance digest must equal the payload digest")
			require.Equal(t, "authentic-capture", prov.Origin)
			require.Equal(t, "none", prov.Redaction)
		})
	}
}

func sum(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

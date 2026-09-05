package opencode_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestAuthenticPayloadsPreserveBytesProvenanceAndIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file    string
		event         model.ContractEventKind
		identityCount int
	}{
		{"session created", "session_created_1_18_29.json", registration.EventOpenCodeSessionCreated, 1},
		{"tool execute before", "tool_execute_before_1_18_29.json", registration.EventOpenCodeToolExecuteBefore, 2},
	}
	manifest := registration.OpenCode1_18_29()
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// The fixture is the exact payload the host handed the plugin, as the
			// capture sink wrote it; its sidecar records the cleared digest.
			raw, err := os.ReadFile("testdata/fixtures/" + test.file)
			require.NoError(t, err)
			sidecarBytes, err := os.ReadFile("testdata/fixtures/" + strings.TrimSuffix(test.file, ".json") + ".provenance.json")
			require.NoError(t, err)
			var sidecar acceptance.CaptureProvenance
			require.NoError(t, json.Unmarshal(sidecarBytes, &sidecar))
			require.Equal(t, manifest.Version, sidecar.HarnessVersion, "captured at the recorded host version")
			require.NoError(t, sidecar.ValidateFixture("testdata", "fixtures/"+test.file))
			require.Equal(t, "sha256:"+sum(raw), sidecar.RawFileDigest)
			event := eventByKind(t, manifest, test.event)
			require.Equal(t, event.NativeName, sidecar.Event)
			capture := opencode.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			require.Equal(t, manifest.Contract, capture.Delivery.Contract)
			require.Equal(t, manifest.Version, capture.Delivery.Envelope.HostVersion)
			require.Len(t, capture.Delivery.Bindings, test.identityCount)
			require.Equal(t, json.RawMessage(raw), json.RawMessage(capture.Delivery.Body))
		})
	}
}

func TestCatalogIsSourceDerivedAndAuthenticProofIsSelected(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode1_18_29()
	// Two roots record the host version: the runtime contract id and the host
	// contract the manifest is generated from. They are one version.
	require.Equal(t, runtime.OpenCode1_18_29().Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	source := hostcontract.OpenCode1_18_29().Events
	require.NotEmpty(t, source, "the OpenCode host contract declares at least one event")
	require.Len(t, manifest.Events, len(source), "the generated manifest is the host contract's whole catalogue")
	proved := map[string]bool{"session.created": true, "tool.execute.before": true}
	proofCount := 0
	for _, event := range manifest.Events {
		if proved[event.NativeName] {
			proofCount++
			require.NotEmpty(t, event.Identities)
		} else {
			require.Empty(t, event.Identities, "source catalog metadata must not imply authentic runtime proof for %s", event.NativeName)
		}
	}
	require.Equal(t, 2, proofCount)
}

func eventByKind(t *testing.T, manifest registration.Manifest, kind model.ContractEventKind) registration.Event {
	t.Helper()
	for _, event := range manifest.Events {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("generated OpenCode manifest does not contain event kind %d", kind)
	return registration.Event{}
}

func sum(value []byte) string { digest := sha256.Sum256(value); return hex.EncodeToString(digest[:]) }

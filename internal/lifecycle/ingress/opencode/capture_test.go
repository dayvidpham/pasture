package opencode_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

type capturedRecord struct {
	Value json.RawMessage `json:"value"`
}

func TestAuthenticCallbackRecordsPreserveBytesProvenanceAndIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file, recordDigest, valueDigest string
		recordBytes, valueBytes               int
		event                                 model.ContractEventKind
		identityCount                         int
	}{
		{"session created", "session_created_1_18_10.capture.json", "a2f1f54e5ed59d048bcabb9d1a4b0a5b1f00f22b8906c429c0b1f1b04877a40f", "07b16ca0c5f9c8ea3948ac31e1509dd6d1d26cb93f5aa0c4456f04ce255f0cc1", 851, 775, registration.EventOpenCodeSessionCreated, 1},
		{"tool execute before", "tool_execute_before_1_18_10.capture.json", "c14a265118d84f679e5be424ed58772e16d4f5d0f2327433f18e860560027358", "b43f3ba21c6c42afd7c2f881da14430e0832600fe521eedd625c214ab9a125df", 367, 287, registration.EventOpenCodeToolExecuteBefore, 2},
	}
	manifest := registration.OpenCode1_18_10()
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("testdata/fixtures/" + test.file)
			require.NoError(t, err)
			require.Len(t, raw, test.recordBytes)
			require.Equal(t, test.recordDigest, sum(raw))
			var record capturedRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			require.Len(t, record.Value, test.valueBytes)
			require.Equal(t, test.valueDigest, sum(record.Value))
			event := eventByKind(t, manifest, test.event)
			capture := opencode.Parse(record.Value, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			require.Equal(t, manifest.Contract, capture.Delivery.Contract)
			require.Equal(t, manifest.Version, capture.Delivery.Envelope.HostVersion)
			require.Len(t, capture.Delivery.Bindings, test.identityCount)
			require.Equal(t, record.Value, json.RawMessage(capture.Delivery.Body))
		})
	}
}

func TestCatalogIsSourceDerivedAndAuthenticProofIsSelected(t *testing.T) {
	t.Parallel()
	manifest := registration.OpenCode1_18_10()
	// Two roots record the host version: the runtime contract id and the host
	// contract the manifest is generated from. They are one version.
	require.Equal(t, runtime.OpenCode1_18_10().Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	source := hostcontract.OpenCode1_18_10().Events
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

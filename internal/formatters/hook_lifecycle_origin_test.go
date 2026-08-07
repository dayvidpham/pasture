package formatters_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// TestHookLifecycleFormatterDisclosesRawOriginInText proves the M4 read-side
// disclosure for the TEXT renderer (UAT-Q3): an occurrence whose envelope
// carries the raw origin must render the visible "origin=raw" marking at the
// end of the list row. The native sentinel (empty origin) renders no marking
// at all, so native rows stay byte-identical to the pre-M4 read surface.
//
// FAILS until L3: the base hook_lifecycle text renderer has no origin column,
// so the raw-origin row renders without the marking (expected L2 failure - the
// missing-origin render contract is documented in the L2 leaf comment).
func TestHookLifecycleFormatterDisclosesRawOriginInText(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle().ID()
	record, err := model.NewLifecycleRecord(newOriginOccurrence(1, contract, origin.OriginRaw), nil)
	require.NoError(t, err)
	page := model.LifecyclePage{Items: []model.LifecycleRecord{record}}

	var text bytes.Buffer
	require.NoError(t, formatters.HookLifecycle(&text, page, "text"))
	require.Equal(t, text.String(),
		"1\t1\tregistration=claude-code/claude-code@2.1.210\tinterpreted=-\tmetamodel=-\torigin=raw\n",
		"text renderer must disclose the raw origin marking on the row")
}

// TestHookLifecycleFormatterDisclosesRawOriginInJSON proves the M4 read-side
// disclosure for the JSON renderer (UAT-Q3): an occurrence with the raw origin
// must serialize the typed "origin":"raw" member. Native (zero) origin keeps
// the member absent so pre-origin JSON stays byte-identical.
//
// FAILS until L3: the L1 record shape carries the omitempty member but the
// renderer does not yet populate it, so "origin" is absent (expected L2
// failure — see the L2 leaf comment).
func TestHookLifecycleFormatterDisclosesRawOriginInJSON(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle().ID()
	identities := []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "s"}}
	interpreted, err := model.NewInterpretedRecord(model.InterpretationID(10), model.OccurrenceID(1), runtime.SemanticObservation, identities, nil, contract)
	require.NoError(t, err)
	record, err := model.NewLifecycleRecord(newOriginOccurrence(1, contract, origin.OriginRaw), []model.InterpretedRecord{interpreted})
	require.NoError(t, err)
	page := model.LifecyclePage{Items: []model.LifecycleRecord{record}}

	var text bytes.Buffer
	require.NoError(t, formatters.HookLifecycle(&text, page, "json"))
	var decoded struct {
		Items []struct {
			Origin origin.CaptureOrigin `json:"origin"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(text.Bytes(), &decoded))
	require.Len(t, decoded.Items, 1)
	require.Equal(t, origin.OriginRaw, decoded.Items[0].Origin,
		"JSON renderer must disclose the raw origin member on the record")
	require.Contains(t, text.String(), `"origin":"raw"`,
		"JSON output must contain the exact raw origin disclosure")
}

// TestHookLifecycleFormatterNativeOriginGoldenBytes pins the native ZERO-diff
// invariant for BOTH renderers: an occurrence with the empty (native sentinel)
// origin must render byte-identical output to the pre-M4 read surface — the
// text row has no origin clause and the JSON record has no origin member. These
// literals are the exact bytes committed by the pre-M4 formatter (verified at
// the SLICE-3 baseline); any change to the native rendering is caught here.
func TestHookLifecycleFormatterNativeOriginGoldenBytes(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle().ID()
	identities := []waist.SemanticIdentity{{Kind: runtime.IdentitySession, Value: "s"}}
	interpreted, err := model.NewInterpretedRecord(model.InterpretationID(10), model.OccurrenceID(1), runtime.SemanticObservation, identities, nil, contract)
	require.NoError(t, err)
	record, err := model.NewLifecycleRecord(model.NewOccurrenceRecord(model.OccurrenceID(1), model.ContractEventKind(1), contract, model.OccurrenceEnvelopeRef{}, time.Unix(0, 0).UTC(), provenance.AgentID{}, nil, model.CaptureValid, model.EvidencePayloadRef{}), []model.InterpretedRecord{interpreted})
	require.NoError(t, err)
	page := model.LifecyclePage{Items: []model.LifecycleRecord{record}}

	var text, rawText bytes.Buffer
	require.NoError(t, formatters.HookLifecycle(&text, page, "text"))
	require.Equal(t,
		"1\t1\tregistration=claude-code/claude-code@2.1.210\tinterpreted=claude-code/claude-code@2.1.210\tmetamodel=unresolved (pre-M5)\n",
		text.String(), "native text row must stay byte-identical to the pre-M4 formatter")
	require.NotContains(t, text.String(), "origin=", "native text row must not render an origin clause")

	require.NoError(t, formatters.HookLifecycle(&rawText, page, "json"))
	var recordJSON struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rawText.Bytes(), &recordJSON))
	require.Len(t, recordJSON.Items, 1)
	require.NotContains(t, string(recordJSON.Items[0]), `"origin"`,
		"native JSON record must keep no origin member (byte-identical to pre-M4)")
}

func newOriginOccurrence(id int64, contract ir.RuntimeContractID, value origin.CaptureOrigin) model.OccurrenceRecord {
	envelope := model.OccurrenceEnvelopeRef{Origin: value}
	return model.NewOccurrenceRecord(model.OccurrenceID(id), model.ContractEventKind(1), contract, envelope, time.Unix(0, 0).UTC(), provenance.AgentID{}, nil, model.CaptureValid, model.EvidencePayloadRef{})
}

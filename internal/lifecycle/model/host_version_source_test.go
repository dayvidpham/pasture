package model_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/stretchr/testify/require"
)

func TestHostVersionSourceIsClosedButLegacyIsUnspecified(t *testing.T) {
	require.NoError(t, model.ValidateHostVersionSource(""))
	require.NoError(t, model.ValidateHostVersionSource(model.HostVersionCallerSupplied))
	require.NoError(t, model.ValidateHostVersionSource(model.HostVersionExecutableQuery))
	require.Error(t, model.ValidateHostVersionSource("running-process-attested"))
	for _, source := range []model.HostVersionSource{model.HostVersionCallerSupplied, model.HostVersionExecutableQuery} {
		envelope := model.OccurrenceEnvelopeRef{
			Runtime:     model.RuntimeContractDefinitionRef{Contract: registration.ClaudeCode2_1_261().Contract},
			HostVersion: "2.1.300", HostVersionSource: source,
		}
		raw, err := json.Marshal(envelope)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"hostVersionSource":"`+string(source)+`"`)
		var decoded model.OccurrenceEnvelopeRef
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, envelope, decoded)
	}
}

func TestUnspecifiedVersionSourcePreservesLegacyEnvelopeBytes(t *testing.T) {
	// The old wire shape is explicit, including field order and origin omission.
	// Do not derive this oracle from the new envelope's fields or JSON output.
	legacy := struct {
		Runtime        model.RuntimeContractDefinitionRef
		HostVersion    string
		Schema         model.LifecycleSchemaDefinitionRef
		Implementation model.EpochImplementationRef
		Retention      model.RetentionPolicyDefinitionRef
		Origin         origin.CaptureOrigin `json:"origin,omitempty"`
	}{Runtime: model.RuntimeContractDefinitionRef{Contract: registration.ClaudeCode2_1_261().Contract}, HostVersion: "2.1.261"}
	before, err := json.Marshal(legacy)
	require.NoError(t, err)
	after, err := json.Marshal(model.OccurrenceEnvelopeRef{Runtime: legacy.Runtime, HostVersion: "2.1.261"})
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	var decoded model.OccurrenceEnvelopeRef
	require.NoError(t, json.Unmarshal(before, &decoded))
	require.Empty(t, decoded.HostVersionSource, "legacy omission is not caller-supplied provenance")
}

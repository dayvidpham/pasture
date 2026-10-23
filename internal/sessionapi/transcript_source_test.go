package sessionapi

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/peasantexport"
)

func TestMapPeasantExportError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind peasantexport.Kind
		want error
	}{
		{name: "unavailable", kind: peasantexport.KindUnavailable, want: ErrTranscriptSourceUnavailable},
		{name: "not found", kind: peasantexport.KindNotFound, want: ErrTranscriptNotFound},
		{name: "incomplete", kind: peasantexport.KindIncomplete, want: ErrTranscriptIncomplete},
		{name: "unsupported", kind: peasantexport.KindUnsupported, want: ErrTranscriptUnsupported},
		{name: "malformed", kind: peasantexport.KindMalformed, want: ErrTranscriptMalformed},
		{name: "too large", kind: peasantexport.KindTooLarge, want: ErrTranscriptTooLarge},
		{name: "identity mismatch", kind: peasantexport.KindIdentityMismatch, want: ErrTranscriptIdentityMismatch},
		{name: "timed out", kind: peasantexport.KindTimedOut, want: ErrTranscriptTimedOut},
		{name: "export failed", kind: peasantexport.KindExportFailed, want: ErrTranscriptExportFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fault := &peasantexport.Fault{Kind: tc.kind, Message: "actionable detail", Err: errors.New("cause")}
			mapped := mapPeasantExportError(fault)
			require.ErrorIs(t, mapped, tc.want)
			require.Contains(t, mapped.Error(), "actionable detail")
			require.ErrorIs(t, mapped, fault.Err, "the underlying cause must remain inspectable")
		})
	}
}

func TestMapPeasantExportErrorDefaultsToExportFailed(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, mapPeasantExportError(errors.New("plain failure")), ErrTranscriptExportFailed)
}

func TestNewPeasantTranscriptSourceIsTheProductionAdapter(t *testing.T) {
	t.Parallel()
	source := newPeasantTranscriptSource()
	require.NotNil(t, source)
	require.NotNil(t, source.(*peasantSource).source)
}

func TestTranscriptExportCloseIsNilSafe(t *testing.T) {
	t.Parallel()
	var nilExport *TranscriptExport
	require.NoError(t, nilExport.Close())
	require.NoError(t, (&TranscriptExport{}).Close())

	called := false
	export := &TranscriptExport{CloseFunc: func() error {
		called = true
		return nil
	}}
	require.NoError(t, export.Close())
	require.True(t, called)
}

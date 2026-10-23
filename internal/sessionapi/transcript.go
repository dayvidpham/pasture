package sessionapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dayvidpham/pasture/internal/peasantexport"
)

// The transcript route proxies one durable Peasant transcript: the source
// package runs `peasant export sessions`, bounds the wait, and refuses output
// that names a different session. This file owns only the transport contract:
// the source interface, the session-API refusal vocabulary, and the adapter
// that maps Peasant's refusal kinds onto it.
//
// The proxy is deliberately server-side. A browser cannot reach Peasant's local
// API across origins, and Pasture must stay the only origin the console talks
// to.

// TranscriptSource exports one durable Peasant transcript. Production uses the
// Peasant CLI adapter; tests inject a fake so no test mutates PATH.
type TranscriptSource interface {
	ExportTranscript(ctx context.Context, harness, sessionID string) (*TranscriptExport, error)
}

// TranscriptExport is a validated durable transcript ready to stream. Content
// is the open export file; ServeContent owns the read loop, so the transcript
// is never buffered in the serve process. The handler must call Close.
type TranscriptExport struct {
	Content   io.ReadSeeker
	Size      int64
	ModTime   time.Time
	CloseFunc func() error
}

// Close releases the export file and its private directory. A nil CloseFunc is
// allowed so tests can wrap an in-memory reader.
func (e *TranscriptExport) Close() error {
	if e == nil || e.CloseFunc == nil {
		return nil
	}
	return e.CloseFunc()
}

// Transcript proxy errors. Each sentinel maps to one HTTP status in the
// handler, so the transport never has to inspect Peasant's text itself.
var (
	// ErrTranscriptSourceUnavailable reports that the peasant executable is
	// absent or could not be started.
	ErrTranscriptSourceUnavailable = errors.New("Peasant is unavailable")
	// ErrTranscriptNotFound reports that Peasant holds no session with that id.
	ErrTranscriptNotFound = errors.New("Peasant has no transcript for that session")
	// ErrTranscriptIncomplete reports that Peasant refused an incomplete newer
	// attempt rather than leaking either the attempt or the last-good content.
	ErrTranscriptIncomplete = errors.New("Peasant refused an incomplete latest attempt")
	// ErrTranscriptUnsupported reports that Peasant cannot serve this session:
	// it cannot read the store, cannot express the native id, or the export did
	// not record a harness Pasture can compare.
	ErrTranscriptUnsupported = errors.New("Peasant cannot serve this session")
	// ErrTranscriptMalformed reports that Peasant exited successfully but its
	// output was not one readable durable JSON document.
	ErrTranscriptMalformed = errors.New("Peasant returned a malformed transcript")
	// ErrTranscriptTooLarge reports that the export exceeds the proxy bound.
	ErrTranscriptTooLarge = errors.New("The Peasant export exceeds the proxy size limit")
	// ErrTranscriptIdentityMismatch reports that the payload names a different
	// session or harness than the caller requested.
	ErrTranscriptIdentityMismatch = errors.New("Peasant returned a different session identity")
	// ErrTranscriptTimedOut reports that the bounded wait for the export ended.
	ErrTranscriptTimedOut = errors.New("The bounded Peasant export wait ended")
	// ErrTranscriptExportFailed reports an unclassified Peasant failure.
	ErrTranscriptExportFailed = errors.New("Peasant could not export the transcript")
)

// peasantSource adapts the exporter's refusal kinds onto the session API's
// transport vocabulary. The exporter never decides HTTP status; this adapter
// never parses Peasant text.
type peasantSource struct {
	source *peasantexport.Source
}

// newPeasantTranscriptSource returns the production source wired into
// NewHandler: `peasant` on PATH under the exporter's bounded timeout.
func newPeasantTranscriptSource() TranscriptSource {
	return &peasantSource{source: peasantexport.NewSource()}
}

func (s *peasantSource) ExportTranscript(ctx context.Context, harness, sessionID string) (*TranscriptExport, error) {
	export, err := s.source.ExportTranscript(ctx, harness, sessionID)
	if err != nil {
		return nil, mapPeasantExportError(err)
	}
	return &TranscriptExport{
		Content:   export,
		Size:      export.Size,
		ModTime:   export.ModTime,
		CloseFunc: export.Close,
	}, nil
}

// mapPeasantExportError prefixes the actionable detail with the session API's
// refusal sentinel. Detail text from the exporter never repeats the sentinel.
func mapPeasantExportError(err error) error {
	switch peasantexport.KindOf(err) {
	case peasantexport.KindUnavailable:
		return fmt.Errorf("%w: %w", ErrTranscriptSourceUnavailable, err)
	case peasantexport.KindNotFound:
		return fmt.Errorf("%w: %w", ErrTranscriptNotFound, err)
	case peasantexport.KindIncomplete:
		return fmt.Errorf("%w: %w", ErrTranscriptIncomplete, err)
	case peasantexport.KindUnsupported:
		return fmt.Errorf("%w: %w", ErrTranscriptUnsupported, err)
	case peasantexport.KindMalformed:
		return fmt.Errorf("%w: %w", ErrTranscriptMalformed, err)
	case peasantexport.KindTooLarge:
		return fmt.Errorf("%w: %w", ErrTranscriptTooLarge, err)
	case peasantexport.KindIdentityMismatch:
		return fmt.Errorf("%w: %w", ErrTranscriptIdentityMismatch, err)
	case peasantexport.KindTimedOut:
		return fmt.Errorf("%w: %w", ErrTranscriptTimedOut, err)
	default:
		return fmt.Errorf("%w: %w", ErrTranscriptExportFailed, err)
	}
}

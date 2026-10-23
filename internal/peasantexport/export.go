// Package peasantexport runs the Peasant CLI to export one durable session
// transcript. It owns the subprocess boundary, the export-file lifecycle, and
// the refusal vocabulary. The session API owns routing, the shared-contract
// identity check, and the HTTP status mapping.
package peasantexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

// The exporter runs `peasant export sessions` beside the local API and hands
// back the durable SessionDetailPayload file unchanged. Peasant owns the
// transcript contract; this package only locates one session, bounds the wait,
// refuses output that names a different session than the caller asked for, and
// streams the file without loading it into memory.
//
// The proxy is deliberately server-side. A browser cannot reach Peasant's local
// API across origins, and Pasture must stay the only origin the console talks
// to. Peasant's CLI is invoked directly, never through a shell, so the session
// id in the URL cannot become shell syntax.

// MaxTranscriptBytes bounds one proxied export. The browser boundary
// (@peasant-labs/schema parseSessionDetailPayloadText) refuses documents above
// 128 MiB, so a larger export could not be rendered even if it were forwarded.
// Identity validation is a streaming JSON decode and the response is streamed
// with http.ServeContent, so this bound never becomes a per-request buffer.
const MaxTranscriptBytes = 128 << 20

// maxDiagnosticBytes bounds the stderr kept for classification and error text.
// A failing Peasant run must not be able to grow the serve process without
// bound.
const maxDiagnosticBytes = 64 << 10

// Kind names one refusal family. The session API maps each kind to its HTTP
// status; this package never decides status codes.
type Kind uint8

const (
	// KindUnavailable means the peasant executable is absent or not runnable.
	KindUnavailable Kind = iota + 1
	// KindNotFound means Peasant holds no session with that id.
	KindNotFound
	// KindIncomplete means Peasant refused an incomplete newer attempt rather
	// than leaking either the attempt or the last-good content.
	KindIncomplete
	// KindUnsupported means Peasant cannot serve this session: it cannot
	// express the native id, cannot read the store, or the export did not
	// record a harness Pasture can compare.
	KindUnsupported
	// KindMalformed means Peasant exited successfully but its output was not
	// one readable durable JSON document with a session identity.
	KindMalformed
	// KindTooLarge means the export exceeds MaxTranscriptBytes.
	KindTooLarge
	// KindIdentityMismatch means the payload names a different session or
	// harness than the caller requested.
	KindIdentityMismatch
	// KindTimedOut means the bounded wait for the export ended.
	KindTimedOut
	// KindExportFailed is an unclassified Peasant failure.
	KindExportFailed
)

// Fault is one classified export refusal. Message is the actionable detail the
// session API prefixes with its own sentinel; Err carries the underlying cause
// for errors.Is/As.
type Fault struct {
	Kind    Kind
	Message string
	Err     error
}

func (f *Fault) Error() string { return f.Message }

// Unwrap exposes the underlying cause without exposing Peasant internals as
// the public refusal.
func (f *Fault) Unwrap() error { return f.Err }

// KindOf reports the classification of err, defaulting to KindExportFailed for
// an unclassified failure.
func KindOf(err error) Kind {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault.Kind
	}
	return KindExportFailed
}

func fault(kind Kind, cause error, format string, args ...any) *Fault {
	return &Fault{Kind: kind, Message: fmt.Sprintf(format, args...), Err: cause}
}

// Source runs the Peasant CLI. Binary is the executable to invoke (empty means
// "peasant" on PATH). Timeout bounds one export even when the caller supplies
// no deadline; a caller deadline that is sooner still wins. Empty uses the
// shared timeout profile's outermost whole-operation tier.
type Source struct {
	Binary  string
	Timeout time.Duration
}

// NewSource returns the production source: `peasant` on PATH under the
// outermost timeout tier.
func NewSource() *Source {
	return &Source{Timeout: defaultTimeout()}
}

// defaultTimeout is the request budget for one export. It reuses the profile's
// outermost whole-operation wait so the exporter never invents a second timeout
// vocabulary for bounded external work.
func defaultTimeout() time.Duration {
	return timeouts.ProductionProfile().WorkflowResult()
}

// Export is the validated, still-open durable transcript file. The caller must
// Close it; Close also removes the private export directory. Export implements
// io.ReadSeeker so a handler can stream it with http.ServeContent.
type Export struct {
	File    *os.File
	Size    int64
	ModTime time.Time

	dir string
}

// Read implements io.Reader.
func (e *Export) Read(p []byte) (int, error) {
	if e == nil || e.File == nil {
		return 0, io.ErrClosedPipe
	}
	return e.File.Read(p)
}

// Seek implements io.Seeker.
func (e *Export) Seek(offset int64, whence int) (int64, error) {
	if e == nil || e.File == nil {
		return 0, io.ErrClosedPipe
	}
	return e.File.Seek(offset, whence)
}

// Close releases the transcript file and removes the private export directory.
// It is safe to call more than once.
func (e *Export) Close() error {
	if e == nil {
		return nil
	}
	var errs []error
	if e.File != nil {
		if err := e.File.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
		e.File = nil
	}
	if e.dir != "" {
		if err := os.RemoveAll(e.dir); err != nil {
			errs = append(errs, err)
		}
		e.dir = ""
	}
	return errors.Join(errs...)
}

// transcriptIdentity is the only part of the durable payload this package
// decodes. It exists to prove the export names the requested session; every
// other field stays opaque so Pasture never grows a second transcript contract.
type transcriptIdentity struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
}

// legacyHarnesses maps the historic Peasant harness spellings to the canonical
// shared-schema ids. The export reader normalizes before comparing, so a
// transcript recorded as `claude` still answers a `claude-code` request; a
// different canonical harness is never accepted.
var legacyHarnesses = map[string]string{
	"claude": "claude-code",
	"gemini": "gemini-cli",
}

// CanonicalHarness normalizes one Peasant harness spelling. Unknown values are
// returned unchanged so the caller can name the mismatch.
func CanonicalHarness(raw string) string {
	if canonical, ok := legacyHarnesses[raw]; ok {
		return canonical
	}
	return raw
}

// ExportTranscript exports one session through Peasant's CLI and returns the
// validated, open transcript file. The export directory is private (0700,
// guaranteed by os.MkdirTemp) and is removed by Export.Close.
func (s *Source) ExportTranscript(ctx context.Context, harness, sessionID string) (*Export, error) {
	binary := s.Binary
	if binary == "" {
		binary = "peasant"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout()
	}

	dir, err := os.MkdirTemp("", "pasture-transcript-*")
	if err != nil {
		return nil, fault(KindExportFailed, err, "create a private export directory: %v; check the temporary directory and its permissions, then retry", err)
	}
	// os.MkdirTemp creates the directory with mode 0700, so Peasant's 0644
	// export file is not reachable by another user through the directory.
	handedOff := false
	defer func() {
		if !handedOff {
			_ = os.RemoveAll(dir)
		}
	}()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binary, "export", "sessions", "--session", sessionID, "--output-dir", dir)
	stderr := &boundedBuffer{limit: maxDiagnosticBytes}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return nil, fault(KindTimedOut, ctxErr,
			"after %s while exporting session %q: %v; Peasant was stopped so no partial transcript is served, retry or export it directly with `peasant export sessions --session %s`",
			timeout, sessionID, ctxErr, sessionID)
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			return nil, fault(KindUnavailable, runErr,
				"%q was not found: %v; install Peasant and ensure it is on PATH for `pasture serve`, then retry", binary, runErr)
		}
		if errors.Is(runErr, fs.ErrPermission) {
			return nil, fault(KindUnavailable, runErr,
				"%q could not be executed: %v; make the Peasant binary executable by the user running `pasture serve`, then retry", binary, runErr)
		}
		return nil, classifyPeasantFailure(sessionID, stderr.String(), runErr)
	}

	export, err := openExport(dir, harness, sessionID)
	if err != nil {
		return nil, err
	}
	handedOff = true
	return export, nil
}

// classifyPeasantFailure maps Peasant's documented CLI refusal text to a Kind.
// Peasant export exits nonzero for every refusal and reports the reason on
// stderr; it has no machine-readable failure code yet.
func classifyPeasantFailure(sessionID, stderr string, runErr error) *Fault {
	detail := summarizeDiagnostic(stderr)
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "session not found"):
		return fault(KindNotFound, runErr,
			"session %q is not in the Peasant store (%s); capture it with Peasant, then retry", sessionID, detail)
	case strings.Contains(lower, "incomplete latest attempt"):
		return fault(KindIncomplete, runErr,
			"session %q holds a newer incomplete capture beside its last-good transcript and Peasant refuses to serve either as the session (%s); finish or re-run the capture in Peasant, or export the last-good transcript explicitly with `peasant export sessions --last-good`, then retry",
			sessionID, detail)
	case strings.Contains(lower, "invalid session id"):
		return fault(KindUnsupported, runErr,
			"Peasant rejected the native session id %q (%s); Peasant supports UUID, agent-{hex}, ses_{id}, sess_{id} (ACP), msg_{id}, and Strike session ids, so retain the host form and export it from Peasant directly",
			sessionID, detail)
	case strings.Contains(lower, "database schema is version") ||
		strings.Contains(lower, "upgraded by a newer peasant") ||
		strings.Contains(lower, "understands only up to version") ||
		strings.Contains(lower, "install that version or newer"):
		return fault(KindUnsupported, runErr,
			"the installed Peasant cannot read its store (%s); install the Peasant version that wrote the store (or newer) and retry", detail)
	default:
		return fault(KindExportFailed, runErr,
			"session %q could not be exported: %v (%s); run `peasant export sessions --session %s --output-dir <dir>` directly to see the full diagnostic",
			sessionID, runErr, detail, sessionID)
	}
}

// openExport locates the single JSON document Peasant wrote, bounds its size,
// and proves it names the requested session and harness. The returned file is
// positioned at the start of the document.
func openExport(dir, harness, sessionID string) (*Export, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fault(KindMalformed, err, "read the export directory %q: %v; the transcript was not exported", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	switch len(names) {
	case 0:
		return nil, fault(KindMalformed, nil, "Peasant exited successfully but wrote no JSON transcript for session %q; export it directly with `peasant export sessions` to inspect the store", sessionID)
	case 1:
		// continue below
	default:
		return nil, fault(KindMalformed, nil, "Peasant wrote %d JSON files for one requested session %q; the exporter cannot tell which document is the transcript", len(names), sessionID)
	}

	path := filepath.Join(dir, names[0])
	file, err := os.Open(path)
	if err != nil {
		return nil, fault(KindMalformed, err, "open the exported transcript %q: %v", path, err)
	}
	cleanup := func(err error) (*Export, error) {
		_ = file.Close()
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		return cleanup(fault(KindMalformed, err, "stat the exported transcript %q: %v", path, err))
	}
	if info.Size() == 0 {
		return cleanup(fault(KindMalformed, nil, "the exported transcript for session %q is empty", sessionID))
	}
	if info.Size() > MaxTranscriptBytes {
		return cleanup(fault(KindTooLarge, nil, "the export is %d bytes, above the %d-byte limit; export it directly from Peasant and trim the session before retrying", info.Size(), MaxTranscriptBytes))
	}
	if err := verifyIdentity(file, harness, sessionID); err != nil {
		return cleanup(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return cleanup(fault(KindMalformed, err, "rewind the exported transcript %q: %v", path, err))
	}
	return &Export{File: file, Size: info.Size(), ModTime: info.ModTime(), dir: dir}, nil
}

// verifyIdentity decodes the durable payload as a stream and compares its
// session identity to the request. It never buffers the whole document.
func verifyIdentity(r io.Reader, harness, sessionID string) error {
	decoder := json.NewDecoder(r)
	var identity transcriptIdentity
	if err := decoder.Decode(&identity); err != nil {
		return fault(KindMalformed, err, "the exported transcript for session %q is not one complete JSON document: %v; re-run the Peasant export", sessionID, err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		// A valid trailing token returns a nil error, so name the token and its
		// offset rather than printing "<nil>". A malformed tail keeps its parse
		// error instead.
		detail := fmt.Sprintf("token %v at offset %d", token, decoder.InputOffset())
		if err != nil {
			detail = err.Error()
		}
		return fault(KindMalformed, err, "the exported transcript for session %q carries content after the durable document (%s); the exporter refuses to serve an envelope it cannot interpret", sessionID, detail)
	}
	switch {
	case identity.ID == "" && identity.Harness == "":
		return fault(KindMalformed, nil, "the exported document for session %q carries no id and harness, so Pasture cannot prove which session it describes; export it directly with `peasant export sessions` and check the Peasant version", sessionID)
	case identity.ID == "":
		return fault(KindMalformed, nil, "the exported document for session %q carries no session id; export it directly with `peasant export sessions` and check the Peasant version", sessionID)
	case identity.Harness == "":
		return fault(KindUnsupported, nil, "the export did not record a harness for session %q, so Pasture cannot verify the transcript's harness identity; re-harvest the session in Peasant so it records a harness, then retry", sessionID)
	}

	got := CanonicalHarness(identity.Harness)
	if identity.ID != sessionID || got != harness {
		legacy := ""
		if got != identity.Harness {
			legacy = fmt.Sprintf(" normalized from legacy %q", identity.Harness)
		}
		return fault(KindIdentityMismatch, nil,
			"Pasture asked Peasant for harness %q session %q but the export names harness %q%s session %q; verify that the Peasant store and Pasture's session inventory describe the same session before retrying",
			harness, sessionID, got, legacy, identity.ID)
	}
	return nil
}

// summarizeDiagnostic renders Peasant's bounded stderr as one actionable
// sentence. It keeps the first non-empty line because that line carries the
// refusal reason; the "Usage:" tail is boilerplate. Truncation lands on a
// UTF-8 boundary so a non-ASCII path never becomes invalid text.
func summarizeDiagnostic(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return truncateUTF8(line, 480)
	}
	return "no diagnostic was printed"
}

func truncateUTF8(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := max - 3
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "..."
}

// boundedBuffer keeps the first limit bytes written and silently drops the
// rest. CombinedOutput-style unbounded buffers would let a hostile or broken
// Peasant grow the serve process without bound.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buf.Write(chunk)
	}
	return written, nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

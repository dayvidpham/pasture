package sessionapi

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

	"github.com/dayvidpham/pasture/internal/timeouts"
)

// The transcript proxy runs `peasant export sessions` beside the local API and
// returns the durable SessionDetailPayload bytes unchanged. Peasant owns the
// transcript contract; Pasture only locates one session, bounds the wait, and
// refuses output that names a different session than the caller asked for.
//
// The proxy is deliberately server-side. A browser cannot reach Peasant's local
// API across origins, and Pasture must stay the only origin the console talks
// to. Peasant's CLI is invoked directly, never through a shell, so the session
// id in the URL cannot become shell syntax.

// maxTranscriptBytes bounds one proxied export. The browser boundary
// (@peasant-labs/schema parseSessionDetailPayloadText) refuses documents above
// 128 MiB, so a larger export could not be rendered even if it were forwarded.
const maxTranscriptBytes = 128 << 20

// maxPeasantDiagnosticBytes bounds the stderr Pasture keeps for classification
// and error text. A failing Peasant run must not be able to grow the proxy's
// memory without bound.
const maxPeasantDiagnosticBytes = 64 << 10

// TranscriptSource exports one durable Peasant transcript. Production uses
// PeasantTranscriptSource; tests inject a fake so no test mutates PATH.
type TranscriptSource interface {
	ExportTranscript(ctx context.Context, harness, sessionID string) ([]byte, error)
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
	// ErrTranscriptUnsupported reports that the installed Peasant cannot read
	// the store it was pointed at, or cannot express the requested session id.
	ErrTranscriptUnsupported = errors.New("Peasant cannot serve this session")
	// ErrTranscriptMalformed reports that Peasant exited successfully but its
	// output was not one readable durable JSON document.
	ErrTranscriptMalformed = errors.New("Peasant returned a malformed transcript")
	// ErrTranscriptTooLarge reports that the export exceeds maxTranscriptBytes.
	ErrTranscriptTooLarge = errors.New("The Peasant export exceeds the proxy size limit")
	// ErrTranscriptIdentityMismatch reports that the payload names a different
	// session or harness than the caller requested.
	ErrTranscriptIdentityMismatch = errors.New("Peasant returned a different session identity")
	// ErrTranscriptTimedOut reports that the bounded wait for the export ended.
	ErrTranscriptTimedOut = errors.New("The bounded Peasant export wait ended")
	// ErrTranscriptExportFailed reports an unclassified Peasant failure.
	ErrTranscriptExportFailed = errors.New("Peasant could not export the transcript")
)

// PeasantTranscriptSource runs the Peasant CLI. Binary is the executable to
// invoke (empty means "peasant" on PATH). Timeout bounds one export even when
// the caller supplies no deadline; a caller deadline that is sooner still wins.
// Empty uses the shared timeout profile's outermost whole-operation tier.
type PeasantTranscriptSource struct {
	Binary  string
	Timeout time.Duration
}

// NewPeasantTranscriptSource returns the production source: `peasant` on PATH
// under the outermost timeout tier.
func NewPeasantTranscriptSource() *PeasantTranscriptSource {
	return &PeasantTranscriptSource{Timeout: transcriptExportTimeout()}
}

// transcriptExportTimeout is the request budget for one export. It reuses the
// profile's outermost whole-operation wait so the proxy never invents a second
// timeout vocabulary for bounded external work.
func transcriptExportTimeout() time.Duration {
	return timeouts.ProductionProfile().WorkflowResult()
}

// transcriptIdentity is the only part of the durable payload Pasture decodes.
// It exists to prove the export names the requested session; every other field
// stays opaque so Pasture never grows a second transcript contract.
type transcriptIdentity struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
}

// ExportTranscript exports one session through Peasant's CLI and returns the
// durable payload bytes. The export directory is private (0700) and removed
// before returning, including on failure.
func (s *PeasantTranscriptSource) ExportTranscript(ctx context.Context, harness, sessionID string) ([]byte, error) {
	binary := s.Binary
	if binary == "" {
		binary = "peasant"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = transcriptExportTimeout()
	}

	dir, err := os.MkdirTemp("", "pasture-transcript-*")
	if err != nil {
		return nil, fmt.Errorf("%w: create a private export directory: %v; check the temporary directory and its permissions, then retry", ErrTranscriptExportFailed, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: restrict the export directory %q to 0700: %v; the transcript was not exported", ErrTranscriptExportFailed, dir, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binary, "export", "sessions", "--session", sessionID, "--output-dir", dir)
	stderr := &boundedBuffer{limit: maxPeasantDiagnosticBytes}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%w after %s while exporting session %q: %v; Peasant was stopped so no partial transcript is served, retry or export it directly with `peasant export sessions --session %s`", ErrTranscriptTimedOut, timeout, sessionID, ctxErr, sessionID)
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q was not found: %v; install Peasant and ensure it is on PATH for `pasture serve`, then retry", ErrTranscriptSourceUnavailable, binary, runErr)
		}
		if errors.Is(runErr, fs.ErrPermission) {
			return nil, fmt.Errorf("%w: %q could not be executed: %v; make the Peasant binary executable by the user running `pasture serve`, then retry", ErrTranscriptSourceUnavailable, binary, runErr)
		}
		return nil, classifyPeasantFailure(sessionID, stderr.String(), runErr)
	}

	data, err := readExportedTranscript(dir, harness, sessionID)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// classifyPeasantFailure maps Peasant's documented CLI refusal text to the
// proxy's sentinels. Peasant export exits nonzero for every refusal and reports
// the reason on stderr; it has no machine-readable failure code yet.
func classifyPeasantFailure(sessionID, stderr string, runErr error) error {
	detail := summarizeDiagnostic(stderr)
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "session not found"):
		return fmt.Errorf("%w: session %q is not in the Peasant store (%s); capture it with Peasant, then retry", ErrTranscriptNotFound, sessionID, detail)
	case strings.Contains(lower, "incomplete latest attempt"):
		return fmt.Errorf("%w for session %q: a newer incomplete capture stands beside the last-good transcript and Peasant refuses to serve either as the session (%s); finish or re-run the capture in Peasant, or export the last-good transcript explicitly with `peasant export sessions --last-good`, then retry", ErrTranscriptIncomplete, sessionID, detail)
	case strings.Contains(lower, "invalid session id"):
		return fmt.Errorf("%w: Peasant rejected the native session id %q (%s); Peasant supports UUID, agent-{hex}, ses_{id}, sess_{id} (ACP), msg_{id}, and Strike session ids, so retain the host form and export it from Peasant directly", ErrTranscriptUnsupported, sessionID, detail)
	case strings.Contains(lower, "database schema is version") ||
		strings.Contains(lower, "upgraded by a newer peasant") ||
		strings.Contains(lower, "understands only up to version") ||
		strings.Contains(lower, "install that version or newer"):
		return fmt.Errorf("%w: the installed Peasant cannot read its store (%s); install the Peasant version that wrote the store (or newer) and retry", ErrTranscriptUnsupported, detail)
	default:
		return fmt.Errorf("%w for session %q: %v (%s); run `peasant export sessions --session %s --output-dir <dir>` directly to see the full diagnostic", ErrTranscriptExportFailed, sessionID, runErr, detail, sessionID)
	}
}

// readExportedTranscript reads the single JSON document Peasant wrote and
// proves it names the requested session and harness.
//
// In the inspected Peasant CLI, `export sessions` writes the flat
// SessionDetailPayload itself at <output-dir>/<session-id>.json; there is no
// outer export envelope to unwrap. A document without the required `id` and
// `harness` members is treated as a malformed payload rather than forwarded.
func readExportedTranscript(dir, harness, sessionID string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: read the export directory %q: %v; the transcript was not exported", ErrTranscriptMalformed, dir, err)
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
		return nil, fmt.Errorf("%w: Peasant exited successfully but wrote no JSON transcript for session %q; export it directly with `peasant export sessions` to inspect the store", ErrTranscriptMalformed, sessionID)
	case 1:
		// continue below
	default:
		return nil, fmt.Errorf("%w: Peasant wrote %d JSON files for one requested session %q; the proxy cannot tell which document is the transcript", ErrTranscriptMalformed, len(names), sessionID)
	}

	path := filepath.Join(dir, names[0])
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: stat the exported transcript %q: %v", ErrTranscriptMalformed, path, err)
	}
	if info.Size() > maxTranscriptBytes {
		return nil, fmt.Errorf("%w: the export is %d bytes, above the %d-byte limit; export it directly from Peasant and trim the session before retrying", ErrTranscriptTooLarge, info.Size(), maxTranscriptBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read the exported transcript %q: %v", ErrTranscriptMalformed, path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: the exported transcript for session %q is empty", ErrTranscriptMalformed, sessionID)
	}

	var identity transcriptIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("%w: the exported transcript for session %q is not one complete JSON document: %v; re-run the Peasant export", ErrTranscriptMalformed, sessionID, err)
	}
	if identity.ID == "" || identity.Harness == "" {
		return nil, fmt.Errorf("%w: the exported document for session %q carries no id and harness, so Pasture cannot prove which session it describes; export it directly with `peasant export sessions` and check the Peasant version", ErrTranscriptMalformed, sessionID)
	}
	if identity.ID != sessionID || identity.Harness != harness {
		return nil, fmt.Errorf("%w: Pasture asked Peasant for harness %q session %q but the export names harness %q session %q; verify that the Peasant store and Pasture's session inventory describe the same session before retrying", ErrTranscriptIdentityMismatch, harness, sessionID, identity.Harness, identity.ID)
	}
	return data, nil
}

// summarizeDiagnostic renders Peasant's bounded stderr as one actionable
// sentence. It keeps the first non-empty line because that line carries the
// refusal reason; the "Usage:" tail is boilerplate.
func summarizeDiagnostic(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 480 {
			line = line[:477] + "..."
		}
		return line
	}
	return "no diagnostic was printed"
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

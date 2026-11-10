package peasantexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sessionListLimit is the most session ids one catalog read asks Peasant to
// print. A larger page does not finish inside the outermost whole-operation
// tier: measured 2026-11-10, --limit 200 returned in under 12s and --limit 500
// took about 35s. A result of this many ids may be only the first page, so the
// catalog says it is truncated instead of treating every later session as
// having no transcript.
const sessionListLimit = 200

// sessionListCacheTTL is how long one successful id list is reused. It is a
// process-local reuse window, not a cancellation budget: the subprocess wait
// still comes from the timeout profile.
const sessionListCacheTTL = 30 * time.Second

// maxSessionListBytes bounds the JSON Peasant may print for one list. A larger
// document is refused so the serve process cannot grow without bound, and so
// the console shows the catalog as unavailable rather than guessing from a
// partial document.
const maxSessionListBytes = 4 << 20

// sessionListCache remembers one successful catalog. Failures are not cached:
// a missing binary or a malformed document must be retried on the next read.
type sessionListCache struct {
	mu        sync.Mutex
	ids       []string
	subagents []string
	truncated bool
	at        time.Time
	valid     bool
}

// SessionList is one bounded Peasant session-id catalog. IDs are the durable
// transcript ids Peasant printed. Subagents is the subset of those ids whose
// parentSessionId is non-empty; a missing or blank parent is a root. On a
// truncated page, Subagents contains only the parented ids that page printed,
// not every subagent in the store. Truncated is true when that print filled
// the requested page, so an id that is absent is not evidence that Peasant
// has no transcript for it.
type SessionList struct {
	IDs       []string
	Subagents []string
	Truncated bool
}

// ListSessions runs `peasant sessions list --json` and returns the id fields.
// The page is bounded by sessionListLimit (0 means no limit in Peasant, which
// does not finish inside the timeout on a large store) and includes subagent
// sessions, because export can still serve those ids. A successful result,
// including an empty page, is reused for sessionListCacheTTL so a console
// refresh does not spawn a process per request. The command is not a shell, so
// the fixed arguments cannot become shell syntax.
func (s *Source) ListSessions(ctx context.Context) (SessionList, error) {
	if s == nil {
		return SessionList{}, fault(KindUnavailable, nil, "no Peasant source is configured for the session catalog; restart `pasture serve` with a source, then retry")
	}
	now := time.Now()
	if cached, ok := s.listCache.get(now); ok {
		return cached, nil
	}
	listed, err := s.fetchSessionList(ctx)
	if err != nil {
		return SessionList{}, err
	}
	s.listCache.put(listed, time.Now())
	return listed, nil
}

func (s *Source) sessionLimit() int {
	if s != nil && s.listLimit > 0 {
		return s.listLimit
	}
	return sessionListLimit
}

func (c *sessionListCache) get(now time.Time) (SessionList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || now.Sub(c.at) >= sessionListCacheTTL {
		return SessionList{}, false
	}
	return SessionList{IDs: cloneIDs(c.ids), Subagents: cloneIDs(c.subagents), Truncated: c.truncated}, true
}

func (c *sessionListCache) put(listed SessionList, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = cloneIDs(listed.IDs)
	c.subagents = cloneIDs(listed.Subagents)
	c.truncated = listed.Truncated
	c.at = at
	c.valid = true
}

func cloneIDs(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

func (s *Source) fetchSessionList(ctx context.Context) (SessionList, error) {
	binary := s.Binary
	if binary == "" {
		binary = "peasant"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout()
	}
	limit := s.sessionLimit()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binary, "sessions", "list", "--json", "--limit", strconv.Itoa(limit), "--include-subagents")
	stdout := &boundedBuffer{limit: maxSessionListBytes + 1}
	stderr := &boundedBuffer{limit: maxDiagnosticBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return SessionList{}, fault(KindTimedOut, ctxErr,
			"after %s while listing Peasant sessions: %v; Peasant was stopped so no partial catalog is served, retry or list it directly with `peasant sessions list --json --limit %d`",
			timeout, ctxErr, limit)
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			return SessionList{}, fault(KindUnavailable, runErr,
				"%q was not found: %v; install Peasant and ensure it is on PATH for `pasture serve`, then retry", binary, runErr)
		}
		if errors.Is(runErr, fs.ErrPermission) {
			return SessionList{}, fault(KindUnavailable, runErr,
				"%q could not be executed: %v; make the Peasant binary executable by the user running `pasture serve`, then retry", binary, runErr)
		}
		return SessionList{}, classifySessionListFailure(stderr.String(), runErr)
	}
	if stdout.buf.Len() > maxSessionListBytes {
		return SessionList{}, fault(KindTooLarge, nil,
			"Peasant printed more than %d bytes for the session catalog; the catalog is refused so a partial list is not treated as complete, retry with `peasant sessions list --json --limit %d`",
			maxSessionListBytes, limit)
	}
	listed, err := parseSessionListIDs(stdout.buf.Bytes(), limit)
	if err != nil {
		return SessionList{}, err
	}
	return listed, nil
}

// classifySessionListFailure maps a nonzero `peasant sessions list` exit to a
// Kind. The list command reports schema and store refusals on stderr, the same
// way export does, and has no machine-readable failure code.
func classifySessionListFailure(stderr string, runErr error) *Fault {
	detail := summarizeDiagnostic(stderr)
	lower := bytes.ToLower([]byte(stderr))
	switch {
	case bytes.Contains(lower, []byte("database schema is version")) ||
		bytes.Contains(lower, []byte("upgraded by a newer peasant")) ||
		bytes.Contains(lower, []byte("understands only up to version")) ||
		bytes.Contains(lower, []byte("install that version or newer")):
		return fault(KindUnsupported, runErr,
			"the installed Peasant cannot read its store (%s); install the Peasant version that wrote the store (or newer) and retry", detail)
	default:
		return fault(KindExportFailed, runErr,
			"Peasant could not list sessions: %v (%s); run `peasant sessions list --json` directly to see the full diagnostic",
			runErr, detail)
	}
}

type sessionListRow struct {
	ID              string `json:"id"`
	ParentSessionID string `json:"parentSessionId"`
}

// parseSessionListIDs reads one JSON array and keeps each object's id. A full
// page (len == limit) is truncated: Peasant was asked for at most that many,
// so a shorter store would have returned fewer. Duplicate ids are dropped.
// An entry with no id fails the whole document, because skipping it would mark
// that session as having no transcript.
func parseSessionListIDs(data []byte, limit int) (SessionList, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var rows []sessionListRow
	if err := decoder.Decode(&rows); err != nil {
		return SessionList{}, fault(KindMalformed, err,
			"Peasant session list is not one JSON array of session objects: %v; run `peasant sessions list --json` and check that Peasant is the version `pasture serve` expects", err)
	}
	if rows == nil {
		return SessionList{}, fault(KindMalformed, nil,
			"Peasant printed null instead of a JSON array of sessions; run `peasant sessions list --json` and check the Peasant version")
	}
	if token, err := decoder.Token(); err != io.EOF {
		detail := fmt.Sprintf("token %v at offset %d", token, decoder.InputOffset())
		if err != nil {
			detail = err.Error()
		}
		return SessionList{}, fault(KindMalformed, err,
			"Peasant session list carries content after the JSON array (%s); the catalog refuses a document it cannot interpret", detail)
	}
	if limit > 0 && len(rows) > limit {
		return SessionList{}, fault(KindTooLarge, nil,
			"Peasant printed %d sessions after being asked for at most %d; the catalog refuses the document so it does not drop ids and call the rest complete",
			len(rows), limit)
	}
	seen := make(map[string]struct{}, len(rows))
	ids := make([]string, 0, len(rows))
	subagents := make([]string, 0)
	for i, row := range rows {
		if row.ID == "" {
			return SessionList{}, fault(KindMalformed, nil,
				"Peasant session list entry %d has no id, so the catalog cannot tell which session it describes; run `peasant sessions list --json` and check the Peasant version", i)
		}
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		ids = append(ids, row.ID)
		if strings.TrimSpace(row.ParentSessionID) != "" {
			subagents = append(subagents, row.ID)
		}
	}
	return SessionList{IDs: ids, Subagents: subagents, Truncated: limit > 0 && len(rows) == limit}, nil
}

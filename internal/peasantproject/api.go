package peasantproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peasant-labs/schema"
)

const (
	// apiLookupTimeout bounds one session-summaries request. The integration is
	// optional; a slow Peasant must not hold a Pasture session read open. It
	// sits below the session API's 2s enrichment bound so a timeout is
	// attributable to the source (and therefore cacheable) rather than to the
	// caller's own deadline.
	apiLookupTimeout = 1500 * time.Millisecond
	// maxAPIBodyBytes bounds the response body read. One page of summaries is a
	// few kilobytes; this is a corruption/abuse backstop.
	maxAPIBodyBytes = 1 << 20
	// apiNotContactedMessage is the initial state of a configured API source.
	// Peasant is reported unavailable until a lookup actually succeeds, so
	// "configured" is never mistaken for "answering".
	apiNotContactedMessage = "the Peasant API has not been contacted yet"
)

// apiSummary is one Peasant session summary. Peasant owns these fields; Pasture
// reads only the identity and project pair.
type apiSummary struct {
	ID          string `json:"id"`
	Harness     string `json:"harness"`
	Project     string `json:"project"`
	ProjectHash string `json:"projectHash"`
}

// apiEnvelope is the documented response shape of
// GET /api/v1/session-summaries.
type apiEnvelope struct {
	Sessions []apiSummary `json:"sessions"`
}

// apiSource reads project mappings from the Peasant HTTP API.
type apiSource struct {
	endpoint string
	client   *http.Client

	status statusHolder
}

// NewAPISource validates and constructs the HTTP-backed source. A malformed URL
// is a configuration error the operator can fix at startup; runtime failures
// become unavailable status instead. A constructed source starts unavailable
// until its first successful lookup.
func NewAPISource(rawURL string) (Source, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return nil, fmt.Errorf("the Peasant API URL is empty; pass --peasant-url http://127.0.0.1:8787")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("the Peasant API URL %q is not an http(s) URL with a host; pass --peasant-url http://127.0.0.1:8787", rawURL)
	}
	source := &apiSource{
		endpoint: trimmed,
		client:   &http.Client{Timeout: apiLookupTimeout},
		status:   newStatusHolder(SourceAPI),
	}
	source.status.set(StateUnavailable, apiNotContactedMessage)
	return source, nil
}

func (s *apiSource) Status() Status { return s.status.get() }

// Lookup asks Peasant for one bounded batch of session summaries. A returned
// summary applies only when its (harness, id) is one of the requested keys and
// its project hash validates; anything else is skipped, never guessed.
func (s *apiSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	out := make(map[Key]Project, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requested := make(map[Key]struct{}, len(keys))
	ids := make([]string, 0, len(keys))
	seenIDs := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		requested[key] = struct{}{}
		if _, ok := seenIDs[key.Session]; ok {
			continue
		}
		seenIDs[key.Session] = struct{}{}
		ids = append(ids, key.Session)
	}
	if len(requested) > maxLookupKeys {
		return nil, fmt.Errorf("the Peasant project lookup requested %d sessions, above the %d-session bound; request one page at a time", len(requested), maxLookupKeys)
	}
	query := url.Values{}
	query.Set("ids", strings.Join(ids, ","))
	requestURL := s.endpoint + "/api/v1/session-summaries?" + query.Encode()

	lookupCtx, cancel := context.WithTimeout(ctx, apiLookupTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		s.status.markFailure(ctx, "the Peasant session-summaries request could not be built: "+err.Error())
		return nil, fmt.Errorf("building the Peasant session-summaries request: %w", err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		s.status.markFailure(ctx, "the Peasant API is unreachable: "+err.Error())
		return nil, apiLookupError("reaching the Peasant API", lookupCtx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.status.markFailure(ctx, fmt.Sprintf("the Peasant API answered HTTP %d for session summaries", response.StatusCode))
		return nil, fmt.Errorf("the Peasant API answered HTTP %d for session summaries", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBodyBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		s.status.markFailure(ctx, "the Peasant session-summaries response could not be read: "+err.Error())
		return nil, apiLookupError("reading the Peasant session-summaries response", lookupCtx, err)
	}
	if len(body) > maxAPIBodyBytes {
		s.status.markFailure(ctx, "the Peasant session-summaries response exceeded its byte bound")
		return nil, fmt.Errorf("the Peasant session-summaries response exceeded its %d-byte bound", maxAPIBodyBytes)
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		s.status.markFailure(ctx, "the Peasant session-summaries response is not the documented JSON envelope: "+err.Error())
		return nil, fmt.Errorf("decoding the Peasant session-summaries response: %w", err)
	}
	for _, summary := range envelope.Sessions {
		key := Key{Harness: summary.Harness, Session: summary.ID}
		if _, ok := requested[key]; !ok {
			// A summary for a session we did not ask about, or one whose harness
			// disagrees with the Pasture session, is not this session.
			continue
		}
		hash, err := schema.NewProjectHash(summary.ProjectHash)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(summary.Project)
		if name == "" {
			name = hash.String()
		}
		out[key] = Project{Name: name, Hash: hash.String()}
	}
	s.status.markOK()
	return out, nil
}

// apiLookupError separates a source-owned timeout from a caller cancellation.
// The caller checks its own context first, so reaching here with a timed-out
// lookup context or a timed-out HTTP client means the source missed its own
// budget while the caller was still waiting. Marking that with the cacheable
// sentinel lets the cache absorb a hung Peasant for one TTL instead of
// retrying it on every refresh tick.
func apiLookupError(operation string, lookupCtx context.Context, err error) error {
	var netErr net.Error
	if lookupCtx.Err() != nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%s: %w: %w", operation, errSourceLookupTimeout, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// Close releases idle HTTP connections. It is safe to call more than once.
func (s *apiSource) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

package peasantproject

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/peasant-labs/schema"
)

const (
	// apiLookupTimeout bounds one session-summaries request. The integration is
	// optional; a slow Peasant must not hold a Pasture session read open.
	apiLookupTimeout = 2 * time.Second
	// maxAPIBodyBytes bounds the response body read. One page of summaries is a
	// few kilobytes; this is a corruption/abuse backstop.
	maxAPIBodyBytes = 1 << 20
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

	mu     sync.Mutex
	status Status
}

// NewAPISource validates and constructs the HTTP-backed source. A malformed URL
// is a configuration error the operator can fix at startup; runtime failures
// become unavailable status instead.
func NewAPISource(rawURL string) (Source, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return nil, fmt.Errorf("the Peasant API URL is empty; pass --peasant-url http://127.0.0.1:8787")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("the Peasant API URL %q is not an http(s) URL with a host; pass --peasant-url http://127.0.0.1:8787", rawURL)
	}
	return &apiSource{
		endpoint: trimmed,
		client:   &http.Client{Timeout: apiLookupTimeout},
		status:   Status{Enabled: true, Source: SourceAPI, State: StateOK},
	}, nil
}

func (s *apiSource) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *apiSource) markUnavailable(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = StateUnavailable
	s.status.Message = message
}

func (s *apiSource) markOK() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = StateOK
	s.status.Message = ""
}

// Lookup asks Peasant for one bounded batch of session summaries. A returned
// summary applies only when its (harness, id) is one of the requested keys and
// its project hash validates; anything else is skipped, never guessed.
func (s *apiSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	out := make(map[Key]Project, len(keys))
	if len(keys) == 0 {
		return out, nil
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
	query := url.Values{}
	query.Set("ids", strings.Join(ids, ","))
	requestURL := s.endpoint + "/api/v1/session-summaries?" + query.Encode()

	lookupCtx, cancel := context.WithTimeout(ctx, apiLookupTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		s.markUnavailable("the Peasant session-summaries request could not be built: " + err.Error())
		return nil, fmt.Errorf("building the Peasant session-summaries request: %w", err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		s.markUnavailable("the Peasant API is unreachable: " + err.Error())
		return nil, fmt.Errorf("reaching the Peasant API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.markUnavailable(fmt.Sprintf("the Peasant API answered HTTP %d for session summaries", response.StatusCode))
		return nil, fmt.Errorf("the Peasant API answered HTTP %d for session summaries", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBodyBytes+1))
	if err != nil {
		s.markUnavailable("the Peasant session-summaries response could not be read: " + err.Error())
		return nil, fmt.Errorf("reading the Peasant session-summaries response: %w", err)
	}
	if len(body) > maxAPIBodyBytes {
		s.markUnavailable("the Peasant session-summaries response exceeded its byte bound")
		return nil, fmt.Errorf("the Peasant session-summaries response exceeded its %d-byte bound", maxAPIBodyBytes)
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		s.markUnavailable("the Peasant session-summaries response is not the documented JSON envelope: " + err.Error())
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
	s.markOK()
	return out, nil
}

// Close releases idle HTTP connections. It is safe to call more than once.
func (s *apiSource) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

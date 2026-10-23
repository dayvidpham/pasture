package sessionapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/peasant-labs/schema"

	"github.com/dayvidpham/pasture/internal/tasks"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// NewHandler returns the loopback-only HTTP handler for the session API. The
// optional store enables the bind/unbind write routes; without it the read
// routes still work. The transcript route always runs the Peasant CLI beside
// the API; the browser never talks to Peasant directly.
func NewHandler(service *Service, stores ...*tasks.SessionAPIStore) http.Handler {
	var store *tasks.SessionAPIStore
	if len(stores) > 0 {
		store = stores[0]
	}
	return loopbackOnly(newHandler(service, store, newPeasantTranscriptSource()))
}

// newHandler builds the mux with explicit dependencies so tests can inject a
// transcript source without mutating PATH or the process environment.
func newHandler(service *Service, store *tasks.SessionAPIStore, transcript TranscriptSource) *handler {
	h := &handler{
		service:    service,
		store:      store,
		transcript: transcript,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", h.list)
	mux.HandleFunc("GET /api/v1/sessions/{harness}/{nativeSessionId}", h.get)
	mux.HandleFunc("GET /api/v1/sessions/{harness}/{nativeSessionId}/transcript", h.sessionTranscript)
	mux.HandleFunc("POST /api/v1/sessions/{harness}/{nativeSessionId}/bind", h.bind)
	mux.HandleFunc("POST /api/v1/sessions/{harness}/{nativeSessionId}/unbind", h.unbind)
	mux.HandleFunc("GET /api/v1/epochs", h.epochs)
	mux.HandleFunc("GET /api/v1/epochs/", h.epochRoute)
	mux.HandleFunc("GET /api/v1/inspect", h.inspect)
	mux.HandleFunc("GET /api/v1/ws", h.ws)
	mux.HandleFunc("GET /openapi.json", h.openapi)
	h.mux = mux
	return h
}

type handler struct {
	service    *Service
	store      *tasks.SessionAPIStore
	transcript TranscriptSource
	mux        *http.ServeMux
}

// ServeHTTP routes through the mux built in newHandler. NewHandler wraps the
// result in loopbackOnly, so the Host/Origin guard stays the first check.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_filter", "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	page, err := h.service.List(r.Context(), ListOptions{
		Epoch:   query.Get("epoch"),
		State:   ActivityState(query.Get("state")),
		Harness: query.Get("harness"),
		Limit:   limit,
		Cursor:  query.Get("cursor"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) bind(w http.ResponseWriter, r *http.Request) {
	harness := r.PathValue("harness")
	session := r.PathValue("nativeSessionId")
	if !validSessionKey(harness) || !validSessionKey(session) {
		writeError(w, http.StatusBadRequest, "invalid_session_key", "harness and nativeSessionId must be non-empty, at most 512 bytes, and contain no controls or slashes")
		return
	}
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "write_unavailable", "bind/unbind is unavailable in this serve process")
		return
	}
	var request tasks.SessionBindRequest
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if _, err := h.store.Bind(r.Context(), harness, session, request); err != nil {
		writeStoreError(w, err)
		return
	}
	h.detail(w, r, harness, session)
}

func (h *handler) unbind(w http.ResponseWriter, r *http.Request) {
	harness := r.PathValue("harness")
	session := r.PathValue("nativeSessionId")
	if !validSessionKey(harness) || !validSessionKey(session) {
		writeError(w, http.StatusBadRequest, "invalid_session_key", "harness and nativeSessionId must be non-empty, at most 512 bytes, and contain no controls or slashes")
		return
	}
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "write_unavailable", "bind/unbind is unavailable in this serve process")
		return
	}
	var request struct {
		ExpectedBinding string `json:"expectedBinding,omitempty"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if _, err := h.store.Unbind(r.Context(), harness, session, request.ExpectedBinding); err != nil {
		writeStoreError(w, err)
		return
	}
	h.detail(w, r, harness, session)
}

func (h *handler) detail(w http.ResponseWriter, r *http.Request, harness, session string) {
	detail, err := h.service.Get(r.Context(), harness, session)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tasks.ErrUnknownEpoch):
		writeError(w, http.StatusNotFound, "unknown_epoch", err.Error())
	case errors.Is(err, tasks.ErrNoOpenBinding):
		writeError(w, http.StatusConflict, "no_open_binding", "the session has no open binding to unbind")
	case errors.Is(err, tasks.ErrBindingConflict):
		writeError(w, http.StatusConflict, "binding_conflict", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "invalid_binding", err.Error())
	}
}

func (h *handler) epochs(w http.ResponseWriter, r *http.Request) {
	page, err := h.service.Epochs(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *handler) epochRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/epochs/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		writeError(w, http.StatusNotFound, "not_found", "epoch id is required")
		return
	}
	if strings.HasSuffix(rest, "/sessions") {
		epochID := strings.TrimSuffix(rest, "/sessions")
		if !validEpochID(epochID) {
			writeError(w, http.StatusBadRequest, "invalid_epoch_id", "epoch id is invalid")
			return
		}
		query := r.URL.Query()
		limit := 50
		if raw := query.Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_filter", "limit must be an integer between 1 and 100")
				return
			}
			limit = parsed
		}
		page, err := h.service.EpochSessions(r.Context(), epochID, ListOptions{
			State:   ActivityState(query.Get("state")),
			Harness: query.Get("harness"),
			Limit:   limit,
			Cursor:  query.Get("cursor"),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	if !validEpochID(rest) {
		writeError(w, http.StatusBadRequest, "invalid_epoch_id", "epoch id is invalid")
		return
	}
	detail, err := h.service.Epoch(r.Context(), rest)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handler) inspect(w http.ResponseWriter, r *http.Request) {
	payload, err := h.service.Inspect(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func validEpochID(value string) bool {
	if value == "" || len(value) > 2048 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (h *handler) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(OpenAPIBytes())
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	harness := r.PathValue("harness")
	session := r.PathValue("nativeSessionId")
	if !validSessionKey(harness) || !validSessionKey(session) {
		writeError(w, http.StatusBadRequest, "invalid_session_key", "harness and nativeSessionId must be non-empty, at most 512 bytes, and contain no controls or slashes")
		return
	}
	detail, err := h.service.Get(r.Context(), harness, session)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// sessionTranscript serves the durable Peasant transcript for one session. The
// body is Peasant's SessionDetailPayload document unchanged; the browser
// validates it against @peasant-labs/schema before rendering.
//
// The route validates the requested identity against the shared schema before
// invoking Peasant: a native id the shared contract cannot express is never
// partly coerced into a subprocess argument. The validated document is streamed
// from the export file, so one transcript never becomes a 128 MiB buffer.
func (h *handler) sessionTranscript(w http.ResponseWriter, r *http.Request) {
	harness := r.PathValue("harness")
	session := r.PathValue("nativeSessionId")
	if !validSessionKey(harness) || !validSessionKey(session) {
		writeError(w, http.StatusBadRequest, "invalid_session_key", "harness and nativeSessionId must be non-empty, at most 512 bytes, and contain no controls or slashes")
		return
	}
	if _, err := schema.NewSessionID(session); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_native_session_id", fmt.Sprintf("the shared session contract rejects native session id %q: %v; retain the host form and export it from Peasant directly", session, err))
		return
	}
	if !schema.Harness(harness).IsKnown() {
		writeError(w, http.StatusBadRequest, "invalid_harness", fmt.Sprintf("harness %q is not in the shared schema harness vocabulary (%s); use the canonical harness id Pasture lists for this session", harness, knownHarnessNames()))
		return
	}
	if h.transcript == nil {
		writeError(w, http.StatusServiceUnavailable, "transcript_unavailable", "this serve process has no transcript source; restart `pasture serve` with Peasant installed, then retry")
		return
	}
	export, err := h.transcript.ExportTranscript(r.Context(), harness, session)
	if err != nil {
		writeTranscriptError(w, err)
		return
	}
	if export == nil {
		writeError(w, http.StatusInternalServerError, "malformed_transcript", "Peasant returned no readable transcript document; export the session directly with `peasant export sessions` and retry")
		return
	}
	// Once the source returns an export, its file and private directory belong
	// to this handler. Install cleanup before validating the export so a source
	// that returns a nil Content or a non-positive Size cannot leak the file.
	defer func() { _ = export.Close() }()
	if export.Content == nil || export.Size <= 0 {
		writeError(w, http.StatusInternalServerError, "malformed_transcript", "Peasant returned no readable transcript document; export the session directly with `peasant export sessions` and retry")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", export.ModTime, export.Content)
}

func writeTranscriptError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTranscriptSourceUnavailable):
		writeError(w, http.StatusServiceUnavailable, "peasant_unavailable", err.Error())
	case errors.Is(err, ErrTranscriptNotFound):
		writeError(w, http.StatusNotFound, "transcript_not_found", err.Error())
	case errors.Is(err, ErrTranscriptIncomplete):
		writeError(w, http.StatusConflict, "transcript_incomplete", err.Error())
	case errors.Is(err, ErrTranscriptUnsupported):
		writeError(w, http.StatusConflict, "transcript_unsupported", err.Error())
	case errors.Is(err, ErrTranscriptIdentityMismatch):
		writeError(w, http.StatusConflict, "transcript_identity_mismatch", err.Error())
	case errors.Is(err, ErrTranscriptTimedOut):
		writeError(w, http.StatusGatewayTimeout, "transcript_timeout", err.Error())
	case errors.Is(err, ErrTranscriptTooLarge):
		writeError(w, http.StatusInternalServerError, "transcript_too_large", err.Error())
	case errors.Is(err, ErrTranscriptMalformed):
		writeError(w, http.StatusInternalServerError, "malformed_transcript", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "peasant_export_failed", err.Error())
	}
}

// knownHarnessNames lists the shared schema harness vocabulary for an
// actionable 400, so a caller can correct the value without reading the schema.
func knownHarnessNames() string {
	harnesses := schema.Harnesses()
	names := make([]string, len(harnesses))
	for i, harness := range harnesses {
		names[i] = harness.String()
	}
	return strings.Join(names, ", ")
}

func validSessionKey(value string) bool {
	if value == "" || len(value) > 512 || strings.Contains(value, "/") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no session matches that harness and native session id")
	case errors.Is(err, ErrInvalidPageToken):
		writeError(w, http.StatusBadRequest, "invalid_page_token", "the page token is malformed or no longer valid")
	case errors.Is(err, ErrInvalidFilter):
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
	case errors.Is(err, ErrStaleCache):
		writeError(w, http.StatusServiceUnavailable, "stale_session_cache", "the session cache is behind the journal; run `pasture session list` or `pasture serve --backfill` to rebuild it")
	default:
		writeError(w, http.StatusInternalServerError, "storage_fault", "the session store could not be read; inspect the serve log and repair the database")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "forbidden_host", "session API requests must use a loopback Host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !loopbackHost(parsed.Host) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "session API requests must use a loopback Origin")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	if hostport == "" {
		return false
	}
	host := hostport
	if parsedHost, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

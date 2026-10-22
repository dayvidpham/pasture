package sessionapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// NewHandler returns the loopback-only HTTP handler for the session API.
func NewHandler(service *Service) http.Handler {
	h := &handler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", h.list)
	mux.HandleFunc("GET /api/v1/sessions/{harness}/{nativeSessionId}", h.get)
	mux.HandleFunc("GET /openapi.json", h.openapi)
	return loopbackOnly(mux)
}

type handler struct {
	service *Service
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
		writeError(w, http.StatusServiceUnavailable, "stale_session_cache", "the session cache is behind the journal; the next serve startup or rebuild repairs it")
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

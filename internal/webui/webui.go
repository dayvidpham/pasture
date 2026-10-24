package webui

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

const (
	indexFile = "index.html"
	assetsDir = "assets"

	// immutableCache is for Vite's content-hashed files under /assets/. The
	// filename changes when the bytes change, so the response can be cached for
	// a year.
	immutableCache = "public, max-age=31536000, immutable"
	// noCache is for other real files in the bundle (for example favicon.svg).
	noCache = "no-cache"
	// noStore is for the application shell: a stale index.html would point at
	// hashed assets that no longer exist after an upgrade.
	noStore = "no-store"
)

// New returns a handler that serves the console in assets and delegates
// /api, /api/*, and /openapi.json to api. It fails closed when the bundle has
// no index.html, because serving API-only would look like a working server with
// a blank console.
func New(api http.Handler, assets fs.FS) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("webui: an API handler is required")
	}
	if assets == nil {
		return nil, errors.New("webui: an asset filesystem is required")
	}
	if err := requireIndex(assets); err != nil {
		return nil, err
	}
	return &handler{api: api, assets: assets}, nil
}

// NewEmbedded serves the console bundle compiled into this binary. It is the
// production path used by `pasture serve`.
func NewEmbedded(api http.Handler) (http.Handler, error) {
	assets, err := Embedded()
	if err != nil {
		return nil, fmt.Errorf("webui: open the embedded console bundle: %w — rebuild pasture after `cd web && pnpm build`", err)
	}
	return New(api, assets)
}

// NewFromDir serves a console build from dir on disk. It is the development
// override (`pasture serve --web-dir DIR`) and fails at startup when the
// directory has no index.html.
func NewFromDir(api http.Handler, dir string) (http.Handler, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("webui: --web-dir requires a directory containing index.html")
	}
	handler, err := New(api, os.DirFS(dir))
	if err != nil {
		return nil, fmt.Errorf("webui: serve the console from %q: %w", dir, err)
	}
	return handler, nil
}

// requireIndex fails closed when the bundle is absent or incomplete.
func requireIndex(assets fs.FS) error {
	info, err := fs.Stat(assets, indexFile)
	if err != nil {
		return fmt.Errorf("web console bundle has no %s: %w — build it with `cd web && pnpm install --frozen-lockfile && pnpm build` (which writes internal/webui/assets), then rebuild pasture; for a development build pass `pasture serve --web-dir <dir>`", indexFile, err)
	}
	if info.IsDir() {
		return fmt.Errorf("web console bundle has a directory named %s, not the application shell — rebuild it with `cd web && pnpm build`", indexFile)
	}
	return nil
}

// handler serves the console shell, its static files, and the API on one
// loopback origin.
type handler struct {
	api    http.Handler
	assets fs.FS
}

// ServeHTTP routes API paths to the session API and everything else to the
// static console. The API keeps its own methods and cache policy; the console
// supports GET and HEAD only.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if isAPIPath(r.URL.Path) {
		h.api.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", noStore)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name, ok := staticName(r.URL.Path)
	if !ok {
		h.notFound(w, r)
		return
	}
	if name == "" || name == indexFile {
		h.serveIndex(w, r, noStore)
		return
	}

	info, err := fs.Stat(h.assets, name)
	switch {
	case err == nil && info.IsDir():
		// Only "/" lists/serves the shell. A real directory path in the bundle
		// (for example /assets/) is never a directory listing.
		h.notFound(w, r)
		return
	case err == nil && !info.Mode().IsRegular():
		h.notFound(w, r)
		return
	case err == nil:
		cache := noCache
		if isHashedAsset(name) {
			cache = immutableCache
		}
		w.Header().Set("Cache-Control", cache)
		http.ServeFileFS(w, r, h.assets, name)
		return
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrInvalid):
		// A missing asset or extension path is a broken request, never a client
		// route. Everything else is a SPA route and gets the shell.
		if isAssetLike(name) {
			h.notFound(w, r)
			return
		}
		h.serveIndex(w, r, noStore)
		return
	default:
		h.notFound(w, r)
		return
	}
}

// serveIndex serves the application shell. Content-Type is explicit because
// ServeFileFS would infer it from the extension anyway, and the explicit value
// keeps the shell recognizable over os.DirFS builds with unusual names.
func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request, cache string) {
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFileFS(w, r, h.assets, indexFile)
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", noStore)
	http.Error(w, "not found", http.StatusNotFound)
}

func isAPIPath(urlPath string) bool {
	return urlPath == "/api" || strings.HasPrefix(urlPath, "/api/") || urlPath == "/openapi.json"
}

// staticName turns a URL path into a clean bundle name and rejects traversal and
// dotfiles. The second return value is false when the path must be refused.
func staticName(urlPath string) (string, bool) {
	if urlPath == "" || urlPath == "/" {
		return "", true
	}
	// Reject any literal parent segment before cleaning collapses it. A bundle
	// has no legitimate "..", so a request that names one is an attack.
	for _, segment := range strings.Split(strings.TrimPrefix(urlPath, "/"), "/") {
		if segment == ".." {
			return "", false
		}
	}
	if strings.Contains(urlPath, "\\") || strings.ContainsRune(urlPath, 0) {
		return "", false
	}
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || name == "." {
		return "", true
	}
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") {
			return "", false
		}
	}
	return name, true
}

// isHashedAsset reports whether name is a Vite content-hashed asset. Only that
// directory earns the immutable cache; the shell and other files stay
// revalidatable.
func isHashedAsset(name string) bool {
	return strings.HasPrefix(name, assetsDir+"/")
}

// isAssetLike reports whether a missing name was meant to be a file rather than
// a client route: anything under /assets/, or any path with an extension.
func isAssetLike(name string) bool {
	if name == assetsDir || strings.HasPrefix(name, assetsDir+"/") {
		return true
	}
	return path.Ext(name) != ""
}

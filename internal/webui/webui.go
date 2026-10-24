package webui

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	indexFile = "index.html"
	assetsDir = "assets"

	// immutableCache is for Vite's content-hashed files under /assets/. The
	// filename changes when the bytes change, so the response can be cached for
	// a year.
	immutableCache = "public, max-age=31536000, immutable"
	// noCache is for other real files in the bundle (for example favicon.svg or
	// an unhashed file under /assets/).
	noCache = "no-cache"
	// noStore is for the application shell: a stale index.html would point at
	// hashed assets that no longer exist after an upgrade.
	noStore = "no-store"
)

// New returns a handler that serves the console in assets and delegates
// /api, /api/*, and /openapi.json to api. It fails closed when the bundle has
// no index.html or when the shell names an asset the bundle does not contain.
func New(api http.Handler, assets fs.FS) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("webui: an API handler is required")
	}
	if assets == nil {
		return nil, errors.New("webui: an asset filesystem is required")
	}
	if err := validateBundle(assets, genericHints()); err != nil {
		return nil, err
	}
	return &handler{api: api, assets: assets}, nil
}

// NewEmbedded serves the console bundle compiled into this binary. It is the
// production path used by `pasture serve`.
func NewEmbedded(api http.Handler) (http.Handler, error) {
	assets, err := Embedded()
	if err != nil {
		return nil, err
	}
	return New(api, assets)
}

// NewFromDir serves a console build from dir on disk. It is the development
// override (`pasture serve --web-dir DIR`). The directory must be a trusted
// build tree: symlinks anywhere under it are refused, because os.DirFS would
// follow them out of the directory.
func NewFromDir(api http.Handler, dir string) (http.Handler, error) {
	assets, err := AssetsFromDir(dir)
	if err != nil {
		return nil, err
	}
	return New(api, assets)
}

// AssetsFromDir validates a console build on disk and returns it as an fs.FS.
// It refuses symlinks anywhere in the tree, then requires index.html and every
// local /assets/ reference it names. The caller can resolve the bundle early
// (before opening a store) and hand it to New later.
func AssetsFromDir(dir string) (fs.FS, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("webui: --web-dir requires a directory containing index.html")
	}
	if err := rejectSymlinks(dir); err != nil {
		return nil, err
	}
	assets := os.DirFS(dir)
	if err := validateBundle(assets, dirHints(dir)); err != nil {
		return nil, fmt.Errorf("webui: --web-dir %q: %w", dir, err)
	}
	return assets, nil
}

// rejectSymlinks walks dir without following links. A symlink in a --web-dir
// tree would let os.DirFS serve a file outside the operator-chosen directory,
// so the whole tree is refused rather than individual escaping links.
func rejectSymlinks(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("webui: --web-dir %q: %w — point --web-dir at a Vite production build directory", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("webui: --web-dir %q is a symlink; point it at a trusted regular build directory", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("webui: --web-dir %q is a file, not a build directory; point it at the directory that contains index.html", dir)
	}
	err = filepath.WalkDir(dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return &symlinkError{link: name}
		}
		return nil
	})
	if err != nil {
		var linkErr *symlinkError
		if errors.As(err, &linkErr) {
			return fmt.Errorf("webui: --web-dir: %w", err)
		}
		return fmt.Errorf("webui: read --web-dir %q: %w — --web-dir must be a trusted, readable build directory", dir, err)
	}
	return nil
}

// symlinkError marks a symlink refusal so the walk error is reported as a
// --web-dir policy failure rather than an unreadable-directory failure.
type symlinkError struct{ link string }

func (e *symlinkError) Error() string {
	return fmt.Sprintf("symlink %q is not allowed in a --web-dir tree; --web-dir must be a trusted build directory without symlinks", e.link)
}

// validateBundle fails closed when the shell or an asset it names is absent.
func validateBundle(assets fs.FS, hints bundleHints) error {
	info, err := fs.Stat(assets, indexFile)
	if err != nil {
		return fmt.Errorf("web console bundle has no %s at its root: %w — %s", indexFile, err, hints.missingIndex)
	}
	if info.IsDir() {
		return fmt.Errorf("web console bundle has a directory named %s, not the application shell — %s", indexFile, hints.missingIndex)
	}
	index, err := fs.ReadFile(assets, indexFile)
	if err != nil {
		return fmt.Errorf("web console bundle: read %s: %w — %s", indexFile, err, hints.missingIndex)
	}
	for _, ref := range localAssetRefs(index) {
		name := strings.TrimPrefix(ref, "/")
		refInfo, err := fs.Stat(assets, name)
		if err != nil {
			return fmt.Errorf("web console %s references %q, but the bundle has no such file: %w — %s", indexFile, ref, err, hints.missingRef)
		}
		if refInfo.IsDir() {
			return fmt.Errorf("web console %s references %q, but the bundle has a directory there, not a file — %s", indexFile, ref, hints.missingRef)
		}
	}
	return nil
}

// bundleHints is the source-specific recovery text for a failed bundle check.
// The embedded bundle tells the operator to rebuild pasture; an external
// --web-dir build tells them to rebuild that directory and can point back at
// the embedded default.
type bundleHints struct {
	missingIndex string
	missingRef   string
}

func genericHints() bundleHints {
	return bundleHints{
		missingIndex: "build the console with `cd web && pnpm install --frozen-lockfile && pnpm build` (which writes internal/webui/assets), then rebuild pasture; for a development build pass `pasture serve --web-dir <dir>`",
		missingRef:   "rebuild the console with `cd web && pnpm install --frozen-lockfile && pnpm build`, then rebuild pasture",
	}
}

func embeddedHints() bundleHints {
	return bundleHints{
		missingIndex: "this binary's embedded bundle is incomplete; rebuild pasture after `cd web && pnpm install --frozen-lockfile && pnpm build`",
		missingRef:   "this binary's embedded bundle is stale or incomplete; rebuild pasture after `cd web && pnpm install --frozen-lockfile && pnpm build`",
	}
}

func dirHints(dir string) bundleHints {
	return bundleHints{
		missingIndex: fmt.Sprintf("directory %q has no %s at its root; point --web-dir at a Vite production build (the build output includes index.html and assets/, as in internal/webui/assets), or omit --web-dir to serve the embedded bundle", dir, indexFile),
		missingRef:   "rebuild that directory with `cd web && pnpm build` and point --web-dir at its output, or omit --web-dir to serve the embedded bundle",
	}
}

// localAssetRef is one local /assets/ URL in the shell. Query strings and
// fragments are excluded, so the checked name is the file the browser fetches.
var localAssetRef = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](/assets/[^"'?#]+)["']`)

// localAssetRefs returns the unique local /assets/ references in the shell.
func localAssetRefs(index []byte) []string {
	matches := localAssetRef.FindAllSubmatch(index, -1)
	refs := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		ref := string(match[1])
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}

// handler serves the console shell, its static files, and the API on one
// loopback origin.
type handler struct {
	api    http.Handler
	assets fs.FS
}

// ServeHTTP routes API paths to the session API and everything else to the
// static console. The API keeps its own methods and cache policy; the console
// supports GET and HEAD only and always forbids framing.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if isAPIPath(r.URL.Path) {
		h.api.ServeHTTP(w, r)
		return
	}

	// The console never needs to be framed. Do not add default-src here: the
	// fairtrade CSS imports Google Fonts at runtime, and this header exists only
	// to stop clickjacking.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", noStore)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name, ok := staticName(r.URL.Path)
	if !ok {
		h.notFound(w)
		return
	}
	if name == "" || name == indexFile {
		h.serveIndex(w, r, noStore)
		return
	}

	info, err := fs.Stat(h.assets, name)
	switch {
	case err == nil && info.IsDir():
		// Only "/" serves the shell. A real directory path in the bundle (for
		// example /assets/) is never a directory listing.
		h.notFound(w)
		return
	case err == nil && !info.Mode().IsRegular():
		h.notFound(w)
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
			h.notFound(w)
			return
		}
		h.serveIndex(w, r, noStore)
		return
	default:
		h.notFound(w)
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

func (h *handler) notFound(w http.ResponseWriter) {
	// Framing headers were set at the top of ServeHTTP; keep the error
	// uncacheable so a corrected deploy is visible immediately.
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

// hashedAssetName matches Vite's content-hash suffix: a stem, a dash, eight
// characters of hash, and an extension, for example
// assets/index-C-tgJrWs.js. Unhashed files under /assets/ are revalidated.
var hashedAssetName = regexp.MustCompile(`^assets/(?:[^/]+/)*[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

// isHashedAsset reports whether name is a Vite content-hashed asset. Only that
// shape earns the immutable cache; the shell and other files stay
// revalidatable.
func isHashedAsset(name string) bool {
	return hashedAssetName.MatchString(name)
}

// isAssetLike reports whether a missing name was meant to be a file rather than
// a client route: anything under /assets/, or any path with an extension.
func isAssetLike(name string) bool {
	if name == assetsDir || strings.HasPrefix(name, assetsDir+"/") {
		return true
	}
	return path.Ext(name) != ""
}

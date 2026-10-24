package webui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiFunc func(http.ResponseWriter, *http.Request)

func (f apiFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) { f(w, r) }

func testAPI() http.Handler {
	return apiFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-API", "true")
		_, _ = io.WriteString(w, "api:"+r.URL.Path)
	})
}

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":               {Data: []byte(`<!doctype html><html><body><div id="root"></div><script src="/assets/index-C-tgJrWs.js"></script></body></html>`)},
		"assets/index-C-tgJrWs.js": {Data: []byte("console.log('ok')")},
		"assets/plain.js":          {Data: []byte("console.log('plain')")},
		"favicon.svg":              {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"docs/readme.txt":          {Data: []byte("docs")},
		".secret":                  {Data: []byte("hidden")},
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := New(testAPI(), testAssets())
	require.NoError(t, err)
	return handler
}

func do(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestIndexAtRoot(t *testing.T) {
	t.Parallel()

	recorder := do(t, newTestHandler(t), http.MethodGet, "/")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `id="root"`)
	require.Equal(t, noStore, recorder.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/html")
}

func TestHashedAssetIsImmutable(t *testing.T) {
	t.Parallel()

	recorder := do(t, newTestHandler(t), http.MethodGet, "/assets/index-C-tgJrWs.js")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "console.log('ok')", recorder.Body.String())
	require.Equal(t, immutableCache, recorder.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
}

func TestNonHashedFileRevalidates(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, target := range []string{"/favicon.svg", "/assets/plain.js"} {
		recorder := do(t, handler, http.MethodGet, target)
		require.Equal(t, http.StatusOK, recorder.Code, target)
		require.Equal(t, noCache, recorder.Header().Get("Cache-Control"), target)
	}
}

func TestAntiFramingHeaders(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, target := range []string{"/", "/assets/index-C-tgJrWs.js", "/sessions/abc"} {
		recorder := do(t, handler, http.MethodGet, target)
		require.Equal(t, http.StatusOK, recorder.Code, target)
		require.Equal(t, "DENY", recorder.Header().Get("X-Frame-Options"), target)
		require.Equal(t, "frame-ancestors 'none'", recorder.Header().Get("Content-Security-Policy"), target)
	}
}

func TestAssetRangeRequest(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/assets/index-C-tgJrWs.js", nil)
	request.Header.Set("Range", "bytes=0-6")
	recorder := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusPartialContent, recorder.Code)
	require.Equal(t, "console", recorder.Body.String())
	require.Equal(t, "bytes 0-6/17", recorder.Header().Get("Content-Range"))
}

func TestSPAFallbackServesTheShell(t *testing.T) {
	t.Parallel()

	recorder := do(t, newTestHandler(t), http.MethodGet, "/sessions/abc")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `id="root"`)
	require.Equal(t, noStore, recorder.Header().Get("Cache-Control"))
}

func TestMissingAssetLikePathsAre404(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, target := range []string{
		"/assets/missing.js",
		"/assets/nested/app.js",
		"/missing.js",
		"/notes.txt",
		"/assets",
		"/assets/",
		"/docs",
		"/docs/",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			recorder := do(t, handler, http.MethodGet, target)
			assert.Equal(t, http.StatusNotFound, recorder.Code, "%s must not fall back to the shell", target)
			assert.NotContains(t, recorder.Body.String(), `id="root"`)
			assert.Equal(t, noStore, recorder.Header().Get("Cache-Control"))
		})
	}
}

func TestAPIPathsDelegate(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, target := range []string{"/api", "/api/v1/sessions", "/openapi.json"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			recorder := do(t, handler, http.MethodGet, target)
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, "api:"+target, recorder.Body.String())
			assert.Equal(t, "true", recorder.Header().Get("X-Test-API"))
		})
	}
}

func TestAPIPathsKeepNonReadMethods(t *testing.T) {
	t.Parallel()

	recorder := do(t, newTestHandler(t), http.MethodPost, "/api/v1/sessions/harness/session/bind")
	require.Equal(t, http.StatusOK, recorder.Code, "UI method policy must not intercept API writes")
	require.Equal(t, "api:/api/v1/sessions/harness/session/bind", recorder.Body.String())
}

func TestNonReadMethodOnTheConsoleIs405(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			recorder := do(t, newTestHandler(t), method, "/sessions/abc")
			assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
			assert.Equal(t, "GET, HEAD", recorder.Header().Get("Allow"))
		})
	}
}

func TestHeadServesHeadersWithoutBody(t *testing.T) {
	t.Parallel()

	recorder := do(t, newTestHandler(t), http.MethodHead, "/")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Body.String())
	require.Equal(t, noStore, recorder.Header().Get("Cache-Control"))
}

func TestDotfilesAndTraversalAreRefused(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	for _, target := range []string{"/.secret", "/.well-known/thing", "/%2e%2e/etc/passwd"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			recorder := do(t, handler, http.MethodGet, target)
			assert.Equal(t, http.StatusNotFound, recorder.Code)
		})
	}

	// Exercise the raw path too: net/http normally cleans it, so the guard is
	// pinned directly.
	if name, ok := staticName("/../etc/passwd"); ok {
		t.Fatalf("staticName accepted traversal: name=%q", name)
	}
}

func TestNewFailsClosedWithoutIndex(t *testing.T) {
	t.Parallel()

	_, err := New(testAPI(), fstest.MapFS{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no index.html")
	require.Contains(t, err.Error(), "pnpm build")
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	_, err := New(nil, testAssets())
	require.Error(t, err)
	_, err = New(testAPI(), nil)
	require.Error(t, err)
}

func TestNewRejectsMissingReferencedAsset(t *testing.T) {
	t.Parallel()

	assets := fstest.MapFS{
		"index.html":               {Data: []byte(`<script src="/assets/missing-C-tgJrWs.js"></script>`)},
		"assets/other-D-tgJrWs.js": {Data: []byte("other")},
	}
	_, err := New(testAPI(), assets)
	require.Error(t, err)
	require.Contains(t, err.Error(), "references")
	require.Contains(t, err.Error(), "/assets/missing-C-tgJrWs.js")
	require.Contains(t, err.Error(), "rebuild the console")
}

func TestNewFromDir(t *testing.T) {
	t.Parallel()

	t.Run("missing index", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := NewFromDir(testAPI(), dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no index.html")
		require.Contains(t, err.Error(), dir)
		require.Contains(t, err.Error(), "at its root")
		require.Contains(t, err.Error(), "internal/webui/assets")
		require.Contains(t, err.Error(), "omit --web-dir")
		require.NotContains(t, err.Error(), "rebuild pasture",
			"a --web-dir operator should not be told to rebuild the binary")
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := NewFromDir(testAPI(), dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no index.html")
	})

	t.Run("symlink escape is refused", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"),
			[]byte(`<div id="root"></div>`), 0o600))
		outside := filepath.Join(t.TempDir(), "outside-secret.txt")
		require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
		link := filepath.Join(dir, "leak.txt")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		assets, err := AssetsFromDir(dir)
		require.Error(t, err)
		require.Nil(t, assets)
		require.Contains(t, err.Error(), "symlink")
		require.Contains(t, err.Error(), link, "the error must name the offending link")
		require.Contains(t, err.Error(), "trusted build directory")

		_, err = NewFromDir(testAPI(), dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "symlink")
	})

	t.Run("valid build", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<div id="root"></div>`), 0o600))
		handler, err := NewFromDir(testAPI(), dir)
		require.NoError(t, err)
		recorder := do(t, handler, http.MethodGet, "/")
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `id="root"`)
	})
}

func TestEmbeddedBundleContainsIndex(t *testing.T) {
	t.Parallel()

	assets, err := Embedded()
	require.NoError(t, err)
	info, err := fs.Stat(assets, "index.html")
	require.NoError(t, err, "the committed production bundle must contain index.html")
	require.False(t, info.IsDir())

	handler, err := NewEmbedded(testAPI())
	require.NoError(t, err)
	recorder := do(t, handler, http.MethodGet, "/")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `id="root"`)

	index, err := fs.ReadFile(assets, "index.html")
	require.NoError(t, err)
	refs := localAssetRefs(index)
	require.NotEmpty(t, refs, "the committed shell must name its hashed assets")
	for _, ref := range refs {
		name := strings.TrimPrefix(ref, "/")
		info, err := fs.Stat(assets, name)
		require.NoError(t, err, "the committed bundle must contain the asset the shell references: %s", ref)
		require.False(t, info.IsDir(), ref)
	}
}

// A request built by hand can carry a path the net/http server would otherwise
// clean before it reaches a handler. The traversal refusal must not depend on
// the server doing that cleanup.
func TestRawTraversalPathIs404(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	request := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/../etc/passwd"},
		Host:   "127.0.0.1",
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

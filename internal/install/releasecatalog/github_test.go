package releasecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

const releasesEndpoint = "https://api.github.com/repos/dayvidpham/pasture/releases"

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func response(req *http.Request, status int, body, link string, final string) *http.Response {
	finalReq := req
	if final != "" {
		u, _ := url.Parse(final)
		finalReq = &http.Request{Method: req.Method, URL: u, Header: req.Header}
	}
	h := http.Header{}
	if link != "" {
		h.Set("Link", link)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h, Request: finalReq}
}

func TestGitHubPaginationFindsPageTwo(t *testing.T) {
	t.Parallel()
	var requests []string
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		if req.URL.Query().Get("page") == "2" {
			return response(req, 200, `[{"tag_name":"v1.2.0","draft":false,"prerelease":false,"assets":[]}]`, "", ""), nil
		}
		next := releasesEndpoint + "?page=2&per_page=100"
		return response(req, 200, `[{"tag_name":"v2.0.0-rc.1","draft":false,"prerelease":true,"assets":[]}]`, "<"+next+">; rel=\"next\"", ""), nil
	})
	source, err := NewGitHubSource(doer, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := source.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(releases) != 2 || releases[1].Version().String() != "1.2.0" {
		t.Fatalf("requests=%v releases=%v", requests, releases)
	}
}

func TestProductionPaginationSelectsOnlyCompatiblePageTwoRelease(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join("testdata", entry.Name()))
		files[entry.Name()] = b
	}
	manifest, err := artifact.ParseAggregateManifest(files[artifact.AggregateManifestAsset])
	if err != nil {
		t.Fatal(err)
	}
	assets := []githubAsset{}
	names := []string{artifact.AggregateManifestAsset, artifact.AggregateChecksumAsset}
	for _, component := range manifest.Components() {
		names = append(names, component.Asset())
	}
	for _, name := range names {
		assets = append(assets, githubAsset{Name: name, URL: "https://objects.githubusercontent.com/" + name, Size: int64(len(files[name])), State: "uploaded"})
	}
	pageTwo, _ := json.Marshal([]githubRelease{{TagName: "v1.2.0", Assets: assets}})
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "objects.githubusercontent.com" {
			return response(req, 200, string(files[strings.TrimPrefix(req.URL.Path, "/")]), "", ""), nil
		}
		if req.URL.Query().Get("page") == "2" {
			return response(req, 200, string(pageTwo), "", ""), nil
		}
		next := releasesEndpoint + "?page=2&per_page=100"
		return response(req, 200, `[{"tag_name":"v2.0.0","draft":true,"prerelease":false,"assets":[]}]`, "<"+next+">; rel=\"next\"", ""), nil
	})
	source, err := NewGitHubSource(doer, releasesEndpoint, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := New(source)
	installer, _ := artifact.ParseVersion("1.5.0")
	candidates, err := catalog.ListCompatible(context.Background(), installer, FinalsOnly)
	if err != nil || len(candidates) != 1 || candidates[0].Version().String() != "1.2.0" {
		t.Fatalf("candidates=%v err=%v", candidates, err)
	}
	verified, err := catalog.ResolveCandidate(context.Background(), candidates[0], 0)
	if err != nil || verified.Manifest().Version().String() != "1.2.0" {
		t.Fatalf("verified=%v err=%v", verified.Manifest().Version(), err)
	}
}

func TestGitHubPaginationRejectsUntrustedAndCycles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, link string
		limits     GitHubLimits
	}{
		{"malformed", "not-a-link", GitHubLimits{}}, {"off-host", `<https://evil.example/repos/dayvidpham/pasture/releases?page=2>; rel="next"`, GitHubLimits{}}, {"cycle", `<https://api.github.com/repos/dayvidpham/pasture/releases?per_page=100>; rel="next"`, GitHubLimits{}}, {"limit", `<https://api.github.com/repos/dayvidpham/pasture/releases?page=2&per_page=100>; rel="next"`, GitHubLimits{MaxPages: 1}}}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(req *http.Request) (*http.Response, error) { return response(req, 200, `[]`, tc.link, ""), nil })
			source, err := NewGitHubSourceWithLimits(doer, releasesEndpoint, tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.ListReleases(context.Background()); err == nil {
				t.Fatal("expected pagination rejection")
			}
		})
	}
}

func TestGitHubPaginationCancellationBetweenPages(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return response(req, 200, `[]`, `<https://api.github.com/repos/dayvidpham/pasture/releases?page=2&per_page=100>; rel="next"`, ""), nil
	})
	source, _ := NewGitHubSource(doer, releasesEndpoint, 4096)
	_, err := source.ListReleases(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestGitHubRequestPurposeRejectsHostileRedirects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		asset bool
		final string
	}{{"catalog", false, "https://evil.example/releases"}, {"asset", true, "https://evil.example/file"}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(req *http.Request) (*http.Response, error) { return response(req, 200, `[]`, "", tc.final), nil })
			source, _ := NewGitHubSource(doer, releasesEndpoint, 4096)
			var err error
			if tc.asset {
				_, err = source.OpenURL(context.Background(), "https://objects.githubusercontent.com/file")
			} else {
				_, err = source.ListReleases(context.Background())
			}
			if err == nil {
				t.Fatal("expected redirect trust rejection")
			}
		})
	}
}

func TestGitHubRequestPurposeRejectsMissingFinalURL(t *testing.T) {
	t.Parallel()
	for _, asset := range []bool{false, true} {
		asset := asset
		t.Run(strconv.FormatBool(asset), func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]"))}, nil
			})
			source, _ := NewGitHubSource(doer, releasesEndpoint, 4096)
			var err error
			if asset {
				_, err = source.OpenURL(context.Background(), "https://objects.githubusercontent.com/file")
			} else {
				_, err = source.ListReleases(context.Background())
			}
			if err == nil {
				t.Fatal("missing final URL accepted")
			}
		})
	}
}

func TestProductionConstructorAndOpenURLUseInjectedDoer(t *testing.T) {
	t.Parallel()
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.github.com" {
			return response(req, 200, `[]`, "", ""), nil
		}
		return response(req, 200, "asset", "", ""), nil
	})
	source, err := NewGitHubSource(doer, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.ListReleases(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader, err := source.OpenURL(context.Background(), "https://objects.githubusercontent.com/file")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	b, _ := io.ReadAll(reader)
	if string(b) != "asset" {
		t.Fatalf("got %q", b)
	}
}

func TestGitHubTransportAndStatusErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		doer HTTPDoer
	}{{"transport", doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}, {"nil", doerFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}, {"nil body", doerFunc(func(r *http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Request: r}, nil })}, {"status", doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 503, "", "", ""), nil })}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, _ := NewGitHubSource(tc.doer, releasesEndpoint, 4096)
			if _, err := source.ListReleases(context.Background()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestProductionGitHubSourceRequiresHTTPSGitHub(t *testing.T) {
	t.Parallel()
	if _, err := NewGitHubSource(http.DefaultClient, "http://api.github.com/repos/x/y/releases", 1); err == nil {
		t.Fatal("expected HTTP rejection")
	}
	if _, err := NewGitHubSource(http.DefaultClient, "https://example.com/releases", 1); err == nil {
		t.Fatal("expected host rejection")
	}
}

func TestGitHubMalformedTrailingAndOversizedPages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		limit      int64
	}{{"malformed", "{", 4096}, {"trailing", "[] {}", 4096}, {"oversized", "[]", 1}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 200, tc.body, "", ""), nil })
			source, err := NewGitHubSource(doer, releasesEndpoint, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.ListReleases(context.Background()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestGitHubCandidateLimitExhaustionIsExplicit(t *testing.T) {
	t.Parallel()
	body := `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[]},{"tag_name":"v0.9.0","draft":false,"prerelease":false,"assets":[]}]`
	doer := doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 200, body, "", ""), nil })
	source, err := NewGitHubSourceWithLimits(doer, releasesEndpoint, GitHubLimits{MaxCandidates: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.ListReleases(context.Background()); err == nil || !strings.Contains(err.Error(), "candidate limit") {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubMalformedReleaseCasesAreIndependent(t *testing.T) {
	t.Parallel()
	cases := []string{`[{"tag_name":"pasture-stable","draft":false,"prerelease":false,"assets":[]}]`, `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"pasture-stable-1.0.0.tgz","browser_download_url":"https://objects.githubusercontent.com/a","size":1,"state":"uploaded"}]}]`, `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a","browser_download_url":"https://objects.githubusercontent.com/a","size":1,"state":"uploaded"},{"name":"a","browser_download_url":"https://objects.githubusercontent.com/b","size":1,"state":"uploaded"}]}]`}
	for i, body := range cases {
		body := body
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 200, body, "", ""), nil })
			source, _ := NewGitHubSource(doer, releasesEndpoint, 4096)
			releases, err := source.ListReleases(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(releases) != 0 {
				t.Fatal("malformed release accepted")
			}
		})
	}
}

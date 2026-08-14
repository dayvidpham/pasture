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
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
)

const releasesEndpoint = "https://api.github.com/repos/dayvidpham/pasture/releases"

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type boundaryBody struct {
	io.Reader
	closeErr   error
	closeCalls *int
}

func (b *boundaryBody) Close() error {
	if b.closeCalls != nil {
		(*b.closeCalls)++
	}
	return b.closeErr
}
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
	source, err := newGitHubSource(doer, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := source.listReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(releases) != 2 || releases[1].version.String() != "1.2.0" {
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
		assets = append(assets, githubAsset{Name: name, URL: "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/" + name, Size: int64(len(files[name])), State: "uploaded"})
	}
	pageTwo, _ := json.Marshal([]githubRelease{{TagName: "v1.2.0", Assets: assets}})
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "github.com" {
			return response(req, 200, string(files[req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]]), "", ""), nil
		}
		if req.URL.Query().Get("page") == "2" {
			return response(req, 200, string(pageTwo), "", ""), nil
		}
		next := releasesEndpoint + "?page=2&per_page=100"
		return response(req, 200, `[{"tag_name":"v2.0.0","draft":true,"prerelease":false,"assets":[]}]`, "<"+next+">; rel=\"next\"", ""), nil
	})
	source, err := newGitHubSource(doer, releasesEndpoint, 1<<20)
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
			source, err := newGitHubSourceWithLimits(doer, releasesEndpoint, tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.listReleases(context.Background()); err == nil {
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
	source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
	_, err := source.listReleases(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestGitHubCancellationClosesAcquiredResponseExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	closeCalls := 0
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusOK,
			Request:    req,
			Body:       &boundaryBody{Reader: strings.NewReader(`[]`), closeErr: os.ErrClosed, closeCalls: &closeCalls},
		}, nil
	})
	source, err := newGitHubSource(doer, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := catalog.ListCompatible(ctx, installerVersion(t), FinalsOnly)
	if candidates != nil || !errors.Is(err, context.Canceled) || closeCalls != 1 {
		t.Fatalf("candidates=%v err=%v close calls=%d", candidates, err, closeCalls)
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
			source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
			var err error
			if tc.asset {
				_, err = source.openAsset(context.Background(), exactTestAsset("https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file"))
			} else {
				_, err = source.listReleases(context.Background())
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
			source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
			var err error
			if asset {
				_, err = source.openAsset(context.Background(), exactTestAsset("https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file"))
			} else {
				_, err = source.listReleases(context.Background())
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
	source, err := newGitHubSource(doer, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.listReleases(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader, err := source.openAsset(context.Background(), exactTestAsset("https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file"))
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
		doer httpDoer
	}{{"transport", doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}, {"nil", doerFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}, {"nil body", doerFunc(func(r *http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Request: r}, nil })}, {"status", doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 503, "", "", ""), nil })}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, _ := newGitHubSource(tc.doer, releasesEndpoint, 4096)
			if _, err := source.listReleases(context.Background()); err == nil {
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

func TestProductionConstructorRejectsCallerPaginationState(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"?page=2", "?page=1&page=2", "?per_page=1", "?state=all"} {
		if _, err := NewGitHubSource(http.DefaultClient, releasesEndpoint+query, 4096); err == nil {
			t.Fatalf("caller query %q accepted", query)
		}
	}
}

func TestAssetTrustUsesExplicitReleaseHostsAndDownloadPaths(t *testing.T) {
	t.Parallel()
	doer := doerFunc(func(req *http.Request) (*http.Response, error) { return response(req, 200, "asset", "", ""), nil })
	source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
	approved := []string{"https://github.com/dayvidpham/pasture/releases/download/v1.2.0/pasture.tgz"}
	for _, location := range approved {
		reader, err := source.openAsset(context.Background(), exactTestAsset(location))
		if err != nil {
			t.Fatalf("approved %s: %v", location, err)
		}
		reader.Close()
	}
	rejected := []string{
		"https://release-assets.githubusercontent.com/github-production-release-asset/123/file",
		"https://objects.githubusercontent.com/github-production-release-asset/123/file",
		"https://raw.githubusercontent.com/dayvidpham/pasture/main/file",
		"https://gist.githubusercontent.com/user/id/raw/file",
		"https://avatars.githubusercontent.com/u/1",
		"https://evil.githubusercontent.com/file",
		"https://github.com/dayvidpham/pasture/raw/main/file",
		"https://github.com/dayvidpham/pasture/releases/tag/v1.2.0",
		"https://github.com/other/pasture/releases/download/v1.2.0/file",
		"https://github.com/dayvidpham/other/releases/download/v1.2.0/file",
		"https://github.com/dayvidpham/pasture/releases/download/v1.1.0/file",
		"https://github.com/dayvidpham/pasture/releases/download/latest/file",
		"https://github.com/dayvidpham/pasture/releases/download/pasture-stable/file",
		"https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file?x=1",
		"https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file#x",
		"https://github.com/dayvidpham/pasture/releases/download/v1.2.0/%66ile",
	}
	for _, location := range rejected {
		if _, err := source.openAsset(context.Background(), exactTestAsset(location)); err == nil {
			t.Fatalf("hostile asset location accepted: %s", location)
		}
	}
}

func TestManualRedirectLocationTrustBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, initial, location string
		wantErr                 bool
	}{
		{"approved asset redirect", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://release-assets.githubusercontent.com/github-production-release-asset/123/file", false},
		{"hostile sibling service", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://raw.githubusercontent.com/dayvidpham/pasture/main/file", true},
		{"wrong github path", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://github.com/dayvidpham/pasture/raw/main/file", true},
		{"cross repository", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://github.com/other/repo/releases/download/v1.2.0/file", true},
		{"cross version", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://github.com/dayvidpham/pasture/releases/download/v1.1.0/file", true},
		{"wrong basename", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file", "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/other", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			doer := doerFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					resp := response(req, http.StatusFound, "", "", "")
					resp.Header.Set("Location", test.location)
					return resp, nil
				}
				return response(req, http.StatusOK, "asset", "", ""), nil
			})
			source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
			reader, err := source.openAsset(context.Background(), exactTestAsset(test.initial))
			if reader != nil {
				reader.Close()
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestProductionClientCannotHideForbiddenIntermediateRedirect(t *testing.T) {
	t.Parallel()
	exact := "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/file"
	forbidden := "https://github.com/other/pasture/releases/download/v1.2.0/file"
	approvedCDN := "https://release-assets.githubusercontent.com/github-production-release-asset/123/file"
	requests := make([]string, 0, 3)
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		result := response(req, http.StatusFound, "", "", "")
		switch req.URL.String() {
		case exact:
			result.Header.Set("Location", forbidden)
		case forbidden:
			result.Header.Set("Location", approvedCDN)
		default:
			result.StatusCode = http.StatusOK
		}
		return result, nil
	})
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return nil
		},
	}
	source, err := NewGitHubSource(client, releasesEndpoint, 4096)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := source.openAsset(context.Background(), exactTestAsset(exact))
	if reader != nil {
		reader.Close()
		t.Fatal("forbidden intermediate redirect returned a body")
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Stage != "GitHub redirect" || typed.Location != forbidden {
		t.Fatalf("typed=%v err=%v", typed, err)
	}
	if len(requests) != 1 || requests[0] != exact {
		t.Fatalf("redirect policy allowed hidden hops: %v", requests)
	}
}

func TestManualCatalogRedirectRejectsHostileLocation(t *testing.T) {
	t.Parallel()
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		resp := response(req, http.StatusFound, "", "", "")
		resp.Header.Set("Location", "https://evil.example/repos/dayvidpham/pasture/releases")
		return resp, nil
	})
	source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
	if _, err := source.listReleases(context.Background()); err == nil {
		t.Fatal("hostile catalog 3xx accepted")
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
			source, err := newGitHubSource(doer, releasesEndpoint, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.listReleases(context.Background()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestGitHubCandidateLimitExhaustionIsExplicit(t *testing.T) {
	t.Parallel()
	body := `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[]},{"tag_name":"v0.9.0","draft":false,"prerelease":false,"assets":[]}]`
	doer := doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 200, body, "", ""), nil })
	source, err := newGitHubSourceWithLimits(doer, releasesEndpoint, GitHubLimits{MaxCandidates: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.listReleases(context.Background()); err == nil || !strings.Contains(err.Error(), "candidate limit") {
		t.Fatalf("err=%v", err)
	}
}

func TestGitHubMalformedReleaseCasesAreIndependent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		skipped    bool
	}{
		{"moving tag", `[{"tag_name":"pasture-stable","draft":false,"prerelease":false,"assets":[]}]`, true},
		{"moving asset", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"pasture-stable-1.0.0.tgz","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/pasture-stable-1.0.0.tgz","size":1,"state":"uploaded"}]}]`, false},
		{"empty name", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a","size":1,"state":"uploaded"}]}]`, false},
		{"path name", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a/b","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a/b","size":1,"state":"uploaded"}]}]`, false},
		{"negative size", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a","size":-1,"state":"uploaded"}]}]`, false},
		{"wrong state", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a","size":1,"state":"new"}]}]`, false},
		{"wrong URL", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a","browser_download_url":"https://raw.githubusercontent.com/o/r/main/a","size":1,"state":"uploaded"}]}]`, false},
		{"duplicate", `[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"a","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a","size":1,"state":"uploaded"},{"name":"a","browser_download_url":"https://github.com/dayvidpham/pasture/releases/download/v1.0.0/a","size":1,"state":"uploaded"}]}]`, false},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(r *http.Request) (*http.Response, error) { return response(r, 200, test.body, "", ""), nil })
			source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
			releases, err := source.listReleases(context.Background())
			if test.skipped && err != nil {
				t.Fatal(err)
			}
			if !test.skipped && err == nil {
				t.Fatal("expected typed malformed-asset error")
			}
			if !test.skipped {
				var typed *Error
				if !errors.As(err, &typed) || typed.Stage != "GitHub release decoding" || typed.Location == "" {
					t.Fatalf("typed=%v err=%v", typed, err)
				}
			}
			if len(releases) != 0 {
				t.Fatal("malformed release accepted")
			}
		})
	}
}

func TestMalformedHistoricalReleaseDoesNotHideValidRelease(t *testing.T) {
	t.Parallel()
	body := `[{"tag_name":"v0.9.0","draft":false,"prerelease":false,"assets":[{"name":"bad","browser_download_url":"https://raw.githubusercontent.com/o/r/main/bad","size":1,"state":"uploaded"}]},{"tag_name":"v1.2.0","draft":false,"prerelease":false,"assets":[]}]`
	doer := doerFunc(func(req *http.Request) (*http.Response, error) { return response(req, 200, body, "", ""), nil })
	source, _ := newGitHubSource(doer, releasesEndpoint, 4096)
	releases, err := source.listReleases(context.Background())
	if err != nil || len(releases) != 1 || releases[0].version.String() != "1.2.0" {
		t.Fatalf("releases=%v err=%v", releases, err)
	}
}

func TestProductionFailureBoundaryMatrixReturnsNoVerifiedOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mutate     func(map[string][]byte)
		faultAsset string
		fault      string
	}{
		{"manifest unknown field", func(files map[string][]byte) {
			files[artifact.AggregateManifestAsset] = []byte(strings.Replace(string(files[artifact.AggregateManifestAsset]), "{", `{"unknown":true,`, 1))
			files[artifact.AggregateChecksumAsset] = artifact.AggregateManifestChecksum(files[artifact.AggregateManifestAsset])
		}, "", ""},
		{"manifest duplicate field", func(files map[string][]byte) {
			files[artifact.AggregateManifestAsset] = []byte(strings.Replace(string(files[artifact.AggregateManifestAsset]), "{", `{"schema":"pasture.aggregate-release/v1",`, 1))
			files[artifact.AggregateChecksumAsset] = artifact.AggregateManifestChecksum(files[artifact.AggregateManifestAsset])
		}, "", ""},
		{"manifest trailing data", func(files map[string][]byte) {
			files[artifact.AggregateManifestAsset] = append(files[artifact.AggregateManifestAsset], []byte(" {}")...)
			files[artifact.AggregateChecksumAsset] = artifact.AggregateManifestChecksum(files[artifact.AggregateManifestAsset])
		}, "", ""},
		{"manifest case fold", func(files map[string][]byte) {
			files[artifact.AggregateManifestAsset] = []byte(strings.Replace(string(files[artifact.AggregateManifestAsset]), `"schema"`, `"Schema"`, 1))
			files[artifact.AggregateChecksumAsset] = artifact.AggregateManifestChecksum(files[artifact.AggregateManifestAsset])
		}, "", ""},
		{"sidecar empty", func(files map[string][]byte) { files[artifact.AggregateChecksumAsset] = nil }, "", ""},
		{"sidecar wrong filename", func(files map[string][]byte) {
			files[artifact.AggregateChecksumAsset] = []byte(strings.Replace(string(files[artifact.AggregateChecksumAsset]), artifact.AggregateManifestAsset, "other.json", 1))
		}, "", ""},
		{"sidecar uppercase", func(files map[string][]byte) {
			files[artifact.AggregateChecksumAsset] = []byte(strings.ToUpper(string(files[artifact.AggregateChecksumAsset])))
		}, "", ""},
		{"manifest transport", nil, artifact.AggregateManifestAsset, "transport"},
		{"manifest status", nil, artifact.AggregateManifestAsset, "status"},
		{"manifest nil body", nil, artifact.AggregateManifestAsset, "nil-body"},
		{"component transport", nil, "pasture-1.2.0-codex-hooks.tgz", "transport"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := loadGitHubFixtureFiles(t)
			if test.mutate != nil {
				test.mutate(files)
			}
			page := githubReleasePage(t, files)
			doer := doerFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "api.github.com" {
					return response(req, 200, page, "", ""), nil
				}
				name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
				if name == test.faultAsset {
					switch test.fault {
					case "transport":
						return nil, io.ErrUnexpectedEOF
					case "status":
						return response(req, 503, "", "", ""), nil
					case "nil-body":
						return &http.Response{StatusCode: 200, Request: req}, nil
					}
				}
				return response(req, 200, string(files[name]), "", ""), nil
			})
			source, _ := newGitHubSource(doer, releasesEndpoint, 1<<20)
			catalog, _ := New(source)
			var verified artifact.VerifiedAggregate
			candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
			if err == nil && len(candidates) == 1 {
				verified, err = catalog.ResolveCandidate(context.Background(), candidates[0], 0)
			}
			var typed *Error
			if err == nil || !errors.As(err, &typed) || typed.Stage == "" || typed.Location == "" || verified.Manifest().Version().String() != "" {
				t.Fatalf("verified=%v typed=%v err=%v", verified, typed, err)
			}
		})
	}
}

func TestExactProductionFailureBranches(t *testing.T) {
	t.Parallel()
	component := "pasture-1.2.0-codex-hooks.tgz"
	manifestURL := "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/" + artifact.AggregateManifestAsset
	componentURL := "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/" + component
	tests := []struct {
		name, target, fault, errorType, stage, location string
		delta                                           int64
		listing                                         bool
	}{
		{"manifest transport", artifact.AggregateManifestAsset, "transport", "catalog", "GitHub request", manifestURL, 0, true},
		{"manifest status", artifact.AggregateManifestAsset, "status", "catalog", "GitHub response", manifestURL, 0, true},
		{"manifest nil body", artifact.AggregateManifestAsset, "nil-body", "catalog", "GitHub response", manifestURL, 0, true},
		{"manifest read", artifact.AggregateManifestAsset, "read", "catalog", "candidate manifest read", artifact.AggregateManifestAsset, 0, true},
		{"manifest close", artifact.AggregateManifestAsset, "close", "catalog", "candidate manifest close", artifact.AggregateManifestAsset, 0, true},
		{"component transport", component, "transport", "catalog", "GitHub request", componentURL, 0, false},
		{"component status", component, "status", "catalog", "GitHub response", componentURL, 0, false},
		{"component nil body", component, "nil-body", "catalog", "GitHub response", componentURL, 0, false},
		{"component read", component, "read", "aggregate", "asset read", component, 0, false},
		{"component close", component, "close", "aggregate", "asset close", component, 0, false},
		{"declared undersize", component, "", "aggregate", "asset read", component, -1, false},
		{"declared oversize", component, "", "aggregate", "asset read", component, 1, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := loadGitHubFixtureFiles(t)
			page := githubReleasePageWithSize(t, files, test.target, test.delta)
			doer := doerFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "api.github.com" {
					return response(req, 200, page, "", ""), nil
				}
				name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
				if name == test.target {
					switch test.fault {
					case "transport":
						return nil, io.ErrUnexpectedEOF
					case "status":
						return response(req, 503, "", "", ""), nil
					case "nil-body":
						return &http.Response{StatusCode: 200, Request: req}, nil
					case "read":
						return &http.Response{StatusCode: 200, Request: req, Body: &boundaryBody{Reader: errorReader{err: io.ErrUnexpectedEOF}}}, nil
					case "close":
						return &http.Response{StatusCode: 200, Request: req, Body: &boundaryBody{Reader: strings.NewReader(string(files[name])), closeErr: os.ErrClosed}}, nil
					}
				}
				return response(req, 200, string(files[name]), "", ""), nil
			})
			source, _ := newGitHubSource(doer, releasesEndpoint, 1<<20)
			catalog, _ := New(source)
			var verified artifact.VerifiedAggregate
			candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
			if test.listing {
				if candidates != nil {
					t.Fatalf("listing fault returned candidates: %v", candidates)
				}
			} else {
				if err != nil || len(candidates) != 1 {
					t.Fatalf("resolution fault selected candidates=%v err=%v", candidates, err)
				}
				verified, err = catalog.ResolveCandidate(context.Background(), candidates[0], 0)
			}
			if !test.listing {
				assertZeroVerifiedAggregate(t, verified)
			}
			if test.errorType == "catalog" {
				var typed *Error
				if !errors.As(err, &typed) || typed.Stage != test.stage || typed.Location != test.location {
					t.Fatalf("typed=%v err=%v", typed, err)
				}
			} else {
				var typed *artifact.AggregateValidationError
				if !errors.As(err, &typed) || typed.Stage != test.stage || typed.Field != test.location {
					t.Fatalf("typed=%v err=%v", typed, err)
				}
			}
		})
	}

	t.Run("success control", func(t *testing.T) {
		files := loadGitHubFixtureFiles(t)
		page := githubReleasePage(t, files)
		doer := doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "api.github.com" {
				return response(req, 200, page, "", ""), nil
			}
			name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			return response(req, 200, string(files[name]), "", ""), nil
		})
		source, _ := newGitHubSource(doer, releasesEndpoint, 1<<20)
		catalog, _ := New(source)
		candidates, err := catalog.ListCompatible(context.Background(), installerVersion(t), FinalsOnly)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("candidates=%v err=%v", candidates, err)
		}
		verified, err := catalog.ResolveCandidate(context.Background(), candidates[0], 0)
		if err != nil || verified.Manifest().Version().String() != "1.2.0" {
			t.Fatalf("verified=%v err=%v", verified, err)
		}
	})
}

func assertZeroVerifiedAggregate(t *testing.T, verified artifact.VerifiedAggregate) {
	t.Helper()
	if !reflect.DeepEqual(verified, artifact.VerifiedAggregate{}) {
		t.Fatalf("verified aggregate is not the exact zero value: %v", verified)
	}
	if verified.Manifest().Version().String() != "" {
		t.Fatalf("verified output returned: %v", verified)
	}
	for _, id := range artifact.ComponentIDs() {
		if _, ok := verified.Asset(id); ok {
			t.Fatalf("verified asset %s returned", id)
		}
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func loadGitHubFixtureFiles(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if !entry.IsDir() {
			files[entry.Name()], err = os.ReadFile(filepath.Join("testdata", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return files
}

func githubReleasePage(t *testing.T, files map[string][]byte) string {
	return githubReleasePageWithSize(t, files, "", 0)
}

func githubReleasePageWithSize(t *testing.T, files map[string][]byte, target string, delta int64) string {
	t.Helper()
	manifest, err := artifact.ParseAggregateManifest(loadGitHubFixtureFiles(t)[artifact.AggregateManifestAsset])
	if err != nil {
		t.Fatal(err)
	}
	names := []string{artifact.AggregateManifestAsset, artifact.AggregateChecksumAsset}
	for _, component := range manifest.Components() {
		names = append(names, component.Asset())
	}
	assets := make([]githubAsset, 0, len(names))
	for _, name := range names {
		size := int64(len(files[name]))
		if name == target {
			size += delta
		}
		assets = append(assets, githubAsset{Name: name, URL: "https://github.com/dayvidpham/pasture/releases/download/v1.2.0/" + name, Size: size, State: "uploaded"})
	}
	encoded, err := json.Marshal([]githubRelease{{TagName: "v1.2.0", Assets: assets}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func exactTestAsset(location string) asset {
	name := "file"
	if strings.Contains(location, "pasture.tgz") {
		name = "pasture.tgz"
	}
	return asset{name: name, downloadURL: location, size: 5, identity: assetIdentity{repository: repositoryIdentity{owner: "dayvidpham", name: "pasture"}, tag: "v1.2.0", name: name}}
}

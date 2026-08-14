package releasecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

const (
	defaultMaxCatalogBytes      int64 = 32 << 20
	defaultMaxCatalogPages            = 10
	defaultMaxCatalogCandidates       = 500
	githubPageSize                    = 100
)

// HTTPDoer injects the GitHub HTTP transport while production URL validation remains active.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// GitHubLimits bound complete catalog discovery across every page.
type GitHubLimits struct {
	MaxPages      int
	MaxCandidates int
	MaxBytes      int64
}

type requestPurpose uint8

const (
	purposeCatalog requestPurpose = iota + 1
	purposeAsset
)

// GitHubSource is the production GitHub Releases API and asset boundary.
type GitHubSource struct {
	client      HTTPDoer
	releasesURL *url.URL
	limits      GitHubLimits
}

func NewGitHubSource(client HTTPDoer, releasesURL string, maxCatalogBytes int64) (*GitHubSource, error) {
	return NewGitHubSourceWithLimits(client, releasesURL, GitHubLimits{MaxBytes: maxCatalogBytes})
}

// NewGitHubSourceWithLimits configures bounded discovery through the production trust boundary.
func NewGitHubSourceWithLimits(client HTTPDoer, releasesURL string, limits GitHubLimits) (*GitHubSource, error) {
	if client == nil {
		return nil, invalid("GitHub source construction", "HTTP client", "the HTTP client is nil", "GitHub releases cannot be loaded", "inject an HTTP client with explicit timeouts", fs.ErrInvalid)
	}
	u, err := url.Parse(releasesURL)
	if err != nil || !validCatalogURL(u, nil) {
		return nil, invalid("GitHub source construction", "releases URL", fmt.Sprintf("%q is not an HTTPS api.github.com repository Releases URL", releasesURL), "the release endpoint cannot be trusted", "use https://api.github.com/repos/<owner>/<repository>/releases", fs.ErrInvalid)
	}
	if limits.MaxPages == 0 {
		limits.MaxPages = defaultMaxCatalogPages
	}
	if limits.MaxCandidates == 0 {
		limits.MaxCandidates = defaultMaxCatalogCandidates
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaultMaxCatalogBytes
	}
	if limits.MaxPages < 1 || limits.MaxCandidates < 1 || limits.MaxBytes < 1 || limits.MaxBytes == math.MaxInt64 {
		return nil, invalid("GitHub source construction", "limits", "page, candidate, and byte limits must all be positive", "catalog discovery cannot be bounded", "provide positive GitHubLimits or zero-valued defaults", fs.ErrInvalid)
	}
	base := *u
	query := base.Query()
	query.Set("per_page", strconv.Itoa(githubPageSize))
	base.RawQuery = query.Encode()
	if httpClient, ok := client.(*http.Client); ok {
		clone := *httpClient
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &clone
	}
	return &GitHubSource{client: client, releasesURL: &base, limits: limits}, nil
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name  string `json:"name"`
	URL   string `json:"browser_download_url"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

func (g *GitHubSource) ListReleases(ctx context.Context) ([]Release, error) {
	next := cloneURL(g.releasesURL)
	visited := map[string]bool{}
	releases := make([]Release, 0)
	var totalBytes int64
	totalCandidates := 0
	for page := 1; next != nil; page++ {
		if err := ctx.Err(); err != nil {
			return nil, invalid("GitHub pagination", next.String(), fmt.Sprintf("context ended before page %d: %v", page, err), "catalog discovery is incomplete", "retry with a live context", err)
		}
		if page > g.limits.MaxPages {
			return nil, limitError("page", g.limits.MaxPages)
		}
		canonical := next.String()
		if visited[canonical] {
			return nil, invalid("GitHub pagination", canonical, "the next link forms a cycle", "catalog completeness cannot be established", "repair the GitHub Link chain", fs.ErrInvalid)
		}
		visited[canonical] = true
		response, err := g.get(ctx, next, purposeCatalog)
		if err != nil {
			return nil, err
		}
		remaining := g.limits.MaxBytes - totalBytes
		body, readErr := io.ReadAll(io.LimitReader(response.Body, remaining+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			cause := readErr
			if cause == nil {
				cause = closeErr
			}
			return nil, invalid("GitHub release listing", canonical, fmt.Sprintf("page %d body could not be read and closed: %v", page, cause), "catalog discovery is incomplete", "repair the response body and retry", cause)
		}
		if int64(len(body)) > remaining {
			return nil, limitError("cumulative byte", g.limits.MaxBytes)
		}
		totalBytes += int64(len(body))
		wire, err := decodeReleasePage(body, canonical)
		if err != nil {
			return nil, err
		}
		if len(wire) > g.limits.MaxCandidates-totalCandidates {
			return nil, limitError("candidate", g.limits.MaxCandidates)
		}
		totalCandidates += len(wire)
		for _, item := range wire {
			release, ok := decodeRelease(item)
			if ok {
				releases = append(releases, release)
			}
		}
		next, err = parseNextLink(response.Header.Get("Link"), g.releasesURL)
		if err != nil {
			return nil, err
		}
	}
	return releases, nil
}

func decodeReleasePage(body []byte, location string) ([]githubRelease, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var wire []githubRelease
	if err := dec.Decode(&wire); err != nil {
		return nil, invalid("GitHub release listing", location, fmt.Sprintf("response JSON is malformed: %v", err), "release selection is unavailable", "serve one valid GitHub Releases array", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, invalid("GitHub release listing", location, "response contains trailing JSON data", "release selection is ambiguous", "serve exactly one GitHub Releases array", fs.ErrInvalid)
	}
	return wire, nil
}

func decodeRelease(item githubRelease) (Release, bool) {
	if item.Draft || !strings.HasPrefix(item.TagName, "v") || strings.Contains(item.TagName, "pasture-stable") {
		return Release{}, false
	}
	version, err := artifact.ParseVersion(strings.TrimPrefix(item.TagName, "v"))
	if err != nil {
		return Release{}, false
	}
	assets := make(map[string]Asset, len(item.Assets))
	for _, raw := range item.Assets {
		parsed, e := url.Parse(raw.URL)
		if raw.Name == "" || strings.Contains(raw.Name, "/") || strings.Contains(raw.Name, "pasture-stable") || raw.Size < 0 || raw.State != "uploaded" || e != nil || !validAssetURL(parsed) || assets[raw.Name].name != "" {
			return Release{}, false
		}
		assets[raw.Name] = Asset{name: raw.Name, downloadURL: raw.URL, size: raw.Size}
	}
	return Release{version: version, prerelease: item.Prerelease, assets: assets}, true
}

func parseNextLink(header string, base *url.URL) (*url.URL, error) {
	if strings.TrimSpace(header) == "" {
		return nil, nil
	}
	var next *url.URL
	for _, part := range strings.Split(header, ",") {
		sections := strings.Split(strings.TrimSpace(part), ";")
		if len(sections) < 2 {
			return nil, invalid("GitHub pagination", "Link", "malformed Link entry", "catalog completeness cannot be established", "provide RFC 8288 links", fs.ErrInvalid)
		}
		target := strings.TrimSpace(sections[0])
		isNext := false
		for _, parameter := range sections[1:] {
			if strings.TrimSpace(parameter) == `rel="next"` {
				isNext = true
			}
		}
		if !isNext {
			continue
		}
		if next != nil {
			return nil, invalid("GitHub pagination", "Link", "more than one next relation was supplied", "pagination is ambiguous", "supply exactly one next link", fs.ErrInvalid)
		}
		if len(target) < 3 || target[0] != '<' || target[len(target)-1] != '>' {
			return nil, invalid("GitHub pagination", "Link", "next target is not angle-bracketed", "pagination is ambiguous", "use <https://...>; rel=\"next\"", fs.ErrInvalid)
		}
		parsed, err := url.Parse(target[1 : len(target)-1])
		if err != nil || !validCatalogURL(parsed, base) {
			return nil, invalid("GitHub pagination", target, "next URL leaves the trusted repository Releases boundary", "catalog bytes could come from an untrusted endpoint", "use the same HTTPS api.github.com repository Releases path", fs.ErrPermission)
		}
		next = parsed
	}
	return next, nil
}

func (g *GitHubSource) OpenURL(ctx context.Context, assetURL string) (io.ReadCloser, error) {
	u, err := url.Parse(assetURL)
	if err != nil || !validAssetURL(u) {
		return nil, invalid("GitHub asset request", assetURL, "asset URL is outside approved HTTPS GitHub asset hosts", "component bytes are untrusted", "use an approved immutable GitHub asset URL", fs.ErrPermission)
	}
	response, err := g.get(ctx, u, purposeAsset)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

func (g *GitHubSource) get(ctx context.Context, endpoint *url.URL, purpose requestPurpose) (*http.Response, error) {
	if !validPurposeURL(endpoint, purpose, g.releasesURL) {
		return nil, invalid("GitHub request", endpoint.String(), "URL does not match its typed request purpose", "release data is untrusted", "use the catalog API or approved asset host for the matching operation", fs.ErrPermission)
	}
	current := cloneURL(endpoint)
	for redirects := 0; redirects <= 5; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, invalid("GitHub request construction", endpoint.String(), fmt.Sprintf("GET could not be created: %v", err), "release data cannot be loaded", "provide a live context and valid URL", err)
		}
		req.Header.Set("Accept", map[requestPurpose]string{purposeCatalog: "application/vnd.github+json", purposeAsset: "application/octet-stream"}[purpose])
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := g.client.Do(req)
		if err != nil {
			return nil, invalid("GitHub request", current.String(), fmt.Sprintf("GET failed: %v", err), "release data cannot be loaded", "repair connectivity and retry", err)
		}
		if response == nil || response.Body == nil {
			return nil, invalid("GitHub response", endpoint.String(), "transport returned no response body", "release data cannot be validated", "repair the HTTP transport", fs.ErrInvalid)
		}
		if response.Request == nil || response.Request.URL == nil {
			response.Body.Close()
			return nil, invalid("GitHub response", current.String(), "transport omitted the final request URL", "redirect trust cannot be validated", "return the exact final request URL from HTTPDoer", fs.ErrPermission)
		}
		final := response.Request.URL
		if !validPurposeURL(final, purpose, g.releasesURL) {
			response.Body.Close()
			return nil, invalid("GitHub redirect", final.String(), "final redirect destination violates the typed request trust boundary", "release data is untrusted", "keep catalog redirects on api.github.com and assets on approved GitHub asset hosts", fs.ErrPermission)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			response.Body.Close()
			next, err := current.Parse(location)
			if err != nil || !validPurposeURL(next, purpose, g.releasesURL) {
				return nil, invalid("GitHub redirect", location, "redirect target violates the typed request trust boundary", "release data is untrusted", "keep every redirect within the approved purpose host/path", fs.ErrPermission)
			}
			current = next
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, invalid("GitHub response", endpoint.String(), fmt.Sprintf("GET returned HTTP %d", response.StatusCode), "release data cannot be loaded", "repair repository access, rate limits, or asset availability", fs.ErrPermission)
		}
		return response, nil
	}
	return nil, invalid("GitHub redirect", endpoint.String(), "redirect limit exceeded", "release data could not be obtained safely", "reduce redirects to at most five", fs.ErrInvalid)
}

func validPurposeURL(u *url.URL, p requestPurpose, base *url.URL) bool {
	if p == purposeCatalog {
		return validCatalogURL(u, base)
	}
	return validAssetURL(u)
}
func validCatalogURL(u, base *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.Fragment != "" {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[1] != "repos" || parts[2] == "" || parts[3] == "" || parts[4] != "releases" {
		return false
	}
	if base != nil && u.Path != base.Path {
		return false
	}
	for key := range u.Query() {
		if key != "page" && key != "per_page" {
			return false
		}
	}
	if perPage := u.Query().Get("per_page"); perPage != "" && perPage != strconv.Itoa(githubPageSize) {
		return false
	}
	if page := u.Query().Get("page"); page != "" {
		n, err := strconv.Atoi(page)
		if err != nil || n < 1 {
			return false
		}
	}
	return true
}
func validAssetURL(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && (u.Host == "github.com" || u.Host == "objects.githubusercontent.com" || strings.HasSuffix(u.Host, ".githubusercontent.com"))
}
func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	copy := *u
	return &copy
}
func limitError(kind string, limit any) error {
	return invalid("GitHub pagination", kind, fmt.Sprintf("the configured %s limit %v was exhausted before the catalog ended", kind, limit), "catalog discovery is explicitly incomplete and no release may be selected", "raise the reviewed bound or reduce repository release history", fs.ErrInvalid)
}

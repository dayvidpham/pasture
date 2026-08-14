package releasecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/dayvidpham/pasture/artifact"
)

const defaultMaxCatalogBytes int64 = 8 << 20

// HTTPDoer injects the GitHub HTTP transport.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// GitHubSource is the production GitHub Releases API and asset boundary.
type GitHubSource struct {
	client          HTTPDoer
	releasesURL     string
	maxCatalogBytes int64
}

// NewGitHubSource configures a typed GitHub Releases source. releasesURL is injectable for offline servers.
func NewGitHubSource(client HTTPDoer, releasesURL string, maxCatalogBytes int64) (*GitHubSource, error) {
	if client == nil {
		return nil, invalid("GitHub source construction", "HTTP client", "the HTTP client is nil", "GitHub releases cannot be loaded", "inject an HTTP client with explicit timeouts", fs.ErrInvalid)
	}
	u, err := url.Parse(releasesURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, invalid("GitHub source construction", "releases URL", fmt.Sprintf("%q is not an absolute HTTP(S) URL", releasesURL), "the release endpoint cannot be contacted safely", "provide the repository's absolute GitHub releases API URL", err)
	}
	if u.Scheme != "https" || u.Hostname() != "api.github.com" {
		return nil, invalid("GitHub source construction", "releases URL", fmt.Sprintf("endpoint %q is not the HTTPS api.github.com boundary", releasesURL), "the release endpoint cannot be trusted", "use the repository's HTTPS api.github.com Releases URL", fs.ErrInvalid)
	}
	if maxCatalogBytes <= 0 {
		maxCatalogBytes = defaultMaxCatalogBytes
	}
	return &GitHubSource{client, releasesURL, maxCatalogBytes}, nil
}

func newTestGitHubSource(client HTTPDoer, releasesURL string, maxCatalogBytes int64) *GitHubSource {
	if maxCatalogBytes <= 0 {
		maxCatalogBytes = defaultMaxCatalogBytes
	}
	return &GitHubSource{client: client, releasesURL: releasesURL, maxCatalogBytes: maxCatalogBytes}
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
	response, err := g.get(ctx, g.releasesURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, g.maxCatalogBytes+1))
	if err != nil {
		return nil, invalid("GitHub release listing", g.releasesURL, fmt.Sprintf("response body could not be read: %v", err), "release selection is unavailable", "repair the network response and retry", err)
	}
	if int64(len(body)) > g.maxCatalogBytes {
		return nil, invalid("GitHub release listing", g.releasesURL, "response exceeded the configured catalog limit", "an unbounded catalog was rejected", "reduce the page size or raise an explicitly reviewed limit", fs.ErrInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var wire []githubRelease
	if err := dec.Decode(&wire); err != nil {
		return nil, invalid("GitHub release listing", g.releasesURL, fmt.Sprintf("response JSON is malformed: %v", err), "release selection is unavailable", "serve a valid GitHub Releases array", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, invalid("GitHub release listing", g.releasesURL, "response contains trailing JSON data", "release selection is ambiguous", "serve exactly one GitHub Releases array", fs.ErrInvalid)
	}
	var releases []Release
	for i, item := range wire {
		if item.Draft {
			continue
		}
		if !strings.HasPrefix(item.TagName, "v") || item.TagName == "pasture-stable" {
			continue
		}
		version, err := artifact.ParseVersion(strings.TrimPrefix(item.TagName, "v"))
		if err != nil {
			continue
		}
		assets := make(map[string]Asset, len(item.Assets))
		valid := true
		for j, raw := range item.Assets {
			parsed, parseErr := url.Parse(raw.URL)
			if raw.Name == "" || strings.Contains(raw.Name, "/") || strings.Contains(raw.Name, "pasture-stable") || raw.Size < 0 || raw.State != "uploaded" || parseErr != nil || parsed.Scheme != "https" || !approvedGitHubAssetHost(parsed.Hostname()) || assets[raw.Name].name != "" {
				valid = false
				_ = i
				_ = j
				break
			}
			assets[raw.Name] = Asset{name: raw.Name, downloadURL: raw.URL, size: raw.Size}
		}
		if valid {
			releases = append(releases, Release{version: version, prerelease: item.Prerelease, assets: assets})
		}
	}
	return releases, nil
}

func approvedGitHubAssetHost(host string) bool {
	return host == "github.com" || host == "objects.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func (g *GitHubSource) OpenURL(ctx context.Context, assetURL string) (io.ReadCloser, error) {
	response, err := g.get(ctx, assetURL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}
func (g *GitHubSource) get(ctx context.Context, endpoint, accept string) (*http.Response, error) {
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || (u.Scheme != "https" && !strings.HasPrefix(g.releasesURL, "http://127.0.0.1:") && !strings.HasPrefix(g.releasesURL, "http://[::1]:")) {
		return nil, invalid("GitHub request", endpoint, "endpoint is not HTTPS", "release bytes cannot be trusted", "use an approved HTTPS GitHub endpoint", fs.ErrPermission)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, invalid("GitHub request construction", endpoint, fmt.Sprintf("GET request could not be created: %v", err), "release data cannot be loaded", "provide a valid endpoint and live context", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := g.client.Do(req)
	if err != nil {
		return nil, invalid("GitHub request", endpoint, fmt.Sprintf("GET failed: %v", err), "release data cannot be loaded", "repair connectivity or credentials and retry", err)
	}
	if response == nil || response.Body == nil {
		return nil, invalid("GitHub response", endpoint, "the HTTP transport returned no response body", "release data cannot be validated", "repair the injected HTTP transport", fs.ErrInvalid)
	}
	if response.Request != nil && response.Request.URL != nil && response.Request.URL.Scheme != u.Scheme {
		response.Body.Close()
		return nil, invalid("GitHub response", endpoint, "redirect changed the transport scheme", "release bytes cannot be trusted", "disable downgrade redirects and use HTTPS throughout", fs.ErrPermission)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, invalid("GitHub response", endpoint, fmt.Sprintf("GET returned HTTP %d", response.StatusCode), "release data cannot be trusted or loaded", "confirm repository access, rate limits, and release asset availability", fs.ErrPermission)
	}
	return response, nil
}

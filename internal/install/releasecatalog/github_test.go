package releasecatalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubSourceRejectsMalformedAssetsAndMovingAlias(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"tag_name":"pasture-stable","draft":false,"prerelease":false,"assets":[]},{"tag_name":"v1.2.0","draft":false,"prerelease":false,"assets":[{"name":"bad/name","browser_download_url":%q,"size":1,"state":"uploaded"}]}]`, serverURL(r))
	}))
	defer server.Close()
	source := newTestGitHubSource(server.Client(), server.URL, 4096)
	releases, err := source.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("got %d releases, want moving alias and malformed release rejected", len(releases))
	}
}

func serverURL(r *http.Request) string { return "http://" + r.Host + "/asset" }

func TestProductionGitHubSourceRequiresHTTPSGitHub(t *testing.T) {
	t.Parallel()
	if _, err := NewGitHubSource(http.DefaultClient, "http://api.github.com/repos/x/y/releases", 1); err == nil {
		t.Fatal("expected HTTP rejection")
	}
	if _, err := NewGitHubSource(http.DefaultClient, "https://example.com/releases", 1); err == nil {
		t.Fatal("expected unrelated host rejection")
	}
}

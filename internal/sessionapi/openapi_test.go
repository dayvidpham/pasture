package sessionapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/sessionapi"
)

func TestOpenAPIIsFresh(t *testing.T) {
	t.Parallel()
	path := filepath.Join("openapi", "pasture-local-api-"+sessionapi.PastureLocalAPIVersion+".json")
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run `make generate` after changing the API contract")
	document := string(sessionapi.OpenAPIBytes())
	require.Equal(t, string(want), document,
		"the committed OpenAPI artifact is stale; run `make generate`")
	require.Contains(t, document, "session and epoch API",
		"the API description must name the write surface; bind/unbind are not read-only")
	require.NotContains(t, document, "read API",
		"the API is not read-only; bind/unbind are writes")
}

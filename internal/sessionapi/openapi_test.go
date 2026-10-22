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
	require.Equal(t, string(want), string(sessionapi.OpenAPIBytes()),
		"the committed OpenAPI artifact is stale; run `make generate`")
}

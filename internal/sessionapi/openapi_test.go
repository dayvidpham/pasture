package sessionapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/sessionapi"
)

// TestFrozenAPIDocumentStaysPreserved pins the plan's requirement that the
// previous surface is preserved rather than deleted on a version bump. The
// hash makes "frozen" real: a later edit cannot quietly rot the old document.
func TestFrozenAPIDocumentStaysPreserved(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("openapi", "pasture-local-api-0.6.0.json"))
	require.NoError(t, err, "the plan requires the previous API artifact to be preserved")
	sum := sha256.Sum256(raw)
	require.Equal(t, "52f209b22fdbec38a371d2de75542e70e4c7a906fb5929680f40b1f6cf03e08d", hex.EncodeToString(sum[:]),
		"a preserved API artifact is immutable; publish a new version instead of editing it")
}

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
	require.Contains(t, document, `"version": "0.7.0"`)
	require.Contains(t, document, `"StateBasis"`)
	require.Contains(t, document, `"lastPromptAt"`)
	require.Contains(t, document, `"AttributionValue"`)
	require.Contains(t, document, `/execution`)
	require.Contains(t, document, "/api/v1/settings/peasant")
	require.Contains(t, document, "PeasantSettingsRequest")
}

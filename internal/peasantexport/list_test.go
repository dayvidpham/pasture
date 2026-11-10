package peasantexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// listPeasant copies the shared fake CLI and places one catalog fixture beside
// that copy. Each test gets its own binary, so parallel catalog tests do not
// share a counter or a fixture.
func listPeasant(t *testing.T, payload string) (binary, countPath string) {
	t.Helper()
	src := fakePeasantBinary(t)
	dir := t.TempDir()
	binary = filepath.Join(dir, "peasant")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(binary, data, 0o700))
	require.NoError(t, os.WriteFile(binary+".list.json", []byte(payload), 0o600))
	return binary, binary + ".count"
}

func readCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(data)
}

func recordedArgs(t *testing.T, binary string) []string {
	t.Helper()
	data, err := os.ReadFile(binary + ".argv")
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal(data, &args))
	return args
}

func TestListSessionsParsesIDs(t *testing.T) {
	t.Parallel()
	binary, countPath := listPeasant(t, `[
		{"id":"ses_a","turns":3,"preview":"hello"},
		{"id":"ses_b","facets":["skill"]},
		{"id":"ses_a","turns":3}
	]`)
	source := &Source{Binary: binary, Timeout: 5 * time.Second}

	listed, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, listed.IDs)
	require.False(t, listed.Truncated, "a page shorter than the requested limit is complete")
	require.Equal(t, []string{
		"sessions", "list", "--json", "--limit", strconv.Itoa(sessionListLimit), "--include-subagents",
	}, recordedArgs(t, binary))
	require.Equal(t, 1, readCount(t, countPath))

	// The caller must not be able to mutate the cached catalog.
	listed.IDs[0] = "mutated"
	again, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, again.IDs)
	require.Equal(t, 1, readCount(t, countPath), "a second read inside the reuse window must not spawn Peasant")
}

func TestListSessionsEmptyPageIsCompleteAndCached(t *testing.T) {
	t.Parallel()
	binary, countPath := listPeasant(t, `[]`)
	source := &Source{Binary: binary, Timeout: 5 * time.Second}

	listed, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, listed.IDs)
	require.NotNil(t, listed.IDs, "an empty catalog must encode as an array, not null")
	require.False(t, listed.Truncated)

	_, err = source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, readCount(t, countPath), "an empty page is a successful catalog and must be reused")
}

func TestListSessionsReportsAMissingBinary(t *testing.T) {
	t.Parallel()
	source := &Source{Binary: filepath.Join(t.TempDir(), "absent-peasant"), Timeout: 5 * time.Second}

	listed, err := source.ListSessions(context.Background())
	require.Equal(t, KindUnavailable, KindOf(err))
	require.Contains(t, err.Error(), "install Peasant")
	require.Empty(t, listed.IDs)
	require.False(t, listed.Truncated)
}

func TestListSessionsRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
		message string
	}{
		{name: "not json", payload: "{not json", message: "not one JSON array"},
		{name: "null document", payload: "null", message: "printed null"},
		{name: "object instead of array", payload: `{"sessions":[]}`, message: "not one JSON array"},
		{name: "trailing content", payload: `[]{"extra":true}`, message: "content after the JSON array"},
		{name: "entry without an id", payload: `[{"turns":1}]`, message: "entry 0 has no id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			binary, countPath := listPeasant(t, tc.payload)
			source := &Source{Binary: binary, Timeout: 5 * time.Second}

			_, err := source.ListSessions(context.Background())
			require.Equal(t, KindMalformed, KindOf(err), "error: %v", err)
			require.Contains(t, err.Error(), tc.message)

			_, err = source.ListSessions(context.Background())
			require.Error(t, err)
			require.Equal(t, 2, readCount(t, countPath), "a malformed catalog must not be cached")
		})
	}
}

func TestListSessionsMarksAFullPageTruncated(t *testing.T) {
	t.Parallel()
	binary, _ := listPeasant(t, `[{"id":"ses_a"},{"id":"ses_b"}]`)
	source := &Source{Binary: binary, Timeout: 5 * time.Second, listLimit: 2}

	listed, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a", "ses_b"}, listed.IDs)
	require.True(t, listed.Truncated, "a page that filled the requested limit is not evidence that later sessions have no transcript")
	require.Equal(t, "2", recordedArgs(t, binary)[4])
}

func TestListSessionsRefetchesAfterTheCacheWindow(t *testing.T) {
	t.Parallel()
	binary, countPath := listPeasant(t, `[{"id":"ses_a"}]`)
	source := &Source{Binary: binary, Timeout: 5 * time.Second}

	_, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, readCount(t, countPath))

	source.listCache.mu.Lock()
	source.listCache.at = time.Now().Add(-sessionListCacheTTL - time.Second)
	source.listCache.mu.Unlock()

	listed, err := source.ListSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ses_a"}, listed.IDs)
	require.Equal(t, 2, readCount(t, countPath), "a catalog older than the reuse window must spawn Peasant again")
}

func TestNewSourceUsesTheMeasuredSessionListBound(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, NewSource().listLimit)
	require.Equal(t, 200, sessionListLimit)
	require.Equal(t, 30*time.Second, sessionListCacheTTL)
}

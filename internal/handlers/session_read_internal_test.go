package handlers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/sessionapi"
)

// The CLI refreshes the cache before it reads, so a dirty marker is normally
// cleared before the service can observe ErrStaleCache. The mapper is the last
// line of defence for the remaining window (a concurrent writer marks the cache
// dirty between the refresh and the read), so its category mapping is pinned
// here rather than through a timing-dependent integration test.
func TestSessionReadErrorMapsEveryTypedBranch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantCode int
		wantText string
	}{
		{
			name:     "stale cache",
			err:      sessionapi.ErrStaleCache,
			wantCode: 5,
			wantText: "marked stale",
		},
		{
			name:     "invalid cursor",
			err:      fmt.Errorf("%w: cursor is not base64", sessionapi.ErrInvalidPageToken),
			wantCode: 1,
			wantText: "not a valid page cursor",
		},
		{
			name:     "invalid filter",
			err:      fmt.Errorf("%w: unknown state", sessionapi.ErrInvalidFilter),
			wantCode: 1,
			wantText: "session filter value is not supported",
		},
		{
			name:     "unknown storage fault",
			err:      errors.New("database is gone"),
			wantCode: 5,
			wantText: "Couldn't read sessions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapped := sessionReadError(tc.err, "/tmp/pasture.db")
			require.Equal(t, tc.wantCode, pasterrors.ExitCode(mapped))
			require.Contains(t, mapped.Error(), tc.wantText)
		})
	}
}

func TestSessionReadErrorPreservesAnAlreadyStructuredCause(t *testing.T) {
	t.Parallel()

	original := &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     "already classified",
	}
	require.Same(t, original, sessionReadError(original, "/tmp/pasture.db"))
}

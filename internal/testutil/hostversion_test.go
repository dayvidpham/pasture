package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
)

func TestBelowFloorStepsDownOneRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"3.3.3", "3.3.2"},
		{"7.7.7", "7.7.6"},
		{"1.0.0", "0.0.0"},
	} {
		v, err := runtime.ParseHostVersion(tc.in)
		require.NoError(t, err)
		require.Equal(t, tc.want, testutil.BelowFloor(t, v).String(), "below %s", tc.in)
	}
}

func TestBumpRaisesTheNamedComponent(t *testing.T) {
	t.Parallel()
	v, err := runtime.ParseHostVersion("1.18.29")
	require.NoError(t, err)
	require.Equal(t, "1.18.30", testutil.Bump(t, v, 0, 0, 1).String())
	require.Equal(t, "1.19.29", testutil.Bump(t, v, 0, 1, 0).String())
	require.Equal(t, "2.18.29", testutil.Bump(t, v, 1, 0, 0).String())
}

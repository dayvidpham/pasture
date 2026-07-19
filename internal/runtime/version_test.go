package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, value string) runtime.HostVersion {
	t.Helper()
	version, err := runtime.ParseHostVersion(value)
	require.NoError(t, err, "parse %q", value)
	return version
}

func TestParseHostVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "exact triple", input: "2.1.210"},
		{name: "leading v", input: "v2.1.210"},
		{name: "prerelease", input: "2.1.210-rc.1"},
		{name: "build metadata", input: "2.1.210+build.7"},
		{name: "prerelease and build", input: "2.1.210-rc.1+build.7"},
		{name: "empty", input: "", wantErr: true},
		{name: "padded", input: " 2.1.210", wantErr: true},
		{name: "two components", input: "2.1", wantErr: true},
		{name: "four components", input: "2.1.210.5", wantErr: true},
		{name: "non numeric", input: "2.x.0", wantErr: true},
		{name: "leading zero", input: "2.01.0", wantErr: true},
		{name: "leading zero numeric prerelease identifier", input: "1.2.3-01", wantErr: true},
		{name: "leading zero alphanumeric prerelease identifier allowed", input: "1.2.3-0a.1"},
		{name: "leading zero build identifier allowed", input: "1.2.3+001"},
		{name: "empty prerelease", input: "2.1.210-", wantErr: true},
		{name: "empty build", input: "2.1.210+", wantErr: true},
		{name: "garbage", input: "not-a-version", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			version, err := runtime.ParseHostVersion(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.False(t, version.IsValid())
				return
			}
			require.NoError(t, err)
			assert.True(t, version.IsValid())
		})
	}
}

func TestComparePrecedence(t *testing.T) {
	t.Parallel()
	// Build metadata never changes precedence.
	assert.Equal(t, 0, runtime.ComparePrecedence(mustParse(t, "2.1.210+a"), mustParse(t, "2.1.210+b")))
	// Release ordering.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.209"), mustParse(t, "2.1.210")))
	assert.Equal(t, 1, runtime.ComparePrecedence(mustParse(t, "2.2.0"), mustParse(t, "2.1.999")))
	// A prerelease has lower precedence than its release.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.210-rc.1"), mustParse(t, "2.1.210")))
	// Prerelease identifier ordering.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.210-rc.1"), mustParse(t, "2.1.210-rc.2")))
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.210-1"), mustParse(t, "2.1.210-alpha")))
}

func TestVersionConstraintExactBoundary(t *testing.T) {
	t.Parallel()
	constraint, err := runtime.NewExactVersion(mustParse(t, "2.1.210"))
	require.NoError(t, err)

	assert.True(t, constraint.Allows(mustParse(t, "2.1.210")), "exact accepted boundary")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.210+build.9")), "build metadata does not change acceptance")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.209")), "immediately lower rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.211")), "immediately higher rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.210-rc.1")), "prerelease requires explicit inclusion")
	assert.False(t, constraint.Allows(runtime.HostVersion{}), "zero version rejected")
}

func TestVersionConstraintPrereleaseInclusion(t *testing.T) {
	t.Parallel()
	lo := mustParse(t, "2.1.210-rc.1")
	hi := mustParse(t, "2.1.210")
	constraint, err := runtime.NewVersionConstraint(lo, hi, true)
	require.NoError(t, err)

	assert.True(t, constraint.Allows(mustParse(t, "2.1.210-rc.1")), "explicitly included prerelease boundary")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.210")))
	assert.False(t, constraint.Allows(mustParse(t, "2.1.210-beta.1")), "prerelease below the included boundary rejected")
}

func TestNewVersionConstraintRejectsInvertedBounds(t *testing.T) {
	t.Parallel()
	_, err := runtime.NewVersionConstraint(mustParse(t, "2.1.211"), mustParse(t, "2.1.210"), false)
	require.Error(t, err)
}

func TestCapabilityVersionRange(t *testing.T) {
	t.Parallel()
	rng, err := runtime.NewCapabilityVersionRange(ir.CapabilityContractVersion("1.0.0"), ir.CapabilityContractVersion("1.4.0"))
	require.NoError(t, err)

	assert.True(t, rng.Includes(ir.CapabilityContractVersion("1.0.0")))
	assert.True(t, rng.Includes(ir.CapabilityContractVersion("1.3.9")))
	assert.True(t, rng.Includes(ir.CapabilityContractVersion("1.4.0")))
	assert.False(t, rng.Includes(ir.CapabilityContractVersion("0.9.9")))
	assert.False(t, rng.Includes(ir.CapabilityContractVersion("1.4.1")))
	assert.False(t, rng.Includes(ir.CapabilityContractVersion("2.0.0")))

	_, err = runtime.NewCapabilityVersionRange(ir.CapabilityContractVersion("2.0.0"), ir.CapabilityContractVersion("1.0.0"))
	require.Error(t, err, "inverted range rejected")

	_, err = runtime.NewCapabilityVersionRange(ir.CapabilityContractVersion("bad"), ir.CapabilityContractVersion("1.0.0"))
	require.Error(t, err, "malformed bound rejected")
}

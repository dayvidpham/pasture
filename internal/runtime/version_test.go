package runtime_test

import (
	"os"
	"path/filepath"
	"regexp"
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
		{name: "exact triple", input: "2.1.261"},
		{name: "leading v", input: "v2.1.261"},
		{name: "prerelease", input: "2.1.261-rc.1"},
		{name: "build metadata", input: "2.1.261+build.7"},
		{name: "prerelease and build", input: "2.1.261-rc.1+build.7"},
		{name: "empty", input: "", wantErr: true},
		{name: "padded", input: " 2.1.261", wantErr: true},
		{name: "two components", input: "2.1", wantErr: true},
		{name: "four components", input: "2.1.261.5", wantErr: true},
		{name: "non numeric", input: "2.x.0", wantErr: true},
		{name: "leading zero", input: "2.01.0", wantErr: true},
		{name: "leading zero numeric prerelease identifier", input: "1.2.3-01", wantErr: true},
		{name: "leading zero alphanumeric prerelease identifier allowed", input: "1.2.3-0a.1"},
		{name: "leading zero build identifier allowed", input: "1.2.3+001"},
		{name: "empty prerelease", input: "2.1.261-", wantErr: true},
		{name: "empty build", input: "2.1.261+", wantErr: true},
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
	assert.Equal(t, 0, runtime.ComparePrecedence(mustParse(t, "2.1.261+a"), mustParse(t, "2.1.261+b")))
	// Release ordering.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.260"), mustParse(t, "2.1.261")))
	assert.Equal(t, 1, runtime.ComparePrecedence(mustParse(t, "2.2.0"), mustParse(t, "2.1.999")))
	// A prerelease has lower precedence than its release.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.261-rc.1"), mustParse(t, "2.1.261")))
	// Prerelease identifier ordering.
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.261-rc.1"), mustParse(t, "2.1.261-rc.2")))
	assert.Equal(t, -1, runtime.ComparePrecedence(mustParse(t, "2.1.261-1"), mustParse(t, "2.1.261-alpha")))
}

func TestVersionConstraintExactBoundary(t *testing.T) {
	t.Parallel()
	constraint, err := runtime.NewExactVersion(mustParse(t, "2.1.261"))
	require.NoError(t, err)

	assert.True(t, constraint.Allows(mustParse(t, "2.1.261")), "exact accepted boundary")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.261+build.9")), "build metadata does not change acceptance")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.260")), "immediately lower rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.211")), "immediately higher rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.261-rc.1")), "prerelease requires explicit inclusion")
	assert.False(t, constraint.Allows(runtime.HostVersion{}), "zero version rejected")
}

func TestVersionConstraintPrereleaseInclusion(t *testing.T) {
	t.Parallel()
	lo := mustParse(t, "2.1.261-rc.1")
	hi := mustParse(t, "2.1.261")
	constraint, err := runtime.NewVersionConstraint(lo, hi, true)
	require.NoError(t, err)

	assert.True(t, constraint.Allows(mustParse(t, "2.1.261-rc.1")), "explicitly included prerelease boundary")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.261")))
	assert.False(t, constraint.Allows(mustParse(t, "2.1.261-beta.1")), "prerelease below the included boundary rejected")
}

func TestNewVersionConstraintRejectsInvertedBounds(t *testing.T) {
	t.Parallel()
	// Sample versions unrelated to any recorded host version: the bounds are
	// inverted by construction (min above max), and no version sweep moves them.
	_, err := runtime.NewVersionConstraint(mustParse(t, "9.9.9"), mustParse(t, "9.9.8"), false)
	require.Error(t, err)
}

// A floor is the admission shape for a recorded baseline version: the host
// may move forward without a re-pin, and a host below the baseline is refused.
func TestVersionFloorAdmitsMinAndEveryHigherRelease(t *testing.T) {
	t.Parallel()
	constraint, err := runtime.NewVersionFloor(mustParse(t, "2.1.261"))
	require.NoError(t, err)

	assert.True(t, constraint.IsValid())
	assert.False(t, constraint.HasUpperBound(), "a floor has no upper bound")
	assert.False(t, constraint.Max().IsValid(), "Max of a floor is the zero version; HasUpperBound is the question to ask")
	assert.Equal(t, "2.1.261", constraint.Min().String())

	assert.True(t, constraint.Allows(mustParse(t, "2.1.261")), "the floor itself is admitted")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.262")), "immediately higher patch release admitted")
	assert.True(t, constraint.Allows(mustParse(t, "2.2.0")), "higher minor release admitted")
	assert.True(t, constraint.Allows(mustParse(t, "3.0.0")), "higher major release admitted")
	assert.True(t, constraint.Allows(mustParse(t, "2.1.261+build.9")), "build metadata does not change acceptance")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.260")), "immediately lower release rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.0.999")), "lower minor release rejected")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.262-rc.1")), "a prerelease above the floor is not admitted: a floor never includes prereleases")
	assert.False(t, constraint.Allows(mustParse(t, "2.1.261-rc.1")), "a prerelease of the floor release is below it and rejected")
	assert.False(t, constraint.Allows(runtime.HostVersion{}), "zero version rejected")
}

func TestNewVersionFloorRejectsUnparsedBound(t *testing.T) {
	t.Parallel()
	_, err := runtime.NewVersionFloor(runtime.HostVersion{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version floor has a zero or unparsed lower bound")
	assert.False(t, runtime.VersionConstraint{}.HasUpperBound(), "the zero constraint has no bound of any kind")
	assert.Equal(t, "", runtime.VersionConstraint{}.Describe(), "the zero constraint describes nothing")
}

// Describe is the text a diagnostic quotes for the admitted versions. Each
// phrase is load-bearing: a reader decides from it whether the host must move
// up, stay, or move down.
func TestVersionConstraintDescribeNamesEachShape(t *testing.T) {
	t.Parallel()
	exact, err := runtime.NewExactVersion(mustParse(t, "0.153.0"))
	require.NoError(t, err)
	closed, err := runtime.NewVersionConstraint(mustParse(t, "2.1.261"), mustParse(t, "2.2.0-0"), false)
	require.NoError(t, err)
	floor, err := runtime.NewVersionFloor(mustParse(t, "1.18.29"))
	require.NoError(t, err)

	assert.Equal(t, "exactly 0.153.0", exact.Describe())
	assert.Equal(t, "from 2.1.261 through 2.2.0-0", closed.Describe())
	assert.Equal(t, "at or above 1.18.29", floor.Describe())
	assert.True(t, exact.HasUpperBound())
	assert.True(t, closed.HasUpperBound())
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

// changelogFloorPins are the load-bearing phrases of the release note that
// describes the admission floor. The first two say what the floor DOES; the
// last three say WHERE it is judged and where it is not.
//
// The where matters more than the what. The floor is judged when a host is
// installed and when a captured fixture is admitted; it is NOT judged on the
// live hook path, where a hook proceeds and the observed host version is kept
// as provenance only, because some routes reach the hook with no usable
// version at all. Without those phrases an operator on a host below the floor
// reads the note, expects the hooks to refuse, and meets no refusal until an
// installation is attempted.
//
// This pin lives with the floor, in the package that defines it
// (NewVersionFloor here, mustFloorContract in profiles.go), rather than beside
// the CHANGELOG or with the harness fixtures: the sentence is a claim about
// this mechanism, so the test that reads it belongs where a person changing
// the mechanism will meet it.
var changelogFloorPins = []string{
	"Admission is a floor at each recorded version",
	"a host below it is refused with the version it needs",
	"The floor decides installation and fixture admission",
	"the observed host version is recorded as provenance and never judged",
	"some routes pass no usable version at all",
}

// changelogWhitespace collapses the wrapped release note into one line so a
// pinned phrase can be found across a line break.
var changelogWhitespace = regexp.MustCompile(`\s+`)

// TestTheReleaseNoteSaysWhereTheAdmissionFloorIsJudged reads the shipped
// CHANGELOG.md and requires the floor sentence to carry both halves of the
// truth: what the floor does, and where it is applied. An operator reads that
// note before they read any code, so a note that promises a refusal the live
// hook path never makes sends them to the wrong place.
func TestTheReleaseNoteSaysWhereTheAdmissionFloorIsJudged(t *testing.T) {
	t.Parallel()

	path := filepath.Join(moduleRoot(t), "CHANGELOG.md")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the release note must be readable to be held to its claims")
	text := changelogWhitespace.ReplaceAllString(string(raw), " ")
	for _, pin := range changelogFloorPins {
		assert.Contains(t, text, pin, "CHANGELOG.md lost the phrase %q, so the release note no longer states where the admission floor is judged", pin)
	}
}

// moduleRoot resolves the repository root from this package's directory, which
// is two levels below it.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(dir, "..", ".."))
}

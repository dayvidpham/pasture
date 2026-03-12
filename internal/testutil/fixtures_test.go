// Package testutil contains self-tests for the fixture loader.
//
// Tests are split into two groups:
//   - Black-box tests (package testutil_test): verify the public API that
//     downstream packages use.
//   - White-box tests (package testutil): verify error-path behavior of the
//     internal readFixture helper without going through t.FailNow.
//
// Both groups live in this single file for clarity.

// White-box tests for error paths (must be package testutil, not testutil_test,
// to access the unexported readFixture helper).
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleFixture mirrors the structure of testdata/content_block.yaml.
type sampleFixture struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
	Count int    `yaml:"count"`
}

// TestLoadFixtures_ValidFile verifies that a well-formed YAML fixture is
// loaded and unmarshalled correctly into the target struct.
// This exercises the full public LoadFixtures path.
func TestLoadFixtures_ValidFile(t *testing.T) {
	var got sampleFixture
	LoadFixtures(t, ContentBlock, &got)

	assert.Equal(t, "test-content-block", got.Name)
	assert.Equal(t, "hello-fixture", got.Value)
	assert.Equal(t, 42, got.Count)
}

// TestReadFixture_MissingFile verifies that readFixture returns a descriptive
// error when the fixture file does not exist.
//
// By testing readFixture (not LoadFixtures), we avoid triggering t.FailNow
// while still covering the same code path that LoadFixtures calls.
func TestReadFixture_MissingFile(t *testing.T) {
	var target sampleFixture
	err := readFixture(FixtureName("nonexistent_fixture_xyzzy"), &target)

	require.Error(t, err,
		"readFixture must return an error for a missing fixture file")
	assert.Contains(t, err.Error(), "nonexistent_fixture_xyzzy",
		"error message should name the missing fixture; got: %s", err)
	assert.Contains(t, err.Error(), "testdata",
		"error message should mention the testdata directory; got: %s", err)
}

// TestReadFixture_MalformedYAML verifies that readFixture returns a descriptive
// error when the YAML file cannot be parsed.
func TestReadFixture_MalformedYAML(t *testing.T) {
	malformedPath := filepath.Join("testdata", "malformed_fixture.yaml")
	require.NoError(t,
		os.WriteFile(malformedPath, []byte(":\tbad: yaml: [\n"), 0o644),
		"setup: could not write malformed fixture",
	)
	t.Cleanup(func() { _ = os.Remove(malformedPath) })

	var target sampleFixture
	err := readFixture(FixtureName("malformed_fixture"), &target)

	require.Error(t, err,
		"readFixture must return an error for malformed YAML")
	assert.Contains(t, err.Error(), "malformed_fixture",
		"error message should name the malformed fixture; got: %s", err)
}

// TestFixtureNameConstants verifies that all four FixtureName constants are
// distinct non-empty strings — a static guard against copy-paste errors.
func TestFixtureNameConstants(t *testing.T) {
	names := []FixtureName{
		ContentBlock,
		CLISmoke,
		RunAgentSession,
		ConfigLoading,
	}

	seen := make(map[FixtureName]struct{}, len(names))
	for _, n := range names {
		assert.NotEmpty(t, string(n), "FixtureName constant must not be empty")
		_, duplicate := seen[n]
		assert.False(t, duplicate, "duplicate FixtureName constant: %q", n)
		seen[n] = struct{}{}
	}
}

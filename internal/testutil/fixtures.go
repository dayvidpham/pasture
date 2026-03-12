// Package testutil provides shared testing utilities for the pasture test suite.
//
// LoadFixtures reads a named YAML fixture from the caller's testdata/ directory
// and unmarshals it into the supplied target value. Tests that rely on this
// function will fail immediately (via require) if the fixture file is missing
// or malformed, keeping test failures actionable.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// FixtureName is a typed string that identifies a YAML fixture file stored
// under the calling package's testdata/ directory. Using a named type instead
// of a plain string prevents accidental string literals at call sites.
type FixtureName string

const (
	// ContentBlock is used by S2–S3 tests (message/content-block scenarios).
	ContentBlock FixtureName = "content_block"

	// CLISmoke is used by S4–S5 tests (CLI smoke / handler scenarios).
	CLISmoke FixtureName = "cli_smoke"

	// RunAgentSession is used by S5–S6 tests (Temporal workflow scenarios).
	RunAgentSession FixtureName = "run_agent_session"

	// ConfigLoading is used by S3 tests (config loading scenarios).
	ConfigLoading FixtureName = "config_loading"

	// CodegenMarkers is used by S3 codegen tests (marker parsing scenarios).
	CodegenMarkers FixtureName = "markers"

	// CodegenContext is used by S2 codegen tests (context injection scenarios).
	CodegenContext FixtureName = "context"

	// CodegenAgents is used by S6 codegen tests (agent definition generation scenarios).
	CodegenAgents FixtureName = "agents"

	// CodegenSkills is used by S4 codegen tests (SKILL.md generation scenarios).
	CodegenSkills FixtureName = "skills"

	// CodegenSchema is used by S5 codegen tests (schema.xml generation scenarios).
	CodegenSchema FixtureName = "schema"
)

// LoadFixtures reads testdata/<name>.yaml relative to the current working
// directory (the package under test) and unmarshals the contents into target.
//
// It calls t.Helper() so that failure lines point to the caller, and uses
// require (not assert) so that the test stops immediately on infrastructure
// failures rather than proceeding with a zero-value target.
//
// Parameters:
//   - t: the active *testing.T (must not be nil).
//   - name: one of the FixtureName constants — determines the file path.
//   - target: a non-nil pointer that yaml.Unmarshal will populate.
//
// Failure modes (both call t.FailNow via require):
//   - The fixture file does not exist at testdata/<name>.yaml.
//   - The YAML content cannot be unmarshalled into target.
func LoadFixtures(t *testing.T, name FixtureName, target any) {
	t.Helper()
	require.NoError(t, readFixture(name, target))
}

// readFixture is the testable core of LoadFixtures. It returns the parsed
// YAML contents without calling t.FailNow, making error-path testing possible
// from within the same package. LoadFixtures wraps this with require.
func readFixture(name FixtureName, target any) error {
	path := filepath.Join("testdata", string(name)+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf(
			"LoadFixtures: could not read fixture file %q — "+
				"ensure testdata/%s.yaml exists in the package under test "+
				"(working dir: %s): %w",
			path, string(name), mustGetwd(), err,
		)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf(
			"LoadFixtures: could not unmarshal fixture file %q into %T — "+
				"check that the YAML structure matches the target type: %w",
			path, target, err,
		)
	}
	return nil
}

// mustGetwd returns the current working directory for error messages,
// substituting a fallback string if os.Getwd fails.
func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "<unknown>"
	}
	return wd
}

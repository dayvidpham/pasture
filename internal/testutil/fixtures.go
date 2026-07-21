// Package testutil provides shared testing utilities for the pasture test suite.
//
// LoadFixtures reads a named YAML fixture from the caller's testdata/ directory
// and unmarshals it into the supplied target value. Tests that rely on this
// function will fail immediately (via require) if the fixture file is missing
// or malformed, keeping test failures actionable.
package testutil

import (
	"bytes"
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
	// ContentBlock covers message/content-block scenarios.
	ContentBlock FixtureName = "content_block"

	// CLISmoke covers CLI smoke and handler scenarios.
	CLISmoke FixtureName = "cli_smoke"

	// ValidateBeforeOpen is used by the pasture CLI tests asserting that invalid
	// epoch/signal/session/slice/phase invocations are rejected by argument
	// validation before the durable database is opened.
	ValidateBeforeOpen FixtureName = "validate_before_open"

	// RunAgentSession covers workflow execution scenarios.
	RunAgentSession FixtureName = "run_agent_session"

	// ConfigLoading covers configuration loading scenarios.
	ConfigLoading FixtureName = "config_loading"

	// CodegenMarkers covers marker parsing scenarios.
	CodegenMarkers FixtureName = "markers"

	// CodegenContext covers context injection scenarios.
	CodegenContext FixtureName = "context"

	// CodegenAgents covers agent definition generation scenarios.
	CodegenAgents FixtureName = "agents"

	// CodegenSkills covers skill generation scenarios.
	CodegenSkills FixtureName = "skills"

	// CodegenSchema covers schema generation scenarios.
	CodegenSchema FixtureName = "schema"

	// MarketplaceValidation covers canonical marketplace boundary scenarios.
	MarketplaceValidation FixtureName = "marketplace_validation"
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
	// Strict decoding: reject any YAML key that has no matching struct field so a
	// typo'd fixture key (e.g. want_stderr_exclude instead of want_stderr_excludes)
	// fails loudly here instead of silently zero-valuing. For skip-if-empty
	// assertion fields (the *_contains / *_excludes negative-control fields), a
	// silent zero-value would quietly DISABLE the control rather than fail.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf(
			"LoadFixtures: could not decode fixture file %q into %T — "+
				"check that the YAML structure matches the target type and contains "+
				"no unknown keys: %w",
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

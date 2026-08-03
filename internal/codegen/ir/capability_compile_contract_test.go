package ir_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCapabilityCompilePassFixture proves the #41 capability escape hatch's
// external usage pattern — a typed const CapabilityID, an opaque descriptor
// stored in a var through MustDefineCapability, and an InvokeTool call with
// matching typed input — compiles as one ordinary Go package, alongside the
// #38 typed-descriptor fixture already in this same testdata/compilepass
// directory.
func TestCapabilityCompilePassFixture(t *testing.T) {
	t.Parallel()

	pass := exec.Command("go", "build", "./testdata/compilepass")
	pass.Env = append(os.Environ(), "CGO_ENABLED=0")
	passOutput, err := pass.CombinedOutput()
	require.NoError(t, err, "typed capability fixture must compile:\n%s", passOutput)
}

// TestCapabilityCompileFailDomainBoundaries isolates every capability-domain
// compile-fail boundary into its own fixture directory/package, mirroring
// #38's TestCompileFailDomainBoundaries so each violation fails compilation
// independently and asserts its own exact diagnostic.
func TestCapabilityCompileFailDomainBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir     string
		what    string
		mustAll []string
	}{
		{
			dir:     "raw_string_capability_descriptor",
			what:    "a raw string literal must not satisfy the opaque Capability lookup boundary",
			mustAll: []string{"cannot use", "Capability"},
		},
		{
			dir:     "wrong_capability_descriptor_domain",
			what:    "an OperationDescriptor must not satisfy InvokeTool's Capability lookup",
			mustAll: []string{"does not match inferred type", "OperationDescriptor", "Capability"},
		},
		{
			dir:     "mismatched_invoke_tool_input",
			what:    "InvokeTool must reject an input type that does not match the capability's own In type parameter",
			mustAll: []string{"does not match inferred type", "wrongInput"},
		},
		{
			dir:     "wrong_capability_id_domain",
			what:    "a SkillID must not satisfy a CapabilityID parameter",
			mustAll: []string{"cannot use", "SkillID", "CapabilityID"},
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.dir, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("go", "build", "./testdata/compilefail/"+testCase.dir)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			output, err := cmd.CombinedOutput()
			require.Error(t, err, "%s:\n%s", testCase.what, output)
			message := string(output)
			for _, substring := range testCase.mustAll {
				assert.Contains(t, message, substring, "%s — expected diagnostic to name %q:\n%s", testCase.what, substring, message)
			}
		})
	}
}

package ir_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpaqueDescriptorCompilePassFixture(t *testing.T) {
	t.Parallel()

	pass := exec.Command("go", "test", "./testdata/compilepass")
	pass.Env = append(os.Environ(), "CGO_ENABLED=0")
	passOutput, err := pass.CombinedOutput()
	require.NoError(t, err, "typed descriptor fixture must compile:\n%s", passOutput)
}

// TestCompileFailDomainBoundaries isolates every compile-fail domain
// boundary into its own fixture directory/package (testdata/compilefail/*),
// each asserting its own exact diagnostic. A prior revision combined three
// unrelated violations into one file/package; combining them meant a single
// weak "cannot use ... (SemanticOperationID OR OperationDescriptor)"
// assertion could not prove any one specific boundary actually failed for
// its own stated reason, and a compiler that stopped after the first error
// could silently leave later violations unexercised.
func TestCompileFailDomainBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir     string
		what    string
		mustAll []string
	}{
		{
			dir:     "raw_string_operation_id",
			what:    "a raw string literal must not satisfy the opaque SemanticOperationID boundary",
			mustAll: []string{"cannot use", "SemanticOperationID"},
		},
		{
			dir:     "raw_string_descriptor_lookup",
			what:    "a raw string literal must not satisfy the opaque OperationDescriptor lookup boundary",
			mustAll: []string{"cannot use", "OperationDescriptor"},
		},
		{
			dir:     "wrong_descriptor_domain",
			what:    "an EffectDescriptor must not satisfy an OperationDescriptor lookup",
			mustAll: []string{"cannot use", "EffectDescriptor", "OperationDescriptor"},
		},
		{
			dir:     "wrong_id_domain",
			what:    "a SkillID must not satisfy a SemanticOperationID parameter",
			mustAll: []string{"cannot use", "SkillID", "SemanticOperationID"},
		},
		{
			dir:     "unforgeable_rendered_tree",
			what:    "RenderedFile construction must not be reachable from outside the package",
			mustAll: []string{"undefined", "NewRenderedFile"},
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

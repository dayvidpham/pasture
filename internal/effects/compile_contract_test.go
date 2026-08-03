package effects_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConsumerCompilePassFixture proves the intended one-way consumer usage —
// import effects, obtain the opaque proof from GuardedPushExactCommit, and hand
// it to a protected commit — compiles as one ordinary Go package.
func TestConsumerCompilePassFixture(t *testing.T) {
	t.Parallel()

	pass := exec.Command("go", "build", "./testdata/compilepass")
	pass.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := pass.CombinedOutput()
	require.NoError(t, err, "consumer fixture must compile:\n%s", output)
}

// TestProofUnforgeabilityCompileFailFixtures proves, each in its own isolated
// package, that the guarded-push proof and typed operands cannot be forged or
// transposed from outside the package. Each asserts its own exact diagnostic,
// mirroring the #38/#41 compile-fail pattern.
func TestProofUnforgeabilityCompileFailFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir     string
		what    string
		mustAll []string
	}{
		{
			dir:     "forge_verified_guarded_push",
			what:    "a VerifiedGuardedPush cannot be forged by setting its unexported verified field",
			mustAll: []string{"verified"},
		},
		{
			dir:     "call_private_proof_constructor",
			what:    "the package-private proof constructor is unreachable from outside the package",
			mustAll: []string{"newVerifiedGuardedPush"},
		},
		{
			dir:     "raw_string_commit_oid",
			what:    "a raw string must not satisfy the opaque CommitOID operand",
			mustAll: []string{"cannot use", "CommitOID"},
		},
		{
			dir:     "wrong_id_domain_repository",
			what:    "a CommitOID must not satisfy a RepositoryID operand",
			mustAll: []string{"cannot use", "RepositoryID"},
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

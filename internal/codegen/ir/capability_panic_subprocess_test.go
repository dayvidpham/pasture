package ir_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCapabilityStaticDeclarationPanicSubprocess is the acceptance
// criterion's subprocess fixture: it runs testdata/panicfixture as a real,
// separate process and proves an invalid static
// `var X = MustDefineCapability(...)` declaration panics during package
// initialization — before main ever runs — with DefineCapability's own
// actionable validation error on stderr, not a generic panic message. This
// cannot be proven in-process: a fatal panic during package-level var
// initialization is not something a test in the same binary can safely
// trigger and recover from.
func TestCapabilityStaticDeclarationPanicSubprocess(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", "./testdata/panicfixture")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "an invalid static MustDefineCapability declaration must crash the process:\n%s", output)

	message := string(output)
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, message, field, "panic output must retain the actionable DefineCapability diagnostic:\n%s", message)
	}
	assert.Contains(t, message, "not-namespaced", "panic output must name the exact invalid identity that failed validation")
	assert.Contains(t, message, "not namespaced", "panic output must retain DefineCapability's own actionable reason, not a generic panic wrapper")
}

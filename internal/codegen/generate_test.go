package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateRunsStrictGateBeforeWritingOutput is pasture#42's end-to-end
// "no partial output" proof for the production pipeline: it copies the real
// canonical skills/ and agents/ trees into a temp module root, injects one
// unclassified harness-syntax candidate, then runs codegen.Generate against
// that root. Generation must fail on the strict gate and write no schema.xml
// — the gate runs before any output is produced.
func TestGenerateRunsStrictGateBeforeWritingOutput(t *testing.T) {
	t.Parallel()

	realRoot, err := scan.ModuleRoot()
	require.NoError(t, err)

	tmpRoot := t.TempDir()
	for _, dir := range []string{"skills", "agents"} {
		require.NoError(t, os.CopyFS(filepath.Join(tmpRoot, dir), os.DirFS(filepath.Join(realRoot, dir))),
			"copying canonical root %q into the temp module root", dir)
	}

	// Inject one unclassified candidate: append a new section containing a
	// TeamCreate( call to a real, active skill owner. It matches no
	// classification-manifest entry, so the strict gate must reject it.
	injected := filepath.Join(tmpRoot, "skills", "worker", "SKILL.md")
	original, err := os.ReadFile(injected)
	require.NoError(t, err)
	appended := string(original) + "\n\n## Injected Unclassified Candidate\n\nSpawn via TeamCreate({ team_name: \"never-classified\" }) here.\n"
	require.NoError(t, os.WriteFile(injected, []byte(appended), 0o644))

	targets, err := codegen.ResolveHarness([]string{string(codegen.HarnessClaudeCode)})
	require.NoError(t, err)

	result, errs := codegen.Generate(tmpRoot, targets, codegen.DefaultOptions)

	require.NotEmpty(t, errs, "an unclassified candidate must abort generation")
	assert.Contains(t, errs[0].Error(), "no partial output")
	assert.Empty(t, result.SchemaPath, "the gate runs before schema generation")
	assert.Empty(t, result.Files, "the gate runs before any harness output")

	_, statErr := os.Stat(filepath.Join(tmpRoot, "schema.xml"))
	assert.True(t, os.IsNotExist(statErr), "generation must not write schema.xml when the strict gate fails")
}

// TestGenerateGateFailureIsTheSoleError proves that when the strict gate
// fails, Generate returns exactly the gate error and does not additionally
// run (and report) the schema, harness, or global-id steps.
func TestGenerateGateFailureIsTheSoleError(t *testing.T) {
	t.Parallel()

	// A module root with neither skills/ nor agents/ fails the gate at the
	// scan's root-discovery stage.
	tmpRoot := t.TempDir()

	targets, err := codegen.ResolveHarness([]string{string(codegen.HarnessClaudeCode)})
	require.NoError(t, err)

	result, errs := codegen.Generate(tmpRoot, targets, codegen.DefaultOptions)

	require.Len(t, errs, 1, "the gate failure must be the sole error; no downstream step ran")
	assert.Contains(t, errs[0].Error(), "pasture#42 strict source-migration gate")
	assert.Empty(t, result.SchemaPath)
	assert.Empty(t, result.Files)
}

// TestStrictLifecycleRowGateAcceptsTheShippedProfiles proves the second strict
// gate is wired and green on the tree that ships. A blocking exit code is a
// real refusal of a user's prompt or tool call, so no shipped row may claim one
// without citing where the host's blocking behavior was read.
func TestStrictLifecycleRowGateAcceptsTheShippedProfiles(t *testing.T) {
	t.Parallel()

	require.NoError(t, codegen.RequireEvidencedLifecycleRows(),
		"every shipped lifecycle row that claims a blocking exit code must cite its evidence")
}

// TestStrictLifecycleRowGateRunsBeforeAnyWrite pins the ORDER of the pipeline.
// The gate is only worth having if it aborts before generation writes anything:
// a partially written tree with an unevidenced blocking row is worse than no
// output at all. The shipped profiles pass the gate, so the ordering cannot be
// proven by triggering it; this reads the production pipeline instead and fails
// if the gate call ever moves below the first write.
func TestStrictLifecycleRowGateRunsBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	source, err := os.ReadFile(filepath.Join(root, "internal/codegen/generate.go"))
	require.NoError(t, err)

	body := string(source)
	gate := strings.Index(body, "RequireEvidencedLifecycleRows(); err != nil")
	require.NotEqual(t, -1, gate, "Generate no longer calls the strict lifecycle-row gate")

	for _, write := range []string{"GenerateSchemaToFile(", "EmitHarness(", "emitInventoryRows("} {
		at := strings.Index(body, write)
		require.NotEqual(t, -1, at, "Generate no longer calls %s", write)
		assert.Less(t, gate, at,
			"the strict lifecycle-row gate must run before %s, or a refused row can leave partial output", write)
	}
}

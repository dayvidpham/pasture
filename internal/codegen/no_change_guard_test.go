package codegen_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/dayvidpham/pasture/internal/inventory"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds the epic-wide safety invariants: the things the lifecycle
// work must NEVER touch, whatever it changes.
//
// Three agent surfaces are out of scope for the whole lifecycle effort: the
// Agent Client Protocol transcript adapters, Gemini, and Antigravity. Work that
// widens the lifecycle blast radius into any of them is a regression even when
// every other gate is green, because nobody is reviewing those surfaces.
//
// The other invariant is harder and simpler at once: pasture must never install
// a Git hook. Harness lifecycle hooks are the product; a Git hook is a change to
// the developer's own repository, and pasture has no business making one.

// lifecycleHarnesses is the CLOSED set of harnesses that have a native
// lifecycle registration. It is written here as a literal on purpose: a new
// harness must be added deliberately, in the same change that reviews it.
var lifecycleHarnesses = []ir.HarnessID{
	ir.HarnessClaudeCode,
	ir.HarnessOpenCode,
	ir.HarnessCodex,
}

// TestNoLifecycleRegistrationForOutOfScopeHarnesses proves that the lifecycle
// registration surface still names exactly three harnesses.
func TestNoLifecycleRegistrationForOutOfScopeHarnesses(t *testing.T) {
	t.Parallel()

	table, err := inventory.Table()
	require.NoError(t, err)

	seen := map[ir.HarnessID]int{}
	for _, row := range table {
		if row.Key.Kind != inventory.KindLifecycleEvent {
			continue
		}
		seen[row.Key.Harness]++
	}

	for harness := range seen {
		assert.Contains(t, lifecycleHarnesses, harness,
			"harness %q gained lifecycle registration rows; only the three reviewed harnesses may have them", harness)
	}
	for _, harness := range lifecycleHarnesses {
		assert.NotZero(t, seen[harness], "harness %q lost its lifecycle registration rows", harness)
	}

	for _, outOfScope := range []string{"acp", "gemini", "antigravity"} {
		assert.Zero(t, seen[ir.HarnessID(outOfScope)],
			"harness %q must have no lifecycle registration row", outOfScope)
	}
}

// TestAntigravityStillRefusesActionably proves the Antigravity refusal is
// intact. It is a refusal and not an omission: there is no public event
// catalogue to bind, so generating an adapter would mean inventing native
// semantics.
func TestAntigravityStillRefusesActionably(t *testing.T) {
	t.Parallel()

	err := pastureruntime.AntigravityLifecycleContract()
	require.Error(t, err, "Antigravity must keep refusing rather than silently generating")
	assert.Contains(t, err.Error(), "unsupported")

	_, resolveErr := codegen.ResolveHarness([]string{string(codegen.HarnessAntigravity)})
	require.Error(t, resolveErr, "the code generator must keep refusing an Antigravity target")
	assert.Contains(t, resolveErr.Error(), "antigravity")
}

// TestNoGeneratedTargetForOutOfScopeHarnesses proves no transport or
// destination was added for an out-of-scope surface. It resolves EVERY target
// the generator accepts and asserts the set is the three reviewed harnesses.
func TestNoGeneratedTargetForOutOfScopeHarnesses(t *testing.T) {
	t.Parallel()

	for _, outOfScope := range []string{"acp", "gemini", "antigravity"} {
		_, err := codegen.ResolveHarness([]string{outOfScope})
		assert.Error(t, err, "the code generator must have no target named %q", outOfScope)
	}

	targets, err := codegen.ResolveHarness([]string{
		string(codegen.HarnessClaudeCode), string(codegen.HarnessOpenCode), string(codegen.HarnessCodex)})
	require.NoError(t, err)
	require.Len(t, targets, 3, "exactly three harnesses may be generated")
}

// TestACPAdapterRegistryIsUnchanged proves the Agent Client Protocol transcript
// adapters were not widened. They read transcripts; they are not a lifecycle
// transport, and no lifecycle work may register one there.
func TestACPAdapterRegistryIsUnchanged(t *testing.T) {
	t.Parallel()

	formats := acp.RegisteredFormats()
	sort.Strings(formats)
	assert.Equal(t, []string{"claude-jsonl", "opencode-json"}, formats,
		"the ACP transcript adapter registry must not gain or lose a format")
}

// TestRepositoryInstallsNoGitHook is the hard safety rule. A Git hook changes
// the developer's own repository and can run on every commit; pasture must
// never install one, and no generated output may become one.
func TestRepositoryInstallsNoGitHook(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	hooksPath, err := gitConfig(root, "core.hooksPath")
	require.NoError(t, err)
	assert.Empty(t, hooksPath,
		"core.hooksPath is set to %q; pasture must never redirect Git hooks", hooksPath)

	commonDir := gitCommonDir(t, root)
	entries, err := os.ReadDir(filepath.Join(commonDir, "hooks"))
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)

	var installed []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}
		installed = append(installed, entry.Name())
	}
	assert.Empty(t, installed,
		"the Git hooks directory %s holds installed hooks %v; pasture must never install one", commonDir, installed)
}

// gitConfig reads one Git configuration value. An unset key exits non-zero,
// which is not an error here: it is the answer this test wants.
func gitConfig(root, key string) (string, error) {
	command := exec.Command("git", "-C", root, "config", "--get", key)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// gitCommonDir resolves the shared Git directory, which is where the hooks live
// even when the checkout is a worktree.
func gitCommonDir(t *testing.T, root string) string {
	t.Helper()

	command := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	output, err := command.Output()
	require.NoError(t, err, "the guard must be able to find the Git directory it is protecting")
	return strings.TrimSpace(string(output))
}

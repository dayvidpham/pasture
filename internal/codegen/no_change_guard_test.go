package codegen_test

import (
	"errors"
	"io/fs"
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

// TestPastureSourceInstallsNoGitHook is the hard safety rule, asserted about
// THE CODE UNDER REVIEW. A Git hook changes the developer's own repository and
// can run on every commit; pasture must never install one, redirect one, or let
// generated output become one.
//
// This is a property of pasture's SOURCE, so the source is what it reads. The
// companion test below reads the repository the suite happens to run in, which
// is a different question with a different answer, and the two must not be
// confused: a hook installed by somebody else is not a pasture defect.
func TestPastureSourceInstallsNoGitHook(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	// The forbidden acts, each named by what it would do to a user's repository.
	forbidden := map[string]string{
		"core.hooksPath":     "redirect every Git hook of the repository",
		".git/hooks":         "write into the Git hooks directory of the repository",
		"pre-commit install": "hand hook installation to another tool",
	}
	self := filepath.Join("internal", "codegen", "no_change_guard_test.go")

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "legacy" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".sh") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		// This file names the forbidden strings in order to forbid them.
		if relative == self {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Only EXECUTABLE lines are evidence. A comment that names one of these
		// strings in order to deny doing it is the opposite of the defect, and
		// treating it as a hit would teach everyone to stop writing the denial.
		for number, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			for needle, act := range forbidden {
				if strings.Contains(line, needle) {
					t.Errorf("%s line %d contains %q outside a comment, which would %s; "+
						"pasture must never install, enable or modify a Git hook",
						relative, number+1, needle, act)
				}
			}
		}
		return nil
	})
	require.NoError(t, walkErr)
}

// TestThisRepositoryHasNoInstalledGitHook reports the STATE of the checkout the
// suite runs in. It is deliberately separate from the source test above,
// because it can fail for a reason pasture does not control: a developer, or
// another tool, may install a hook in this repository. When that happens the
// message must say so and must NOT blame pasture, or a worker spends a
// diagnosis cycle on somebody else's change.
//
// core.hooksPath stays a hard failure whatever installed it: nothing in a normal
// workflow sets it, and a redirected hooks directory silently changes what runs
// on every commit.
func TestThisRepositoryHasNoInstalledGitHook(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	hooksPath, err := gitConfig(root, "core.hooksPath")
	require.NoError(t, err)
	assert.Empty(t, hooksPath,
		"core.hooksPath is set to %q in this checkout; nothing in a normal workflow sets it, so find out what did before continuing", hooksPath)

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
		"this repository has installed Git hooks %v in %s. pasture never installs one, and the companion source test proves that, "+
			"so find out which tool or person installed these and remove them before trusting a commit from this checkout",
		installed, commonDir)
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

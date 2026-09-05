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
//
// WHAT IT VISITS: every regular file under the module root whose name or
// extension can carry an executable instruction — Go, TypeScript and
// JavaScript sources (.go .ts .js .mjs .cjs), shell (.sh .bash), Python, Nix,
// Make (Makefile and .mk), JSON, YAML and TOML configuration, and any other
// regular file with an executable bit — for three needles on lines that are
// not comments, read by gitHookHitsOnLine: `hooksPath` (the Git key
// core.hooksPath, whole or with its section spelled apart) in ANY letter case,
// because Git config keys are case-insensitive and `git config --list` prints
// them lowercased, so `core.hookspath` is the spelling a script copied from
// that output carries; `.git/hooks` and `pre-commit install` as written,
// because one is a path and one is a command, and both are case-sensitive on
// the Linux filesystem and PATH this tree builds on. A hit is refused whatever
// the surrounding code does.
// WHAT IT DOES NOT READ: .git/ and legacy/ (the retired substrate, preserved
// as written); this file, which names the needles in order to forbid them; a
// key assembled at run time from parts that never spell hooksPath; a hook
// written through a path variable that never spells .git/hooks; the path or
// the command in another letter case (.GIT/HOOKS), which only a
// case-insensitive filesystem or PATH would still reach; and any file kind not
// named above. The first version read .go, .ts and .sh alone for
// `core.hooksPath` whole, so a Makefile target or a Nix expression that
// redirected the hooks directory was outside its reach, and its doc said none
// of that. The second version matched the key in one letter case, so
// `git config core.hookspath`, the spelling Git itself prints, was outside its
// reach; TestTheNoGitHookGuardReadsTheHooksPathKeyInAnyLetterCase pins the
// case axis now.
func TestPastureSourceInstallsNoGitHook(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)

	// The kinds that can carry an executable instruction. A kind not listed
	// here is outside this guard's reach, and the doc above says so.
	guardedExtensions := map[string]bool{
		".go": true, ".ts": true, ".js": true, ".mjs": true, ".cjs": true,
		".sh": true, ".bash": true, ".py": true, ".nix": true, ".mk": true,
		".json": true, ".yaml": true, ".yml": true, ".toml": true,
	}
	guardedNames := map[string]bool{"Makefile": true, "Dockerfile": true, "Justfile": true}
	self := filepath.Join("internal", "codegen", "no_change_guard_test.go")

	visited := 0
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
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		executable := info.Mode()&0o111 != 0
		if !guardedExtensions[filepath.Ext(path)] && !guardedNames[entry.Name()] && !executable {
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
		visited++
		// Only EXECUTABLE lines are evidence. A comment that names one of these
		// strings in order to deny doing it is the opposite of the defect, and
		// treating it as a hit would teach everyone to stop writing the denial.
		for number, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, hit := range gitHookHitsOnLine(line) {
				t.Errorf("%s line %d contains %q outside a comment, which would %s; "+
					"pasture must never install, enable or modify a Git hook",
					relative, number+1, hit.Spelling, hit.Act)
			}
		}
		return nil
	})
	require.NoError(t, walkErr)
	require.NotZero(t, visited,
		"the walk read no file at all, so the rule above was asserted over nothing; the kinds it "+
			"names must still exist under the module root")
}

// TestTheNoGitHookGuardReadsTheHooksPathKeyInAnyLetterCase is the negative
// control for the case axis of the guard above. Git config keys are
// case-insensitive, so the guard must refuse the hooks-path key under every
// spelling a script can carry; a reader narrowed back to one spelling turns
// this RED by name. The path and the command are matched as written, and that
// stated limit is pinned here so the doc and the reader move together.
func TestTheNoGitHookGuardReadsTheHooksPathKeyInAnyLetterCase(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"\tgit config core.hookspath .githooks", // the spelling `git config --list` prints
		"\tgit config core.hooksPath .githooks", // the spelling the Git manual prints
		"\tgit config CORE.HOOKSPATH .githooks",
		"\tgit config core.HooksPath .githooks",
		`  git config --global hooksPath /tmp/h`,
		`{"hookspath": "x"}`,
	} {
		hits := gitHookHitsOnLine(line)
		require.Len(t, hits, 1,
			"the line %q spells the Git hooks-path key, which Git reads in any letter case, and "+
				"the guard must refuse it in that letter case", line)
		assert.Equal(t, "redirect every Git hook of the repository (the Git key core.hooksPath, "+
			"matched in any letter case)", hits[0].Act)
		assert.Contains(t, strings.ToLower(line), strings.ToLower(hits[0].Spelling),
			"the spelling the guard reports must be the one the line carries")
	}

	assert.Len(t, gitHookHitsOnLine("cp hook .git/hooks/pre-commit"), 1, "the path as written is a hit")
	assert.Len(t, gitHookHitsOnLine("pre-commit install"), 1, "the command as written is a hit")
	assert.Empty(t, gitHookHitsOnLine("cp hook .GIT/HOOKS/pre-commit"),
		"the path in another letter case is the stated limit of the guard; widen the reader and "+
			"the doc together if this is to become a hit")
	assert.Empty(t, gitHookHitsOnLine("Pre-Commit install"),
		"the command in another letter case is the stated limit of the guard; widen the reader "+
			"and the doc together if this is to become a hit")
	assert.Empty(t, gitHookHitsOnLine("git config core.editor vim"), "an unrelated Git key is not a hit")
}

// gitHookHit is one forbidden needle found on one line: the spelling the line
// carries it under and what that act would do to a user's repository.
type gitHookHit struct {
	Spelling string
	Act      string
}

// gitHookNeedles is the table of forbidden acts, each named by what it would
// do to a user's repository. The Git key is matched in any letter case by an
// ASCII fold, which is the fold Git applies to a configuration key; the path
// and the command are matched as written.
var gitHookNeedles = []struct {
	text    string
	anyCase bool
	act     string
}{
	{"hooksPath", true, "redirect every Git hook of the repository (the Git key core.hooksPath, matched in any letter case)"},
	{".git/hooks", false, "write into the Git hooks directory of the repository"},
	{"pre-commit install", false, "hand hook installation to another tool"},
}

// gitHookHitsOnLine reads one line and returns every forbidden needle it
// carries, in table order, each under the spelling the line carries.
func gitHookHitsOnLine(line string) []gitHookHit {
	hits := []gitHookHit{}
	for _, needle := range gitHookNeedles {
		haystack, text := line, needle.text
		if needle.anyCase {
			haystack, text = asciiLower(line), asciiLower(needle.text)
		}
		start := strings.Index(haystack, text)
		if start == -1 {
			continue
		}
		// The fold keeps every byte in place, so the index into the folded
		// line is the index into the line as written.
		hits = append(hits, gitHookHit{Spelling: line[start : start+len(text)], Act: needle.act})
	}
	return hits
}

// asciiLower folds the ASCII letters of s to lower case and leaves every
// other byte, and so every byte offset, in place.
func asciiLower(s string) string {
	folded := []byte(s)
	for index, char := range folded {
		if 'A' <= char && char <= 'Z' {
			folded[index] = char + ('a' - 'A')
		}
	}
	return string(folded)
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

	// The guard reads the Git directory of the checkout it runs in, so it needs
	// one. A tree with no Git directory is an ordinary environment: every gate in
	// this project runs from a `git archive` copy, which carries no .git at all,
	// and a failure there would be read as a defect of the change under gate.
	if exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run() != nil {
		t.Skip("not inside a git repo; skipping the installed-hook guard")
	}

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

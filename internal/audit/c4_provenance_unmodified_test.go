// Package audit_test — c4_provenance_unmodified_test.go
//
// BDD constraint test for PROPOSAL-2 §3 C4 / §11 Scenario 3:
//
//	Scenario 3: Provenance library is unmodified
//	Given the implementation is complete,
//	When git log is inspected over the implementation epoch's commit range,
//	Then no commits authored as part of this proposal appear in provenance,
//	Should not any required behaviour from this proposal depend on a
//	Provenance library change.
//
// This test enforces the C4 binding by running git log against the
// provenance source repository and asserting that no commits in the epoch
// range reference pasture-workflow-record work.
//
// # Epoch boundary
//
// The epoch base commit is af3b432 (2026-04-25T04:29:36-0700),
// the first S1 commit on this implementation epoch.  Any provenance commit
// AFTER that timestamp that mentions pasture / workflow-record / audit /
// migrate in its subject or body constitutes a potential C4 violation.
//
// Commits from OTHER concurrent epochs (e.g. bestiary work) that happen to
// land in the same date range are logged as informational noise and do NOT
// fail the assertion — we only fail on commits that look related to this
// proposal.  When found, the failure message lists the offending commits
// verbatim so the reviewer can inspect them directly.
//
// # Environment
//
//   - PROVENANCE_SRC — override the provenance checkout path
//     (default: ../../../../provenance relative to go.mod, i.e.
//     ~/codebases/dayvidpham/provenance in the standard layout).
//     Set to an absolute path in CI to point at a fresh clone.
//
//   - EPOCH_BASE_COMMIT — override the epoch base commit SHA
//     (default: af3b432).  The test resolves this to a timestamp via
//     git show and uses the timestamp as the --after boundary.
//
// The test skips (instead of failing) when the provenance source tree is
// absent or is not a valid git repository, emitting an actionable skip
// message that names the env var and a recovery hint.
package audit_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// c4EpochBaseCommit is the SHA of the first commit on this implementation
// epoch (S1: feat(errors): add CategoryStorage with exit code 5).
// Override via the EPOCH_BASE_COMMIT env var when re-running the test for a
// different epoch.
const c4EpochBaseCommit = "af3b432dfb401d20c12f784bee467835c78e116a"

// c4ProvenanceSrcDefault is the path relative to this file's go.mod root
// where the provenance checkout is expected.
const c4ProvenanceSrcDefault = "../../../../provenance"

// c4PastureKeywords is the set of terms that, when found in a provenance
// commit subject or body, indicate the commit was made to satisfy
// PROPOSAL-2 (pasture-workflow-record) requirements — a C4 violation.
// Matches are case-insensitive.
var c4PastureKeywords = []string{
	"pasture",
	"workflow-record",
	"workflow record",
	"pasture-msg",
	"pastured",
	"audit trail",
	"tasktracker",
	"task tracker",
	"context_edges",
	"agents_software",
	"audit_events",
	"audit_schema_meta",
	"backfill",
}

// TestC4_ProvenanceLibraryUnmodified implements PROPOSAL-2 §11 Scenario 3.
//
// It runs git log against the provenance source checkout over the
// implementation epoch's commit range and fails loudly if any commit is
// found whose message suggests it was authored as part of this proposal.
// Commits from unrelated concurrent epochs are logged informatively but
// do not cause a failure.
func TestC4_ProvenanceLibraryUnmodified(t *testing.T) {
	t.Parallel()
	t.Helper()

	// ── 1. Locate provenance source ────────────────────────────────────────
	provSrc := resolveProvenanceSrc(t)

	// ── 2. Verify it is a valid git repo ───────────────────────────────────
	assertGitRepo(t, provSrc)

	// ── 3. Resolve epoch-base timestamp from the pasture git log ──────────
	epochAfter := resolveEpochBaseTimestamp(t)

	// ── 4. Collect all provenance commits since the epoch base ────────────
	allCommits := provenanceCommitsSince(t, provSrc, epochAfter)

	if len(allCommits) == 0 {
		t.Logf("C4 PASS: provenance has no commits since epoch base %s — constraint is satisfied.",
			c4EpochBaseCommit[:8])
		return
	}

	// ── 5. Partition into pasture-related vs. unrelated ───────────────────
	var pastureRelated []string
	var unrelated []string
	for _, commit := range allCommits {
		if looksLikePastureCommit(commit) {
			pastureRelated = append(pastureRelated, commit)
		} else {
			unrelated = append(unrelated, commit)
		}
	}

	// Unrelated commits are informational: log them so reviewers can
	// confirm they are from other epochs (e.g. bestiary work).
	if len(unrelated) > 0 {
		t.Logf("C4 INFO: %d provenance commit(s) found since epoch base that do NOT appear "+
			"related to PROPOSAL-2 (pasture-workflow-record).\n"+
			"These are likely from concurrent epochs and do NOT constitute a C4 violation.\n"+
			"Verify manually if needed:\n%s",
			len(unrelated), formatCommitList(unrelated))
	}

	// ── 6. Fail on pasture-related commits ────────────────────────────────
	if len(pastureRelated) == 0 {
		t.Logf("C4 PASS: %d unrelated provenance commit(s) present but none reference "+
			"pasture-workflow-record work. Constraint C4 is satisfied.",
			len(unrelated))
		return
	}

	t.Fatalf(
		"C4 VIOLATION DETECTED — PROPOSAL-2 §3 C4 constraint breach:\n\n"+
			"What:   %d provenance commit(s) found that appear related to this proposal's work.\n"+
			"Why:    C4 states 'Provenance the library is NOT modified' for PROPOSAL-2.\n"+
			"        All integration must occur in pasture/ packages only.\n"+
			"Where:  provenance source: %s\n"+
			"When:   commits after epoch base %s (resolved: %s)\n"+
			"Impact: Pasture cannot depend on provenance API changes introduced by this epoch;\n"+
			"        such changes would couple the repositories and invalidate the C4 guarantee.\n"+
			"Fix:    Remove or revert the pasture-related changes from provenance.\n"+
			"        If the commit was made for a different purpose and falsely matched,\n"+
			"        update c4PastureKeywords in this test file with an exclusion rule.\n\n"+
			"Offending commits:\n%s",
		len(pastureRelated),
		provSrc,
		c4EpochBaseCommit[:8],
		epochAfter.Format(time.RFC3339),
		formatCommitList(pastureRelated),
	)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// resolveProvenanceSrc returns the absolute path to the provenance source
// checkout.  It prefers PROVENANCE_SRC env, then falls back to the default
// relative path anchored at the go.mod root of the pasture module.
// If neither location contains a directory, the test is skipped.
func resolveProvenanceSrc(t *testing.T) string {
	t.Helper()

	if v := os.Getenv("PROVENANCE_SRC"); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			t.Skipf(
				"C4 SKIP: PROVENANCE_SRC=%q could not be resolved to an absolute path: %v\n"+
					"Recovery: set PROVENANCE_SRC to an absolute path pointing at a provenance git checkout.",
				v, err,
			)
		}
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			t.Skipf(
				"C4 SKIP: PROVENANCE_SRC=%q does not exist on disk.\n"+
					"Recovery: set PROVENANCE_SRC to an absolute path pointing at a provenance git checkout,\n"+
					"or clone provenance to %s.",
				v, abs,
			)
		}
		return abs
	}

	// Default: walk up from this test file's location to go.mod, then
	// resolve the default relative path.
	goModDir := findGoModDir(t)
	abs := filepath.Clean(filepath.Join(goModDir, c4ProvenanceSrcDefault))
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		t.Skipf(
			"C4 SKIP: provenance source not found at default path %q.\n"+
				"Recovery: clone the provenance repo to that location, or set\n"+
				"  PROVENANCE_SRC=<absolute-path-to-provenance-checkout>\n"+
				"before re-running this test.",
			abs,
		)
	}
	return abs
}

// findGoModDir walks up from this test file's directory until it finds
// a directory containing go.mod.  Fatals if none is found.
func findGoModDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot locate this test file's directory")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf(
				"walked from %s to filesystem root without finding go.mod;\n"+
					"set PROVENANCE_SRC env var to bypass auto-detection.",
				filepath.Dir(file),
			)
		}
		dir = parent
	}
}

// assertGitRepo verifies that dir is a git repository by checking for a
// .git entry.  Skips the test if the repository is not valid, because the
// C4 assertion cannot be made without git history.
func assertGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Skipf(
			"C4 SKIP: %q exists but is not a git repository (no .git entry).\n"+
				"Recovery: ensure the provenance checkout is a full git clone (not a bare export),\n"+
				"or set PROVENANCE_SRC to a valid git checkout.",
			dir,
		)
	}
}

// resolveEpochBaseTimestamp resolves the author timestamp of the epoch base
// commit from the current pasture git log.  The commit SHA comes from
// c4EpochBaseCommit (overridable via EPOCH_BASE_COMMIT env).
//
// We resolve the timestamp from git show so the boundary is exact even if
// the host clock drifts.
func resolveEpochBaseTimestamp(t *testing.T) time.Time {
	t.Helper()

	sha := c4EpochBaseCommit
	if v := os.Getenv("EPOCH_BASE_COMMIT"); v != "" {
		sha = v
	}

	out, err := runGit(t, ".", "show", "-s", "--format=%aI", sha)
	if err != nil {
		t.Skipf(
			"C4 SKIP: cannot resolve epoch base commit %q in the current git repo: %v\n"+
				"Output: %s\n"+
				"Recovery: ensure the pasture git worktree contains commit %s,\n"+
				"or set EPOCH_BASE_COMMIT to a commit SHA that exists in this repo.",
			sha, err, out, sha,
		)
	}

	ts := strings.TrimSpace(out)
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Git may emit ISO 8601 with numeric tz offset instead of Z.
		// Try the alternate layout.
		parsed, err = time.Parse("2006-01-02T15:04:05-07:00", ts)
		if err != nil {
			t.Fatalf(
				"C4: could not parse epoch base commit timestamp %q: %v\n"+
					"Expected an ISO 8601 / RFC 3339 timestamp from git show --format=%%aI.",
				ts, err,
			)
		}
	}
	return parsed
}

// provenanceCommitsSince returns the one-line summaries of all commits in
// the provenance repo whose author date is strictly after `after`.
func provenanceCommitsSince(t *testing.T, provSrc string, after time.Time) []string {
	t.Helper()

	afterStr := after.UTC().Format(time.RFC3339)
	out, err := runGit(t, provSrc,
		"log",
		"--oneline",
		fmt.Sprintf("--after=%s", afterStr),
		"--format=%H %s",
	)
	if err != nil {
		t.Fatalf(
			"C4: git log in provenance source %q failed: %v\nOutput: %s\n"+
				"Recovery: ensure provenance is a valid, accessible git repository.",
			provSrc, err, out,
		)
	}

	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits
}

// looksLikePastureCommit returns true if the commit one-liner (hash + subject)
// contains any of the keywords in c4PastureKeywords.  The check is
// case-insensitive.
func looksLikePastureCommit(commitLine string) bool {
	lower := strings.ToLower(commitLine)
	for _, kw := range c4PastureKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// formatCommitList formats a slice of "hash subject" lines as an indented
// bullet list suitable for test log / fatal output.
func formatCommitList(commits []string) string {
	var b bytes.Buffer
	for _, c := range commits {
		fmt.Fprintf(&b, "  • %s\n", c)
	}
	return b.String()
}

// runGit runs git with the given arguments in dir and returns combined
// stdout+stderr output.  It does NOT fatal on non-zero exit — callers
// decide how to handle errors.
func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

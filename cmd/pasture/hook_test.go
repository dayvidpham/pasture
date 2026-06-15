package main_test

// hook_test.go — Cobra-layer wiring tests for `pasture hook record`.
//
// These run the compiled binary (TestMain) so the cobra RunE wiring — flag
// registration, Changed()->&v pointer conversion, cobra.NoArgs, and the
// exitWithCode/os.Exit path — is exercised end-to-end. The handler-level tests
// in internal/handlers/hook_test.go cover the recording logic; these close the
// previously-deferred CLI-wiring gap.

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestCLI_HookRecord_FlagWiring_RoundTrips runs the real `hook record`
// subcommand with all metadata flags set, then reads the event back via
// `task events` and asserts the flag values reached the recorded payload —
// proving the cobra flag binding (incl. the optional-pointer conversion) works.
func TestCLI_HookRecord_FlagWiring_RoundTrips(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	const sha = "facefeed00000000000000000000000000001234"

	out := runCLI(t,
		"--db", db,
		"hook", "record",
		"--event", "git-commit",
		"--sha", sha,
		"--message", "fix: wiring",
		"--author", "Test Person <test@example.com>",
		"--branch", "wiring-branch",
		"--timestamp", "2026-03-03T03:03:03Z",
	)
	if out.exitCode != 0 {
		t.Fatalf("hook record exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stdout, "recorded git-commit event for sha "+sha) {
		t.Errorf("success line missing; stdout=%q", out.stdout)
	}
	// Assert the event-id suffix is present end-to-end through the real binary.
	if !strings.Contains(out.stdout, "(event #") {
		t.Errorf("event-id suffix '(event #' missing from success line; stdout=%q", out.stdout)
	}

	// Read the stored payload directly so the assertion isn't subject to the
	// text formatter's payload truncation. The flags must have reached the
	// recorded GitCommit row (proving the cobra binding). Avoid < > here since
	// json.Marshal escapes them (</>) — assert on plain substrings.
	payload := selectGitCommitPayload(t, db, sha)
	for _, want := range []string{sha, "fix: wiring", "Test Person", "wiring-branch", "2026-03-03T03:03:03Z"} {
		if !strings.Contains(payload, want) {
			t.Errorf("recorded payload missing %q; payload=%s", want, payload)
		}
	}
}

// TestCLI_HookRecord_FormatJSON_EmitsMetadata asserts the global --format json
// flag is honored and the JSON output includes the seven commit camelCase keys
// (eventType, sha, eventId, message, author, branch, timestamp) when all four
// metadata flags are supplied. Because all four commit flags are set, git is
// NOT consulted, so repo/remotes are absent and the count stays at 7.
func TestCLI_HookRecord_FormatJSON_EmitsMetadata(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	const sha = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"

	out := runCLI(t,
		"--db", db,
		"--format", "json",
		"hook", "record",
		"--event", "git-commit",
		"--sha", sha,
		"--message", "fix: json",
		"--author", "JSON Person <json@example.com>",
		"--branch", "json-branch",
		"--timestamp", "2026-05-05T05:05:05Z",
	)
	if out.exitCode != 0 {
		t.Fatalf("hook record --format json exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.stdout), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%q", err, out.stdout)
	}
	// All four commit flags supplied → git not consulted → repo/remotes absent → 7 keys.
	if len(decoded) != 7 {
		t.Errorf("JSON output has %d keys, want exactly 7 (eventType, sha, eventId, message, author, branch, timestamp); got %v", len(decoded), decoded)
	}
	if decoded["eventType"] != "git-commit" {
		t.Errorf("eventType = %v, want %q", decoded["eventType"], "git-commit")
	}
	if decoded["sha"] != sha {
		t.Errorf("sha = %v, want %q", decoded["sha"], sha)
	}
	// JSON numbers decode to float64; assert eventId is present and positive.
	id, ok := decoded["eventId"].(float64)
	if !ok {
		t.Fatalf("eventId missing or not a number: %v", decoded["eventId"])
	}
	if id <= 0 {
		t.Errorf("eventId = %v, want a positive audit_events row id", id)
	}
	if decoded["message"] != "fix: json" {
		t.Errorf("message = %v, want %q", decoded["message"], "fix: json")
	}
	if decoded["author"] != "JSON Person <json@example.com>" {
		t.Errorf("author = %v, want %q", decoded["author"], "JSON Person <json@example.com>")
	}
	if decoded["branch"] != "json-branch" {
		t.Errorf("branch = %v, want %q", decoded["branch"], "json-branch")
	}
	if decoded["timestamp"] != "2026-05-05T05:05:05Z" {
		t.Errorf("timestamp = %v, want %q", decoded["timestamp"], "2026-05-05T05:05:05Z")
	}
}

// TestCLI_HookRecord_FormatJSON_EmitsRepoAndRemotes asserts that when git IS
// consulted (a commit field omitted), the JSON output includes "repo" and
// "remotes" keys. The --repo and --remote override flags are used to supply
// deterministic values without depending on the test environment's git state.
func TestCLI_HookRecord_FormatJSON_EmitsRepoAndRemotes(t *testing.T) {
	db := newDB(t)
	const sha = "5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e"

	// Run from the pasture repo root so the real git gatherer can find the sha.
	// We also supply --repo and --remote to get deterministic JSON output,
	// because the actual git state of the test environment may vary.
	// Only THREE commit flags are supplied (branch absent) so git IS consulted
	// and the --repo/--remote overrides take effect.
	repoRoot, err := runGitCmd("rev-parse", "--show-toplevel")
	if err != nil {
		t.Skip("not inside a git repo; skipping repo+remotes CLI test")
	}
	t.Chdir(strings.TrimSpace(repoRoot))

	realSHAOut, err := runGitCmd("rev-parse", "HEAD")
	if err != nil {
		t.Skip("git rev-parse HEAD failed; skipping repo+remotes CLI test")
	}
	realSHA := strings.TrimSpace(realSHAOut)

	out := runCLI(t,
		"--db", db,
		"--format", "json",
		"hook", "record",
		"--event", "git-commit",
		"--sha", realSHA,
		"--message", "fix: repo-remotes test",
		"--author", "Test Person <test@example.com>",
		"--timestamp", "2026-06-07T12:00:00Z",
		// branch is ABSENT → git consulted → --repo/--remote overrides take effect
		"--repo", "dayvidpham/pasture",
		"--remote", "origin=git@github.com:dayvidpham/pasture.git",
	)
	_ = sha // sha const defined above but we use realSHA for the actual run
	if out.exitCode != 0 {
		t.Fatalf("hook record exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.stdout), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%q", err, out.stdout)
	}

	// repo must be present with the override value.
	if decoded["repo"] != "dayvidpham/pasture" {
		t.Errorf("repo = %v, want %q", decoded["repo"], "dayvidpham/pasture")
	}
	// remotes must be a map containing origin.
	gotRemotes, _ := decoded["remotes"].(map[string]any)
	if gotRemotes == nil {
		t.Fatalf("remotes key missing or not a map in JSON; got %v", decoded)
	}
	if gotRemotes["origin"] != "git@github.com:dayvidpham/pasture.git" {
		t.Errorf("remotes[origin] = %v, want %q", gotRemotes["origin"], "git@github.com:dayvidpham/pasture.git")
	}
}

// runGitCmd runs git with the given args and returns (stdout, error).
func runGitCmd(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

// selectGitCommitPayload returns the JSON payload of the single GitCommit
// audit_events row keyed to sha via a fresh read handle.
func selectGitCommitPayload(t *testing.T, dbPath, sha string) string {
	t.Helper()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer conn.Close()
	var payload string
	err = conn.QueryRow(
		`SELECT ae.payload FROM audit_events ae
		 JOIN context_edges ce ON ce.event_id = ae.id
		 WHERE ce.context_kind = 'GitContext' AND ce.context_id = ? AND ae.event_type = 'GitCommit'`,
		sha,
	).Scan(&payload)
	if err != nil {
		t.Fatalf("select payload for sha %s: %v", sha, err)
	}
	return payload
}

// TestCLI_HookRecord_UnknownEvent_Exit1 asserts the cobra path surfaces the
// handler's actionable validation error (exit 1) for an unsupported --event.
func TestCLI_HookRecord_UnknownEvent_Exit1(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	out := runCLI(t,
		"--db", db,
		"hook", "record",
		"--event", "git-push",
		"--sha", "abc123",
	)
	if out.exitCode != 1 {
		t.Fatalf("unknown-event exit %d, want 1; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stderr, "git-commit") {
		t.Errorf("unknown-event stderr should list supported events; got:\n%s", out.stderr)
	}
}

// TestCLI_HookRecord_MissingSHA_Exit1 asserts the cobra path surfaces the
// handler's actionable validation error (exit 1) when --sha is omitted.
func TestCLI_HookRecord_MissingSHA_Exit1(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	out := runCLI(t,
		"--db", db,
		"hook", "record",
		"--event", "git-commit",
	)
	if out.exitCode != 1 {
		t.Fatalf("missing-sha exit %d, want 1; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stderr, "--sha") {
		t.Errorf("missing-sha stderr should mention --sha; got:\n%s", out.stderr)
	}
}

// TestCLI_HookRecord_RejectsPositionalArgs asserts cobra.NoArgs is wired —
// an unexpected positional argument is rejected.
func TestCLI_HookRecord_RejectsPositionalArgs(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	out := runCLI(t,
		"--db", db,
		"hook", "record",
		"--event", "git-commit",
		"--sha", "abc123",
		"unexpected-positional",
	)
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for positional arg; stdout=%q stderr=%q", out.stdout, out.stderr)
	}
}

package handlers_test

// hook_test.go — Tests for `pasture hook record` (PROPOSAL-1, aura-plugins-3lzsc,
// SLICE-1 L4).
//
// The system under test is handlers.HookRecord + the full Manager-path wire
// (hooks.Manager.Dispatch → GitRecorder.Handle → tasks.RecordGitEvent →
// pasture.db). Per pasture/CLAUDE.md we do NOT mock the SUT or the storage
// layer — the DB is a real file-backed t.TempDir() pasture.db. The ONLY
// injected dependency is the git-metadata gatherer (handlers.GitMetaGatherer),
// so merge-precedence is unit-testable without shelling git.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// strptr returns a pointer to s — for setting optional metadata flags.
func strptr(s string) *string { return &s }

// decodeAuditPayload SELECTs the JSON payload of the single audit_events row of
// the given event_type and decodes it. Fails if there is not exactly one row.
// The existing query helpers do not project the payload column, so this is the
// only place "metadata in payload" is verifiable.
func decodeAuditPayload(t *testing.T, dbPath string, eventType protocol.EventType) map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verify: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT payload FROM audit_events WHERE event_type = ?`, string(eventType))
	if err != nil {
		t.Fatalf("query payload: %v", err)
	}
	defer rows.Close()

	var payloads []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) != 1 {
		t.Fatalf("audit_events(%s) row count = %d, want exactly 1", eventType, len(payloads))
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(payloads[0]), &decoded); err != nil {
		t.Fatalf("payload is not valid JSON (%q): %v", payloads[0], err)
	}
	return decoded
}

// countContextEdges counts context_edges rows for the (kind, contextId) pair.
func countContextEdges(t *testing.T, dbPath string, kind protocol.ContextKind, contextId string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verify: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM context_edges WHERE context_kind = ? AND context_id = ?`,
		string(kind), contextId,
	).Scan(&n); err != nil {
		t.Fatalf("count context_edges: %v", err)
	}
	return n
}

// assertString fails unless decoded[key] equals want.
func assertString(t *testing.T, decoded map[string]any, key, want string) {
	t.Helper()
	got, ok := decoded[key]
	if !ok {
		t.Errorf("payload missing key %q (want %q)", key, want)
		return
	}
	if gotStr, _ := got.(string); gotStr != want {
		t.Errorf("payload[%q] = %v, want %q", key, got, want)
	}
}

// requireValidationError asserts err is a *StructuredError with CategoryValidation
// and that the exit code is 1.
func requireValidationError(t *testing.T, code int, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (validation)", code)
	}
}

// ─── (a) Injectable gatherer — merge precedence (no git required) ─────────────

func TestHookRecord_MergePrecedence_FlagsOverrideGit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "0123456789abcdef0123456789abcdef01234567"

	// Fake gatherer supplies all four git-derived fields.
	fake := func(s string) (map[string]string, error) {
		if s != sha {
			t.Errorf("gatherer called with sha %q, want %q", s, sha)
		}
		return map[string]string{
			"message":   "git-derived message",
			"author":    "Git Author <git@example.com>",
			"branch":    "git-branch",
			"timestamp": "2026-01-01T00:00:00Z",
		}, nil
	}

	// Explicit flags for message + author; branch + timestamp absent (git fills).
	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Message:  strptr("flag message"),
		Author:   strptr("Flag Author <flag@example.com>"),
		Gatherer: fake,
	}

	var out bytes.Buffer
	code, err := handlers.HookRecord(&out, in)
	if err != nil {
		t.Fatalf("HookRecord: %v (code %d)", err, code)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "sha", sha)
	// Flags win where set...
	assertString(t, decoded, "message", "flag message")
	assertString(t, decoded, "author", "Flag Author <flag@example.com>")
	// ...git fills where the flag is absent.
	assertString(t, decoded, "branch", "git-branch")
	assertString(t, decoded, "timestamp", "2026-01-01T00:00:00Z")
}

func TestHookRecord_MergePrecedence_GitFillsWhenFlagsAbsent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "ffffffffffffffffffffffffffffffffffffffff"

	fake := func(string) (map[string]string, error) {
		return map[string]string{
			"message":   "only from git",
			"author":    "Solo <solo@example.com>",
			"branch":    "feature/x",
			"timestamp": "2026-02-02T12:34:56Z",
		}, nil
	}

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: fake, // no metadata flags at all
	}

	var out bytes.Buffer
	if code, err := handlers.HookRecord(&out, in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "message", "only from git")
	assertString(t, decoded, "author", "Solo <solo@example.com>")
	assertString(t, decoded, "branch", "feature/x")
	assertString(t, decoded, "timestamp", "2026-02-02T12:34:56Z")
}

// TestHookRecord_MergePrecedence_ExplicitEmptyOverridesGit asserts the
// documented contract that a flag set to the EMPTY string (non-nil pointer)
// overrides the git-derived value — i.e. "absent" (nil → git fills) and
// "explicitly empty" (override to "") are distinct, observable in the payload.
func TestHookRecord_MergePrecedence_ExplicitEmptyOverridesGit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "1111111111111111111111111111111111111111"

	// Git would supply non-empty message AND branch...
	fake := func(string) (map[string]string, error) {
		return map[string]string{
			"message": "git message that must be overridden",
			"branch":  "git-branch-that-must-be-overridden",
			"author":  "Kept Author <kept@example.com>",
		}, nil
	}

	// ...but the caller explicitly clears message + branch (empty-string flags),
	// while leaving author absent (git fills it).
	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Message:  strptr(""),
		Branch:   strptr(""),
		Gatherer: fake,
	}

	var out bytes.Buffer
	if code, err := handlers.HookRecord(&out, in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	// Explicit empty wins over git's non-empty value (observable as "").
	assertString(t, decoded, "message", "")
	assertString(t, decoded, "branch", "")
	// Absent flag still lets git fill in.
	assertString(t, decoded, "author", "Kept Author <kept@example.com>")
}

// ─── (b) Lightweight integration smoke — one GitCommit row + ContextGit edge ──

func TestHookRecord_WritesOneGitCommitRowAndEdge(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "abc1230000000000000000000000000000000000"

	// Empty gatherer: assert the SUT records even with no derivable metadata.
	empty := func(string) (map[string]string, error) { return map[string]string{}, nil }

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: empty,
	}

	var out bytes.Buffer
	if code, err := handlers.HookRecord(&out, in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	// Exactly one GitCommit audit row, with sha in its payload...
	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "sha", sha)
	// ...linked to the sha via exactly one ContextGit edge.
	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 1 {
		t.Errorf("context_edges (GitContext, %q) = %d, want 1", sha, got)
	}
}

// ─── (b') Real-git integration — derive metadata from an actual commit ────────

func TestHookRecord_RealGit_DerivesMetadataFromCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping real-git integration test")
	}

	repo := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Initialise a repo with a deterministic identity + a single commit.
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Ada Lovelace",
			"GIT_AUTHOR_EMAIL=ada@example.com",
			"GIT_COMMITTER_NAME=Ada Lovelace",
			"GIT_COMMITTER_EMAIL=ada@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "trunk")
	runGit("config", "user.name", "Ada Lovelace")
	runGit("config", "user.email", "ada@example.com")
	runGit("commit", "--allow-empty", "-m", "feat: first commit")

	// Resolve the commit sha.
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = repo
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	sha := string(bytes.TrimSpace(shaOut))

	// Run the handler from inside the repo so the DEFAULT (real-git) gatherer
	// resolves the commit. --sha ONLY: every metadata field must come from git.
	t.Chdir(repo)

	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  string(handlers.HookEventGitCommit),
		SHA:    sha,
		// Gatherer nil → the production gatherGitMeta path.
	}

	var out bytes.Buffer
	if code, err := handlers.HookRecord(&out, in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "sha", sha)
	assertString(t, decoded, "message", "feat: first commit")
	assertString(t, decoded, "author", "Ada Lovelace <ada@example.com>")
	assertString(t, decoded, "branch", "trunk")
	// timestamp is git-formatted ISO-8601; assert it is present and non-empty.
	if ts, _ := decoded["timestamp"].(string); ts == "" {
		t.Errorf("payload[timestamp] missing or empty; want git-derived committer date")
	}
}

// ─── (d) Error cases — unknown --event and missing --sha (actionable) ─────────

func TestHookRecord_UnknownEvent_ActionableError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  "git-push", // not supported in this slice
		SHA:    "deadbeef",
	}
	var out bytes.Buffer
	code, err := handlers.HookRecord(&out, in)
	requireValidationError(t, code, err)

	// The error must list the supported events so the user can self-correct.
	// As() is REQUIRED to succeed — a failed type assertion must fail the test,
	// not silently skip the Fix-content assertion.
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("unknown-event error is not *StructuredError: %v", err)
	}
	if !bytesContains(se.Fix, "git-commit") {
		t.Errorf("unknown-event Fix should list supported events incl. git-commit; got:\n%s", se.Fix)
	}
}

func TestHookRecord_MissingSHA_ActionableError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  string(handlers.HookEventGitCommit),
		SHA:    "   ", // whitespace-only → treated as empty
	}
	var out bytes.Buffer
	code, err := handlers.HookRecord(&out, in)
	requireValidationError(t, code, err)
}

// bytesContains reports whether s contains substr (avoids importing strings
// twice for one call in the test body).
func bytesContains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

package handlers_test

// hook_test.go — Tests for `pasture hook record`.
//
// The system under test is handlers.HookRecord + the full Manager-path wire
// (hooks.Manager.Dispatch → GitRecorder.Handle → tasks.RecordGitEvent →
// pasture.db). Per pasture/CLAUDE.md we do NOT mock the SUT or the storage
// layer — the DB is a real file-backed t.TempDir() pasture.db. The ONLY
// injected dependency is the git-metadata gatherer (handlers.GitMetaGatherer),
// so merge-precedence is unit-testable without shelling git.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/hooks"
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

// countAuditEventsByType counts audit_events rows of the given event_type.
func countAuditEventsByType(t *testing.T, dbPath string, eventType protocol.EventType) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open verify: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`,
		string(eventType),
	).Scan(&n); err != nil {
		t.Fatalf("count audit_events: %v", err)
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
	fake := func(s string) (handlers.GitMeta, error) {
		if s != sha {
			t.Errorf("gatherer called with sha %q, want %q", s, sha)
		}
		return handlers.GitMeta{
			Message:   "git-derived message",
			Author:    "Git Author <git@example.com>",
			Branch:    "git-branch",
			Timestamp: "2026-01-01T00:00:00Z",
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

	_, code, err := handlers.HookRecord(in)
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

	fake := func(string) (handlers.GitMeta, error) {
		return handlers.GitMeta{
			Message:   "only from git",
			Author:    "Solo <solo@example.com>",
			Branch:    "feature/x",
			Timestamp: "2026-02-02T12:34:56Z",
		}, nil
	}

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: fake, // no metadata flags at all
	}

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
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
	fake := func(string) (handlers.GitMeta, error) {
		return handlers.GitMeta{
			Message: "git message that must be overridden",
			Branch:  "git-branch-that-must-be-overridden",
			Author:  "Kept Author <kept@example.com>",
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

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
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
	empty := func(string) (handlers.GitMeta, error) { return handlers.GitMeta{}, nil }

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: empty,
	}

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
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

// ─── (02qmh) Surface the recorded audit_events row id ─────────────────────────

// TestHookRecord_SurfacesRecordedEventID asserts the success result carries the
// audit_events row id of the event just recorded, and that the id matches the
// row actually written. This is the dispatch-tied event-id contract: the id is
// read back from the Manager dispatch result, not from a stateful "last id".
func TestHookRecord_SurfacesRecordedEventID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d"

	empty := func(string) (handlers.GitMeta, error) { return handlers.GitMeta{}, nil }

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: empty,
	}

	result, code, err := handlers.HookRecord(in)
	if err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	if result.EventType != "git-commit" {
		t.Errorf("result.EventType = %q, want %q", result.EventType, "git-commit")
	}
	if result.SHA != sha {
		t.Errorf("result.SHA = %q, want %q", result.SHA, sha)
	}
	if result.EventID <= 0 {
		t.Fatalf("result.EventID = %d, want a positive audit_events row id", result.EventID)
	}

	// The surfaced id must be the id of the row that was actually written.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var gotID int64
	if err := db.QueryRow(
		`SELECT id FROM audit_events WHERE event_type = ?`,
		string(protocol.EventType("GitCommit")),
	).Scan(&gotID); err != nil {
		t.Fatalf("select recorded row id: %v", err)
	}
	if gotID != result.EventID {
		t.Errorf("surfaced EventID = %d, but audit_events row id = %d", result.EventID, gotID)
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

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
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

// ─── Fail-hard git gather — attempted+failed → record nothing ─────────────────

// TestHookRecord_GatherFails_FailsHardRecordsNothing: when a metadata flag is
// absent the gatherer is consulted, and if it FAILS the handler must return an
// actionable validation error (exit 1) and record NOTHING. Uses an injected
// failing fake so the failure-propagation path is unit-testable without git.
func TestHookRecord_GatherFails_FailsHardRecordsNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	failing := func(string) (handlers.GitMeta, error) {
		return handlers.GitMeta{}, stderrors.New("simulated: not a git repository")
	}

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: failing, // --sha only → all four fields absent → gather attempted
	}

	_, code, err := handlers.HookRecord(in)
	requireValidationError(t, code, err)

	// Actionable Fix must guide the user (run inside the repo / pass flags).
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("gather-failure error is not *StructuredError: %v", err)
	}
	// The Fix field must guide the user with full commands, readable
	// placeholders, and a concrete worked example — not cryptic shorthands.
	for _, want := range []string{
		"cd <path-to-repo>",              // remedy 1 placeholder
		"--message \"<commit message>\"", // remedy 2 readable placeholder
		"--author \"<name> <email>\"",    // remedy 2 readable placeholder
		"--branch \"<branch>\"",          // remedy 2 readable placeholder
		"jane@example.com",               // concrete example (author)
		"fix: handle nil config",         // concrete example (message)
	} {
		if !bytesContains(se.Fix, want) {
			t.Errorf("gather-failure Fix missing %q; got:\n%s", want, se.Fix)
		}
	}

	// NOTHING recorded: zero GitCommit rows and zero ContextGit edges for the sha.
	if got := countAuditEventsByType(t, dbPath, protocol.EventType("GitCommit")); got != 0 {
		t.Errorf("audit_events(GitCommit) = %d, want 0 (must record nothing on fail-hard)", got)
	}
	if got := countContextEdges(t, dbPath, protocol.ContextGit, sha); got != 0 {
		t.Errorf("context_edges (GitContext, %q) = %d, want 0", sha, got)
	}
}

// TestHookRecord_RealGit_OutsideRepo_FailsHard exercises the DEFAULT gatherer's
// fail-hard behaviour: running with --sha only from a directory that is not a
// git repo must fail non-zero and record nothing.
func TestHookRecord_RealGit_OutsideRepo_FailsHard(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping real-git fail-hard test")
	}
	nonRepo := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "pasture.db") // absolute → cwd-independent
	const sha = "0000000000000000000000000000000000000000"

	t.Chdir(nonRepo) // git commands now run outside any repo

	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  string(handlers.HookEventGitCommit),
		SHA:    sha,
		// Gatherer nil → real gatherGitMeta, which fails outside a repo.
	}

	_, code, err := handlers.HookRecord(in)
	requireValidationError(t, code, err)

	if got := countAuditEventsByType(t, dbPath, protocol.EventType("GitCommit")); got != 0 {
		t.Errorf("audit_events(GitCommit) = %d, want 0 (outside-repo gather must record nothing)", got)
	}
}

// TestHookRecord_AllFlagsSupplied_SkipsGather: when ALL four metadata fields are
// supplied explicitly, the gatherer is NEVER consulted — proven by injecting a
// gatherer that would FAIL if called, yet the record still succeeds (exit 0).
func TestHookRecord_AllFlagsSupplied_SkipsGather(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "9999999999999999999999999999999999999999"

	mustNotBeCalled := func(string) (handlers.GitMeta, error) {
		t.Errorf("gatherer must NOT be called when all metadata flags are supplied")
		return handlers.GitMeta{}, stderrors.New("should not happen")
	}

	in := handlers.HookRecordInput{
		DBPath:    dbPath,
		Event:     string(handlers.HookEventGitCommit),
		SHA:       sha,
		Message:   strptr("explicit msg"),
		Author:    strptr("Explicit <e@example.com>"),
		Branch:    strptr("explicit-branch"),
		Timestamp: strptr("2026-04-04T04:04:04Z"),
		Gatherer:  mustNotBeCalled,
	}

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d (all-flags path must not consult git)", err, code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "message", "explicit msg")
	assertString(t, decoded, "author", "Explicit <e@example.com>")
	assertString(t, decoded, "branch", "explicit-branch")
	assertString(t, decoded, "timestamp", "2026-04-04T04:04:04Z")
}

// ─── (d) Error cases — unknown --event and missing --sha (actionable) ─────────

func TestHookRecord_UnknownEvent_ActionableError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  "git-push", // not supported in this slice
		SHA:    "deadbeef",
	}
	_, code, err := handlers.HookRecord(in)
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
	_, code, err := handlers.HookRecord(in)
	requireValidationError(t, code, err)
}

// ─── Cause preservation on gather failure ─────────────────────────────────────

// TestHookRecord_GatherFails_CauseIsReachable asserts that the wrapped
// underlying gather error is reachable via errors.As/Unwrap, so a regression
// that drops Cause: is caught.
func TestHookRecord_GatherFails_CauseIsReachable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "cafebabedeadbeefcafebabedeadbeef0badcafe"

	sentinel := stderrors.New("sentinel gather failure")
	failing := func(string) (handlers.GitMeta, error) { return handlers.GitMeta{}, sentinel }

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: failing,
	}

	_, _, err := handlers.HookRecord(in)
	if err == nil {
		t.Fatal("expected error from failing gatherer, got nil")
	}
	// The sentinel gather error must be reachable through the StructuredError chain.
	if !stderrors.Is(err, sentinel) {
		t.Errorf("errors.Is(err, sentinel) = false; Cause field is not wired through the error chain: %v", err)
	}
}

// ─── Empty-RecordedEventIDs guard — injectable registrar seam + test ──────────

// nonRecordingHandler is a hook handler that subscribes to HookGitCommit but
// returns a zero HandleOutcome (never records anything). Used to exercise the
// post-dispatch empty-guard in HookRecord without a real recorder.
type nonRecordingHandler struct{}

func (h *nonRecordingHandler) Handle(_ context.Context, _ hooks.HookPayload) (hooks.HandleOutcome, error) {
	return hooks.HandleOutcome{}, nil // zero outcome — no ids
}

func (h *nonRecordingHandler) Events() []hooks.HookEvent {
	return []hooks.HookEvent{hooks.HookGitCommit}
}

// requireStorageError asserts err is a *StructuredError with CategoryStorage
// and exit code 5. Used to verify the post-dispatch empty-guard branch.
func requireStorageError(t *testing.T, code int, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected storage error, got nil")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryStorage)
	}
	if code != 5 {
		t.Errorf("exit code = %d, want 5 (storage)", code)
	}
}

// TestHookRecord_EmptyGuard_NoRecorderReported exercises the post-dispatch
// guard that fires when all handlers returned a zero HandleOutcome (no ids).
// A non-recording handler is injected via the Registrar seam so this branch
// is reached without bypassing the Manager pipeline.
func TestHookRecord_EmptyGuard_NoRecorderReported(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "abababababababababababababababababababababab"

	empty := func(string) (handlers.GitMeta, error) { return handlers.GitMeta{}, nil }

	// Registrar that registers a non-recording handler instead of the real GitRecorder.
	nonRecorder := &nonRecordingHandler{}
	injectRegistrar := func(mgr *hooks.Manager, _ protocol.TaskTracker, _ *sql.DB) (*hooks.GitRecorder, error) {
		mgr.Register(nonRecorder)
		return nil, nil // no GitRecorder returned; handler is subscribed via Register
	}

	in := handlers.HookRecordInput{
		DBPath:    dbPath,
		Event:     string(handlers.HookEventGitCommit),
		SHA:       sha,
		Gatherer:  empty,
		Registrar: injectRegistrar,
	}

	_, code, err := handlers.HookRecord(in)
	requireStorageError(t, code, err)

	// What + Fix text must be present and actionable.
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("error is not *StructuredError: %v", err)
	}
	if !bytesContains(se.What, "no recorder reported") {
		t.Errorf("What should mention 'no recorder reported'; got:\n%s", se.What)
	}
	if !bytesContains(se.Fix, "wiring bug") {
		t.Errorf("Fix should mention 'wiring bug'; got:\n%s", se.Fix)
	}
}

// bytesContains reports whether s contains substr (avoids importing strings
// twice for one call in the test body).
func bytesContains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

// ─── parseRepoSlug unit tests ─────────────────────────────────────────────────

// TestParseRepoSlug exercises the slug parser for all documented URL forms.
func TestParseRepoSlug(t *testing.T) {
	cases := []struct {
		name      string
		remoteURL string
		want      string
	}{
		// ── SCP / SSH shorthand ─────────────────────────────────────────────
		{
			name:      "SCP with .git suffix",
			remoteURL: "git@github.com:owner/name.git",
			want:      "owner/name",
		},
		{
			name:      "SCP without .git suffix",
			remoteURL: "git@github.com:owner/name",
			want:      "owner/name",
		},
		{
			name:      "SCP nested namespace takes last two components",
			remoteURL: "git@gitlab.com:group/subgroup/name.git",
			want:      "subgroup/name",
		},
		{
			name:      "SCP trailing slash before .git stripped correctly",
			remoteURL: "git@github.com:owner/name.git/",
			want:      "owner/name",
		},
		// ── HTTPS / HTTP ────────────────────────────────────────────────────
		{
			name:      "HTTPS with .git suffix",
			remoteURL: "https://github.com/owner/name.git",
			want:      "owner/name",
		},
		{
			name:      "HTTPS without .git suffix",
			remoteURL: "https://github.com/owner/name",
			want:      "owner/name",
		},
		{
			name:      "HTTP with .git suffix",
			remoteURL: "http://github.com/owner/name.git",
			want:      "owner/name",
		},
		// ── ssh:// and git:// URL forms ─────────────────────────────────────
		{
			name:      "ssh:// URL form with user",
			remoteURL: "ssh://git@github.com/owner/name.git",
			want:      "owner/name",
		},
		{
			name:      "ssh:// URL form with user and port",
			remoteURL: "ssh://git@github.com:22/owner/name.git",
			want:      "owner/name",
		},
		{
			name:      "git:// URL form",
			remoteURL: "git://github.com/owner/name.git",
			want:      "owner/name",
		},
		// ── Unrecognized / local ────────────────────────────────────────────
		{
			name:      "empty URL returns empty",
			remoteURL: "",
			want:      "",
		},
		{
			name:      "local absolute path returns empty",
			remoteURL: "/local/path/repo",
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := handlers.ParseRepoSlug(tc.remoteURL)
			if got != tc.want {
				t.Errorf("ParseRepoSlug(%q) = %q, want %q", tc.remoteURL, got, tc.want)
			}
		})
	}
}

// ─── repo + remotes: override and escape-hatch tests ─────────────────────────

// TestHookRecord_RepoRemotes_FlagOverridesGitDerived asserts that --repo and
// --remote override the git-derived values, even when git IS consulted (a
// commit field is absent).
func TestHookRecord_RepoRemotes_FlagOverridesGitDerived(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a"

	// Fake gatherer returns git-derived repo + remotes that must be overridden.
	fake := func(string) (handlers.GitMeta, error) {
		return handlers.GitMeta{
			Message:   "msg",
			Author:    "A <a@example.com>",
			Branch:    "main",
			Timestamp: "2026-01-01T00:00:00Z",
			Repo:      "git-derived/repo",
			Remotes:   map[string]string{"origin": "git@github.com:git-derived/repo.git"},
		}, nil
	}

	explicitRemotes := map[string]string{"origin": "git@github.com:explicit/override.git"}
	repoFlag := "explicit/override"
	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: fake,
		Repo:     &repoFlag,
		Remotes:  explicitRemotes,
	}

	result, code, err := handlers.HookRecord(in)
	if err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}
	if result.Repo != "explicit/override" {
		t.Errorf("result.Repo = %q, want %q", result.Repo, "explicit/override")
	}
	if result.Remotes["origin"] != "git@github.com:explicit/override.git" {
		t.Errorf("result.Remotes[origin] = %q, want %q", result.Remotes["origin"], "git@github.com:explicit/override.git")
	}

	// Payload must carry the override values.
	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "repo", "explicit/override")
}

// TestHookRecord_RepoRemotes_LandInPayload asserts that repo and remotes
// derived from git (via the fake gatherer) appear in the recorded audit payload.
func TestHookRecord_RepoRemotes_LandInPayload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b"

	fake := func(string) (handlers.GitMeta, error) {
		return handlers.GitMeta{
			Message:   "msg",
			Author:    "A <a@example.com>",
			Branch:    "main",
			Timestamp: "2026-01-01T00:00:00Z",
			Repo:      "dayvidpham/pasture",
			Remotes:   map[string]string{"origin": "git@github.com:dayvidpham/pasture.git"},
		}, nil
	}

	in := handlers.HookRecordInput{
		DBPath:   dbPath,
		Event:    string(handlers.HookEventGitCommit),
		SHA:      sha,
		Gatherer: fake,
	}

	if _, code, err := handlers.HookRecord(in); err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	assertString(t, decoded, "repo", "dayvidpham/pasture")
	// remotes in the payload is stored as a nested object; verify presence.
	if _, ok := decoded["remotes"]; !ok {
		t.Errorf("payload missing 'remotes' key; got %v", decoded)
	}
}

// TestHookRecord_AllFlagsSupplied_RepoRemotesAbsent asserts the escape hatch:
// when all four commit metadata flags are supplied, git is not consulted, so
// repo+remotes are absent from the recorded payload (git never ran).
func TestHookRecord_AllFlagsSupplied_RepoRemotesAbsent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	const sha = "4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c"

	// Gatherer would provide repo+remotes if called — it must NOT be called.
	mustNotBeCalled := func(string) (handlers.GitMeta, error) {
		t.Errorf("gatherer must NOT be called when all metadata flags are supplied")
		return handlers.GitMeta{}, stderrors.New("should not happen")
	}

	in := handlers.HookRecordInput{
		DBPath:    dbPath,
		Event:     string(handlers.HookEventGitCommit),
		SHA:       sha,
		Message:   strptr("explicit msg"),
		Author:    strptr("Explicit <e@example.com>"),
		Branch:    strptr("explicit-branch"),
		Timestamp: strptr("2026-04-04T04:04:04Z"),
		Gatherer:  mustNotBeCalled, // must not be called
	}

	result, code, err := handlers.HookRecord(in)
	if err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}
	// repo+remotes absent because git was never consulted and no override flags given.
	if result.Repo != "" {
		t.Errorf("result.Repo = %q, want empty (git not consulted)", result.Repo)
	}
	if len(result.Remotes) != 0 {
		t.Errorf("result.Remotes = %v, want nil/empty (git not consulted)", result.Remotes)
	}

	decoded := decodeAuditPayload(t, dbPath, protocol.EventType("GitCommit"))
	if _, ok := decoded["repo"]; ok {
		t.Errorf("payload should not contain 'repo' when git was not consulted; got %v", decoded)
	}
	if _, ok := decoded["remotes"]; ok {
		t.Errorf("payload should not contain 'remotes' when git was not consulted; got %v", decoded)
	}
}

// TestHookRecord_RealGit_RepoAndRemotes exercises gatherGitMeta inside the
// actual pasture repo, asserting repo="dayvidpham/pasture" and that remotes
// contains an "origin" whose URL includes "pasture".
func TestHookRecord_RealGit_RepoAndRemotes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping real-git repo+remotes test")
	}

	// Run from the pasture submodule root so gatherGitMeta sees the real origin.
	repoRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not inside a git repo; skipping real-git repo+remotes test")
	}
	t.Chdir(strings.TrimSpace(string(repoRoot)))

	shaOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	sha := strings.TrimSpace(string(shaOut))

	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	in := handlers.HookRecordInput{
		DBPath: dbPath,
		Event:  string(handlers.HookEventGitCommit),
		SHA:    sha,
		// Gatherer nil → real gatherGitMeta (reads origin remote)
	}

	result, code, err := handlers.HookRecord(in)
	if err != nil || code != 0 {
		t.Fatalf("HookRecord: err=%v code=%d", err, code)
	}

	// remotes must contain "origin" with a URL containing "pasture".
	originURL := result.Remotes["origin"]
	if !strings.Contains(originURL, "pasture") {
		t.Errorf("result.Remotes[origin] = %q, want URL containing 'pasture'", originURL)
	}

	// repo must equal the slug parsed from the origin URL, making the
	// origin-parse path load-bearing (a dir-basename fallback would not
	// satisfy this equality).
	wantRepo := handlers.ParseRepoSlug(originURL)
	if result.Repo != wantRepo {
		t.Errorf("result.Repo = %q, want ParseRepoSlug(origin) = %q", result.Repo, wantRepo)
	}
	// Cross-check the expected value so a wrong ParseRepoSlug doesn't mask
	// a wrong result.Repo.
	if wantRepo != "dayvidpham/pasture" {
		t.Errorf("ParseRepoSlug(origin=%q) = %q, want %q (origin URL may have changed)", originURL, wantRepo, "dayvidpham/pasture")
	}
}

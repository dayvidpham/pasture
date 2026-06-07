// Package handlers — hook.go
//
// Handler for `pasture hook record`.
//
// Surface:
//
//	pasture hook record --event git-commit --sha <sha>
//	                    [--message M] [--author A] [--branch B] [--timestamp T]
//	                    [--repo owner/name] [--remote name=url ...]
//
// This is the CLI-direct path that graduates internal/hooks/git_recorder.go
// from a wire-demonstration stub to a production entry point. It records a
// free-floating git-commit audit event WITHOUT requiring the pastured daemon.
//
// ─── Why the Manager path (not a direct tasks.RecordGitEvent call) ────────────
//
// The RATIFIED design (URE Q-seam = "Manager path (unified pipeline)") routes
// the CLI through the SAME hooks.Manager.Dispatch → GitRecorder.Handle →
// tasks.RecordGitEvent pipeline that pastured uses. The handler builds its own
// in-process hooks.Manager and registers the default recorders, so CLI-now and
// daemon-later feed one pipeline. The seam is extensible: new --event values
// map to new hooks.HookEvent variants dispatched through the same Manager.
//
// ─── Metadata gathering (injectable) ──────────────────────────────────────────
//
// When the optional --message/--author/--branch/--timestamp flags are omitted,
// the handler best-effort-derives them from git via an injectable GitMetaGatherer
// (default: gatherGitMeta, which shells `git show -s`). Explicit flags ALWAYS
// override git-derived values; git fills only the absent flags. The gatherer is
// injectable so merge precedence is unit-testable with a fake — no git repo
// required.
//
// ─── Repo + remotes (best-effort, non-trigger) ───────────────────────────────
//
// When git IS consulted (at least one commit field absent), the gatherer also
// derives:
//   - Repo: owner/name slug parsed from the origin remote URL, with a fallback
//     to the repository directory basename if the origin is absent or unparseable.
//   - Remotes: a map of every configured remote name → URL.
//
// These two fields are ALWAYS best-effort: individual absence is not fatal.
// Only a `git show` failure fails hard (unchanged). When git is NOT consulted
// (all four commit flags supplied), repo+remotes come only from --repo/--remote
// override flags (if given), else they are absent from the recorded payload.
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SupportedHookEvent is a CLI-facing event name accepted by
// `pasture hook record --event`. It is the extensible seam: adding a new
// recordable hook event means adding a constant here, a binding in
// hookEventBindings, and (if needed) a recorder subscribed to the mapped
// hooks.HookEvent. The typed enum keeps the supported-set static and
// statically checkable rather than a bare string compared at call sites.
type SupportedHookEvent string

const (
	// HookEventGitCommit records a git commit. Maps to hooks.HookGitCommit and
	// requires --sha. This is the only event supported in this slice; the
	// supported-set (hookEventBindings) is the extensible seam for future
	// events (push, rebase, …).
	HookEventGitCommit SupportedHookEvent = "git-commit"
)

// hookEventBinding ties a CLI event name to the internal hooks.HookEvent the
// in-process Manager dispatches for it.
type hookEventBinding struct {
	cli  SupportedHookEvent
	hook hooks.HookEvent
}

// hookEventBindings is the SINGLE ordered source of truth for the supported
// CLI events: both the supported-list (for help text / errors) and the
// event→hook lookup are derived from it. Adding a new recordable event is a
// one-place edit — append a {cli, hook} pair here.
var hookEventBindings = []hookEventBinding{
	{cli: HookEventGitCommit, hook: hooks.HookGitCommit},
}

// Metadata payload keys. These are the well-known keys carried in the
// HookPayload.Data map (alongside the canonical "sha" key) and persisted into
// the audit_events.payload JSON blob.
const (
	metaMessage   = "message"
	metaAuthor    = "author"
	metaBranch    = "branch"
	metaTimestamp = "timestamp"
	metaRepo      = "repo"
	metaRemotes   = "remotes"
)

// GitMeta holds the metadata derived from a git repository for one commit SHA.
// Commit fields (Message, Author, Branch, Timestamp) are gathered via `git show`;
// repo-context fields (Repo, Remotes) are gathered best-effort after the commit
// lookup succeeds. Individual absent fields are represented as zero values; the
// caller applies flag-over-git merge precedence.
type GitMeta struct {
	// Commit fields — gathered via `git show -s`.
	Message   string
	Author    string
	Branch    string // current HEAD branch; empty on detached HEAD
	Timestamp string

	// Repo-context fields — best-effort, not fail-hard.
	//
	// Repo is the owner/name slug derived from the origin remote URL (SSH or
	// HTTPS form), falling back to the repository directory basename when the
	// origin is absent or unparseable.
	Repo string
	// Remotes maps every configured remote name → URL. Empty when the repo has
	// no configured remotes.
	Remotes map[string]string
}

// GitMetaGatherer derives commit metadata for a sha. A returned non-nil error
// is FATAL to the operation: the handler consults the gatherer only when at
// least one commit metadata flag is absent, and if that attempt fails it
// records nothing and returns an actionable error. Implementations should
// return an error when they cannot resolve the commit (e.g. not in a git repo
// or sha not found); individual absent fields within a successful gather are
// fine (the corresponding flag, if set, wins anyway). Injectable so tests can
// supply a fake — including a failing fake to exercise the fail-hard path —
// without shelling git.
type GitMetaGatherer func(sha string) (GitMeta, error)

// RecorderRegistrar is the function that registers hook handlers onto a
// Manager. It mirrors hooks.RegisterDefaultRecorders so callers can inject an
// alternative registration for testing purposes (e.g. a non-recording handler
// to exercise the empty-guard branch). nil → hooks.RegisterDefaultRecorders.
type RecorderRegistrar func(mgr *hooks.Manager, tracker protocol.TaskTracker, auditDB *sql.DB) (*hooks.GitRecorder, error)

// HookRecordInput captures the CLI inputs for `pasture hook record`.
//
// The optional metadata fields are pointers so the handler can distinguish
// "flag absent" (nil → git may fill it) from "flag set to empty" (non-nil ""
// → explicit override). Gatherer is injectable; nil defaults to gatherGitMeta.
// Registrar is injectable; nil defaults to hooks.RegisterDefaultRecorders.
//
// Repo and Remotes are override flags for the repo-context fields. When Repo
// is non-nil it wins over the git-derived slug. When Remotes is non-nil it
// replaces the gathered remotes map entirely. Both are only consulted when the
// repo-context gather runs (i.e. when at least one commit field is absent and
// git is consulted); when all four commit fields are supplied explicitly, git
// is never consulted and Repo/Remotes come only from these fields if non-nil.
type HookRecordInput struct {
	DBPath string
	Event  string
	SHA    string

	Message   *string
	Author    *string
	Branch    *string
	Timestamp *string

	// Repo overrides the git-derived owner/name slug. nil → git-derived (or
	// absent when git was not consulted).
	Repo *string
	// Remotes overrides the gathered remotes map (name → URL). nil → git-derived
	// (or absent when git was not consulted). A non-nil but empty map explicitly
	// records an empty remotes set.
	Remotes map[string]string

	// Gatherer derives metadata from git when a flag is absent. nil → the real
	// git-backed gatherGitMeta.
	Gatherer GitMetaGatherer

	// Registrar registers hook handlers onto the in-process Manager. nil →
	// hooks.RegisterDefaultRecorders, which subscribes the GitRecorder to
	// HookGitCommit. Inject an alternative to unit-test the post-dispatch guard
	// without standing up a real recorder.
	Registrar RecorderRegistrar
}

// HookRecordResult is the success outcome of HookRecord, handed back to the CLI
// so it (not the handler) decides how to render — text vs JSON — via the global
// --format flag. EventID is the audit_events row id of the recorded event,
// surfaced from the Manager dispatch result. The metadata fields carry the
// actual merged values that were recorded, zero value when absent.
type HookRecordResult struct {
	// EventType is the CLI event name that was recorded (e.g. "git-commit").
	EventType string
	// SHA is the recorded commit SHA.
	SHA string
	// EventID is the audit_events row id of the recorded event.
	EventID int64
	// Message is the commit message that was recorded (empty if absent).
	Message string
	// Author is the commit author that was recorded (empty if absent).
	Author string
	// Branch is the branch name that was recorded (empty if absent).
	Branch string
	// Timestamp is the commit timestamp that was recorded (empty if absent).
	Timestamp string
	// Repo is the owner/name slug that was recorded (empty if absent).
	Repo string
	// Remotes is the map of remote name → URL that was recorded (nil if absent).
	Remotes map[string]string
}

// HookRecord validates the requested hook event, opens the unified task tracker,
// builds an in-process hooks.Manager, and dispatches a HookPayload through the
// Manager path (→ GitRecorder.Handle → tasks.RecordGitEvent). On success it
// returns a HookRecordResult (event type, sha, recorded row id) so the caller
// can render it under the global --format flag. The exit code is derived via
// errors.ExitCode so the caller never hand-rolls a 0/1/5 switch.
func HookRecord(in HookRecordInput) (HookRecordResult, int, error) {
	// 1. Validate --event against the typed supported-set (the extensible seam).
	hookEvent, err := parseSupportedHookEvent(in.Event)
	if err != nil {
		return HookRecordResult{}, errors.ExitCode(err), err
	}

	// 2. Require --sha (git-commit cannot be keyed without it).
	sha := strings.TrimSpace(in.SHA)
	if sha == "" {
		se := &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     "The --sha flag is required to record a git-commit event.",
			Why:      "A git-commit event is keyed by its commit SHA — without it there is nothing to attach the event to or look it up by later. The --sha flag was empty or missing.",
			Where:    "Recording a hook event (internal/handlers/hook.go in handlers.HookRecord).",
			Impact:   "Nothing was recorded; the audit trail is unchanged.",
			Fix: "1. Pass the full commit SHA:\n" +
				"     pasture hook record --event git-commit --sha <commit-sha>\n" +
				"2. To get the SHA of the latest commit: git rev-parse HEAD",
		}
		return HookRecordResult{}, errors.ExitCode(se), se
	}

	// 3. Open the unified tracker + auxiliary audit handle (short-lived CLI;
	//    both closed before return). Empty DBPath → DefaultDBPath (env
	//    PASTURE_DB_PATH). NewGitRecorder requires a non-nil auditDB even
	//    though RecordGitEvent ignores it — pass the real handle (least change).
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return HookRecordResult{}, errors.ExitCode(err), err
	}
	defer tracker.Close()

	auditDB, err := tasks.OpenAuditDBForFreeFloating(in.DBPath)
	if err != nil {
		return HookRecordResult{}, errors.ExitCode(err), err
	}
	defer auditDB.Close()

	// 4. Build an in-process Manager and register the recorders. The injectable
	//    Registrar (default: hooks.RegisterDefaultRecorders) subscribes the
	//    GitRecorder to HookGitCommit. This is the same pipeline pastured wires —
	//    the CLI just constructs it locally.
	registrar := in.Registrar
	if registrar == nil {
		registrar = hooks.RegisterDefaultRecorders
	}
	mgr := hooks.NewManager()
	if _, err := registrar(mgr, tracker, auditDB); err != nil {
		return HookRecordResult{}, errors.ExitCode(err), err
	}

	// 5. Assemble metadata. Explicit flags override git-derived values. Git is
	//    consulted ONLY when at least one commit metadata field is absent. If
	//    that gather is attempted and fails (not in a git repo / sha not found /
	//    git error), we FAIL HARD and record nothing — recording an event with
	//    missing or wrong metadata is worse than refusing. When all four commit
	//    fields are supplied explicitly, git is never consulted, so there is no
	//    failure path for the commit fields. Repo + remotes are gathered
	//    best-effort alongside the commit fields when git is consulted; their
	//    individual absence is not fail-hard.
	var gitMeta GitMeta
	if in.Message == nil || in.Author == nil || in.Branch == nil || in.Timestamp == nil {
		gather := in.Gatherer
		if gather == nil {
			gather = gatherGitMeta
		}
		m, gErr := gather(sha)
		if gErr != nil {
			se := &errors.StructuredError{
				Category: errors.CategoryValidation,
				What:     "Couldn't read git metadata for the commit being recorded.",
				Why: fmt.Sprintf(
					"One or more metadata flags were omitted, so pasture tried to read them\n"+
						"from git for sha %q — but the git lookup failed. This usually means the\n"+
						"command wasn't run inside the commit's git repository, or the SHA\n"+
						"doesn't exist there.", sha),
				Where:  "Recording a hook event (internal/handlers/hook.go in handlers.HookRecord, git-metadata gather step).",
				Impact: "Nothing was recorded — to avoid writing an event with missing or wrong\nmetadata, pasture stops instead of guessing.",
				Fix: "1. Run pasture from inside the commit's git repository so it can read the metadata:\n" +
					"     cd <path-to-repo>\n" +
					"     pasture hook record --event git-commit --sha <commit-sha>\n" +
					"   Example:\n" +
					"     cd ~/dev/myproject\n" +
					"     pasture hook record --event git-commit --sha " + sha + "\n" +
					"2. Or supply every metadata field explicitly so git is never consulted:\n" +
					"     pasture hook record --event git-commit --sha <commit-sha> \\\n" +
					"       --message \"<commit message>\" \\\n" +
					"       --author \"<name> <email>\" \\\n" +
					"       --branch \"<branch>\" \\\n" +
					"       --timestamp \"<ISO-8601 timestamp>\"\n" +
					"   Example:\n" +
					"     pasture hook record --event git-commit --sha " + sha + " \\\n" +
					"       --message \"fix: handle nil config\" \\\n" +
					"       --author \"Jane Dev <jane@example.com>\" \\\n" +
					"       --branch main \\\n" +
					"       --timestamp 2026-06-07T12:00:00Z",
				Cause: gErr,
			}
			return HookRecordResult{}, errors.ExitCode(se), se
		}
		gitMeta = m
	}

	data := map[string]any{hooks.GitCommitDataKey: sha}
	mergeMetaString(data, metaMessage, in.Message, gitMeta.Message)
	mergeMetaString(data, metaAuthor, in.Author, gitMeta.Author)
	mergeMetaString(data, metaBranch, in.Branch, gitMeta.Branch)
	mergeMetaString(data, metaTimestamp, in.Timestamp, gitMeta.Timestamp)

	// Repo: flag wins; fall back to git-derived; omit if absent.
	var resolvedRepo string
	if in.Repo != nil {
		resolvedRepo = *in.Repo
	} else {
		resolvedRepo = gitMeta.Repo
	}
	if resolvedRepo != "" {
		data[metaRepo] = resolvedRepo
	}

	// Remotes: flag wins (replaces gathered map when non-nil); fall back to
	// git-derived; omit if nil/empty.
	var resolvedRemotes map[string]string
	if in.Remotes != nil {
		resolvedRemotes = in.Remotes
	} else {
		resolvedRemotes = gitMeta.Remotes
	}
	if len(resolvedRemotes) > 0 {
		data[metaRemotes] = resolvedRemotes
	}

	// 6. Dispatch through the Manager path.
	payload := hooks.HookPayload{
		Event: hookEvent,
		Data:  data,
	}
	res, err := mgr.Dispatch(context.Background(), payload)
	if err != nil {
		// The Manager returns a *dispatchErrors aggregate (it has Unwrap()
		// []error); errors.ExitCode uses errors.As to reach the underlying
		// *StructuredError Category, so storage failures map to exit 5 and
		// validation failures to exit 1 without a hand-rolled switch.
		return HookRecordResult{}, errors.ExitCode(err), err
	}

	// 7. Read the recorded row id back from this dispatch. Exactly one handler
	//    (the GitRecorder) is registered for git-commit and it records exactly
	//    one event, so res.RecordedEventIDs[0] is that id. Guard defensively in
	//    case a future wiring change leaves no recorder subscribed.
	if len(res.RecordedEventIDs) == 0 {
		se := &errors.StructuredError{
			Category: errors.CategoryStorage,
			What:     "The git-commit event was dispatched but no recorder reported saving it.",
			Why: "The in-process hook Manager dispatched the event but returned no\n" +
				"recorded-event ids. This means no handler was subscribed to the\n" +
				"git-commit hook event when the dispatch ran, or the subscribed handler\n" +
				"returned a zero outcome — a wiring bug, not a user input error.",
			Where:  "Recording a hook event (internal/handlers/hook.go in handlers.HookRecord, post-dispatch step).",
			Impact: "It is not possible to confirm the event reached the audit trail; treat the record as not durably written.",
			Fix: "1. This indicates the default recorders were not registered correctly.\n" +
				"2. If you hit this from production code, this is a wiring bug — please\n" +
				"   file a bug.",
		}
		return HookRecordResult{}, errors.ExitCode(se), se
	}

	// 8. Populate the result from the merged data map (flag-over-git values
	//    actually dispatched). String values are cast directly; absent keys
	//    yield zero values which callers render with omitempty.
	strVal := func(key string) string {
		v, _ := data[key].(string)
		return v
	}
	mapVal := func(key string) map[string]string {
		v, _ := data[key].(map[string]string)
		return v
	}
	return HookRecordResult{
		EventType: strings.TrimSpace(in.Event),
		SHA:       sha,
		EventID:   res.RecordedEventIDs[0],
		Message:   strVal(metaMessage),
		Author:    strVal(metaAuthor),
		Branch:    strVal(metaBranch),
		Timestamp: strVal(metaTimestamp),
		Repo:      strVal(metaRepo),
		Remotes:   mapVal(metaRemotes),
	}, 0, nil
}

// mergeMetaString applies flag-over-git precedence for one string metadata key.
// When the flag pointer is non-nil it wins (even if empty — an explicit
// override). When it is nil, the git-derived value fills in if non-empty.
func mergeMetaString(data map[string]any, key string, flag *string, gitVal string) {
	if flag != nil {
		data[key] = *flag
		return
	}
	if gitVal != "" {
		data[key] = gitVal
	}
}

// parseSupportedHookEvent resolves a CLI --event value to the internal
// hooks.HookEvent the Manager dispatches, returning an actionable validation
// error (listing the supported events) when the value is unknown or empty.
func parseSupportedHookEvent(raw string) (hooks.HookEvent, error) {
	cliEvent := SupportedHookEvent(strings.TrimSpace(raw))
	for _, b := range hookEventBindings {
		if b.cli == cliEvent {
			return b.hook, nil
		}
	}
	return "", &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     fmt.Sprintf("%q isn't a supported value for --event.", raw),
		Why:      "The --event flag must name one of the hook events pasture knows how to record. The value given didn't match any of them.",
		Where:    "Recording a hook event (internal/handlers/hook.go in handlers.HookRecord).",
		Impact:   "Nothing was recorded — pasture can't dispatch an event type it doesn't recognise.",
		Fix: "1. Pass one of the supported events (case-sensitive):\n" +
			"     " + listSupportedHookEvents() + "\n" +
			"   For example:\n" +
			"     pasture hook record --event git-commit --sha <commit-sha>",
	}
}

// listSupportedHookEvents renders the comma-separated supported CLI event names
// for help text and error messages. Derived from hookEventBindings so a new
// pair appears here automatically.
func listSupportedHookEvents() string {
	parts := make([]string, len(hookEventBindings))
	for i, b := range hookEventBindings {
		parts[i] = string(b.cli)
	}
	return strings.Join(parts, ", ")
}

// gatherGitMeta is the default GitMetaGatherer: it derives commit metadata by
// shelling `git` in the current working directory. It FAILS HARD — when the
// `git show` lookup for `sha` fails, it returns an actionable error and the
// handler records nothing. This is only ever called when at least one commit
// metadata flag was omitted; supply all four flags to skip git entirely.
//
// CWD-relative: the git commands run in the PROCESS working directory. If the
// CLI is invoked from outside a git repo, or from a repo that doesn't know
// `sha`, `git show` fails and this returns an error (the handler then fails
// hard rather than recording partial data). To record metadata for a commit in
// another repo, run from inside that repo or pass the metadata explicitly.
//
// Branch semantics: "branch" is the CURRENT HEAD's branch
// (`git rev-parse --abbrev-ref HEAD`), not the branch the sha belongs to.
// git has no single answer for "the branch of a commit" (a commit may be on
// many branches or none), so this records "the branch checked out when the
// event was recorded". Pass --branch explicitly to override. Branch resolution
// is best-effort: once `git show` confirms a valid repo + sha, a
// missing/detached branch is omitted rather than treated as fatal.
//
// Repo and Remotes are also best-effort: gathered after the commit lookup
// succeeds, but their individual failure is not fatal.
func gatherGitMeta(sha string) (GitMeta, error) {
	var meta GitMeta

	// One call yields subject, "author <email>", and committer ISO-8601 date,
	// separated by an ASCII unit-separator (0x1f) that can't appear in any field.
	const sep = "\x1f"
	format := "--format=%s" + sep + "%an <%ae>" + sep + "%cI"
	showCmd := exec.Command("git", "show", "-s", format, sha)
	var stderr bytes.Buffer
	showCmd.Stderr = &stderr
	out, err := showCmd.Output()
	if err != nil {
		return GitMeta{}, fmt.Errorf("`git show -s %s` failed in %s: %w — %s",
			sha, cwdForError(), err, strings.TrimSpace(stderr.String()))
	}

	fields := strings.SplitN(strings.TrimRight(string(out), "\n"), sep, 3)
	if len(fields) == 3 {
		meta.Message = fields[0]
		meta.Author = fields[1]
		meta.Timestamp = fields[2]
	}

	// Branch is the current HEAD's branch name (best-effort; "HEAD" = detached
	// checkout, which we skip rather than record).
	if branchOut, bErr := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); bErr == nil {
		if branch := strings.TrimSpace(string(branchOut)); branch != "" && branch != "HEAD" {
			meta.Branch = branch
		}
	}

	// Repo: parse origin remote URL → owner/name slug; fall back to repo
	// directory basename. Best-effort: failure leaves Repo empty.
	if originURL, oErr := exec.Command("git", "remote", "get-url", "origin").Output(); oErr == nil {
		meta.Repo = ParseRepoSlug(strings.TrimSpace(string(originURL)))
	}
	if meta.Repo == "" {
		// Fallback: use the repository's top-level directory basename.
		if toplevel, tErr := exec.Command("git", "rev-parse", "--show-toplevel").Output(); tErr == nil {
			meta.Repo = filepath.Base(strings.TrimSpace(string(toplevel)))
		}
	}

	// Remotes: gather all configured remotes (name → URL). Best-effort.
	if namesOut, nErr := exec.Command("git", "remote").Output(); nErr == nil {
		names := strings.Fields(strings.TrimSpace(string(namesOut)))
		if len(names) > 0 {
			remotes := make(map[string]string, len(names))
			for _, name := range names {
				if urlOut, uErr := exec.Command("git", "remote", "get-url", name).Output(); uErr == nil {
					remotes[name] = strings.TrimSpace(string(urlOut))
				}
			}
			if len(remotes) > 0 {
				meta.Remotes = remotes
			}
		}
	}

	return meta, nil
}

// ParseRepoSlug extracts an owner/name slug from a git remote URL. It handles
// both SSH form (git@host:owner/name.git) and HTTPS form
// (https://host/owner/name.git), stripping a trailing ".git" suffix. When the
// URL cannot be parsed into an owner/name pair, it returns an empty string and
// the caller can fall back to the repository directory basename.
// Exported for unit testing.
func ParseRepoSlug(remoteURL string) string {
	if remoteURL == "" {
		return ""
	}
	var path string
	if strings.HasPrefix(remoteURL, "https://") || strings.HasPrefix(remoteURL, "http://") {
		// HTTPS: https://host/owner/name(.git)
		// Strip scheme + host to get the path component.
		rest := strings.SplitN(remoteURL, "/", 4)
		// rest[0]="https:", rest[1]="", rest[2]=host, rest[3]=owner/name(.git)
		if len(rest) < 4 {
			return ""
		}
		path = rest[3]
	} else if strings.Contains(remoteURL, "@") && strings.Contains(remoteURL, ":") {
		// SSH: git@host:owner/name(.git)
		colonIdx := strings.Index(remoteURL, ":")
		path = remoteURL[colonIdx+1:]
	} else {
		return ""
	}

	// Strip trailing ".git" (case-sensitive, as git itself treats it).
	path = strings.TrimSuffix(path, ".git")

	// Validate: must be exactly owner/name (two components).
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// cwdForError returns the process working directory for inclusion in the
// gather-failure error (so the user can see WHERE git was run). Falls back to a
// placeholder if the cwd can't be determined — never fatal.
func cwdForError() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "the current directory"
}

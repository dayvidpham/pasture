// Package handlers — hook.go
//
// Handler for `pasture hook record`.
//
// Surface:
//
//	pasture hook record --event git-commit --sha <sha>
//	                    [--message M] [--author A] [--branch B] [--timestamp T]
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
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
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
)

// GitMetaGatherer derives commit metadata for a sha. Returns a map keyed by the
// meta* constants above. A returned non-nil error is FATAL to the operation: the
// handler consults the gatherer only when at least one metadata flag is absent,
// and if that attempt fails it records nothing and returns an actionable error
// (C4). Implementations should therefore return an error when they cannot
// resolve metadata for the sha (e.g. not in a git repo / sha not found); an
// individual missing key within a successful gather is fine (the corresponding
// flag, if set, wins anyway). Injectable so tests can supply a fake — including
// a failing fake to exercise the fail-hard path — without shelling git.
type GitMetaGatherer func(sha string) (map[string]string, error)

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
type HookRecordInput struct {
	DBPath string
	Event  string
	SHA    string

	Message   *string
	Author    *string
	Branch    *string
	Timestamp *string

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
// surfaced from the Manager dispatch result.
type HookRecordResult struct {
	// EventType is the CLI event name that was recorded (e.g. "git-commit").
	EventType string
	// SHA is the recorded commit SHA.
	SHA string
	// EventID is the audit_events row id of the recorded event.
	EventID int64
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
	//    consulted ONLY when at least one metadata field is absent. If that
	//    gather is attempted and fails (not in a git repo / sha not found / git
	//    error), we FAIL HARD and record nothing (C4) — recording an event with
	//    missing or wrong metadata is worse than refusing. When all four fields
	//    are supplied explicitly, git is never consulted, so there is no failure
	//    path.
	var gitMeta map[string]string
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
				Fix: "1. Run inside the commit's git repo:\n" +
					"     cd <repo> && pasture hook record --event git-commit --sha " + sha + "\n" +
					"2. Or pass every metadata field explicitly so git isn't consulted:\n" +
					"     pasture hook record --event git-commit --sha " + sha + " \\\n" +
					"       --message <m> --author <a> --branch <b> --timestamp <t>",
				Cause: gErr,
			}
			return HookRecordResult{}, errors.ExitCode(se), se
		}
		gitMeta = m
	}

	data := map[string]any{hooks.GitCommitDataKey: sha}
	mergeMeta(data, metaMessage, in.Message, gitMeta)
	mergeMeta(data, metaAuthor, in.Author, gitMeta)
	mergeMeta(data, metaBranch, in.Branch, gitMeta)
	mergeMeta(data, metaTimestamp, in.Timestamp, gitMeta)

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

	return HookRecordResult{
		EventType: strings.TrimSpace(in.Event),
		SHA:       sha,
		EventID:   res.RecordedEventIDs[0],
	}, 0, nil
}

// mergeMeta applies the flag-over-git precedence for one metadata key. When the
// flag pointer is non-nil it wins (even if empty — an explicit override). When
// it is nil, the git-derived value fills in if present and non-empty.
func mergeMeta(data map[string]any, key string, flag *string, gitMeta map[string]string) {
	if flag != nil {
		data[key] = *flag
		return
	}
	if v, ok := gitMeta[key]; ok && v != "" {
		data[key] = v
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
// handler records nothing (C4). This is only ever called when at least one
// metadata flag was omitted; supply all four flags to skip git entirely.
//
// CWD-relative (A2): the git commands run in the PROCESS working directory, not
// in the repository that actually contains `sha`. If the CLI is invoked from
// outside a git repo, or from a repo that doesn't know `sha`, `git show` fails
// and this returns an error (the handler then fails hard rather than recording
// partial data). To record metadata for a commit in another repo, run from
// inside that repo or pass the metadata explicitly via flags.
//
// Branch semantics (A3): the "branch" key is the CURRENT HEAD's branch
// (`git rev-parse --abbrev-ref HEAD`), NOT the branch that `sha` belongs to.
// git has no single answer for "the branch of a commit" (a commit can be on
// many branches or none), so this records "the branch checked out when the
// commit was recorded". Pass --branch explicitly to override. Branch resolution
// is the one best-effort step: once `git show` has confirmed a valid repo + sha,
// a missing/detached branch is omitted rather than treated as fatal.
func gatherGitMeta(sha string) (map[string]string, error) {
	meta := make(map[string]string)

	// One call yields subject, "author <email>", and committer ISO-8601 date,
	// separated by an ASCII unit-separator (0x1f) that can't appear in any field.
	const sep = "\x1f"
	format := "--format=%s" + sep + "%an <%ae>" + sep + "%cI"
	showCmd := exec.Command("git", "show", "-s", format, sha)
	var stderr bytes.Buffer
	showCmd.Stderr = &stderr
	out, err := showCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("`git show -s %s` failed in %s: %w — %s",
			sha, cwdForError(), err, strings.TrimSpace(stderr.String()))
	}

	fields := strings.SplitN(strings.TrimRight(string(out), "\n"), sep, 3)
	if len(fields) == 3 {
		if fields[0] != "" {
			meta[metaMessage] = fields[0]
		}
		if fields[1] != "" {
			meta[metaAuthor] = fields[1]
		}
		if fields[2] != "" {
			meta[metaTimestamp] = fields[2]
		}
	}

	// Branch is the current HEAD's branch name (best-effort within a confirmed
	// repo; "HEAD" means a detached checkout, which we skip rather than record).
	if out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" && branch != "HEAD" {
			meta[metaBranch] = branch
		}
	}

	return meta, nil
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

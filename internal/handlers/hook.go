// Package handlers — hook.go
//
// Handler for `pasture hook record` (PROPOSAL-1, aura-plugins-3lzsc).
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
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
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
	// supported-set is the extensible seam for future events (push, rebase, …).
	HookEventGitCommit SupportedHookEvent = "git-commit"
)

// supportedHookEvents is the ordered set of CLI event names, used to render the
// actionable "supported events" list in validation errors and help text. A new
// SupportedHookEvent constant must be appended here AND bound in
// hookEventBindings below.
var supportedHookEvents = []SupportedHookEvent{
	HookEventGitCommit,
}

// hookEventBindings maps each CLI event name to the internal hooks.HookEvent
// the in-process Manager dispatches. Single source of truth for the
// CLI-event → hook-event correspondence.
var hookEventBindings = map[SupportedHookEvent]hooks.HookEvent{
	HookEventGitCommit: hooks.HookGitCommit,
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
// meta* constants above. Implementations MUST be best-effort: a missing key is
// fine (the corresponding flag, if set, wins anyway), and the error is advisory
// — the handler proceeds with whatever keys are present. Injectable so tests can
// supply a fake without shelling git.
type GitMetaGatherer func(sha string) (map[string]string, error)

// HookRecordInput captures the CLI inputs for `pasture hook record`.
//
// The optional metadata fields are pointers so the handler can distinguish
// "flag absent" (nil → git may fill it) from "flag set to empty" (non-nil ""
// → explicit override). Gatherer is injectable; nil defaults to gatherGitMeta.
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
}

// HookRecord validates the requested hook event, opens the unified task tracker,
// builds an in-process hooks.Manager, and dispatches a HookPayload through the
// Manager path (→ GitRecorder.Handle → tasks.RecordGitEvent). Returns the
// standard (exitCode, error) tuple; exitCode is derived via errors.ExitCode so
// the caller never hand-rolls a 0/1/5 switch.
func HookRecord(w io.Writer, in HookRecordInput) (int, error) {
	// 1. Validate --event against the typed supported-set (the extensible seam).
	cliEvent, hookEvent, err := parseSupportedHookEvent(in.Event)
	if err != nil {
		return errors.ExitCode(err), err
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
		return errors.ExitCode(se), se
	}

	// 3. Open the unified tracker + auxiliary audit handle (short-lived CLI;
	//    both closed before return). Empty DBPath → DefaultDBPath (env
	//    PASTURE_DB_PATH). NewGitRecorder requires a non-nil auditDB even
	//    though RecordGitEvent ignores it — pass the real handle (least change).
	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return errors.ExitCode(err), err
	}
	defer tracker.Close()

	auditDB, err := tasks.OpenAuditDBForFreeFloating(in.DBPath)
	if err != nil {
		return errors.ExitCode(err), err
	}
	defer auditDB.Close()

	// 4. Build an in-process Manager and register the default recorders
	//    (GitRecorder subscribed to HookGitCommit). This is the same pipeline
	//    pastured wires — the CLI just constructs it locally.
	mgr := hooks.NewManager()
	if _, err := hooks.RegisterDefaultRecorders(mgr, tracker, auditDB); err != nil {
		return errors.ExitCode(err), err
	}

	// 5. Assemble metadata. Explicit flags override git-derived values; git
	//    fills only the absent flags (best-effort — gather errors are advisory).
	gather := in.Gatherer
	if gather == nil {
		gather = gatherGitMeta
	}
	gitMeta, _ := gather(sha) // best-effort: ignore error, fall back to flags

	data := map[string]any{"sha": sha}
	mergeMeta(data, metaMessage, in.Message, gitMeta)
	mergeMeta(data, metaAuthor, in.Author, gitMeta)
	mergeMeta(data, metaBranch, in.Branch, gitMeta)
	mergeMeta(data, metaTimestamp, in.Timestamp, gitMeta)

	// 6. Dispatch through the Manager path.
	payload := hooks.HookPayload{
		Event: hookEvent,
		Data:  data,
	}
	if err := mgr.Dispatch(context.Background(), payload); err != nil {
		// The Manager returns a *dispatchErrors aggregate (it has Unwrap()
		// []error); errors.ExitCode uses errors.As to reach the underlying
		// *StructuredError Category, so storage failures map to exit 5 and
		// validation failures to exit 1 without a hand-rolled switch.
		return errors.ExitCode(err), err
	}

	fmt.Fprintf(w, "recorded %s event for sha %s\n", cliEvent, sha)
	return 0, nil
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

// parseSupportedHookEvent resolves a CLI --event value to its internal
// hooks.HookEvent, returning an actionable validation error (listing the
// supported events) when the value is unknown or empty.
func parseSupportedHookEvent(raw string) (SupportedHookEvent, hooks.HookEvent, error) {
	cliEvent := SupportedHookEvent(strings.TrimSpace(raw))
	hookEvent, ok := hookEventBindings[cliEvent]
	if !ok {
		se := &errors.StructuredError{
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
		return "", "", se
	}
	return cliEvent, hookEvent, nil
}

// listSupportedHookEvents renders the comma-separated supported CLI event names
// for help text and error messages. Centralised so a new SupportedHookEvent
// constant appears here automatically.
func listSupportedHookEvents() string {
	parts := make([]string, len(supportedHookEvents))
	for i, e := range supportedHookEvents {
		parts[i] = string(e)
	}
	return strings.Join(parts, ", ")
}

// gatherGitMeta is the default GitMetaGatherer: it best-effort-derives commit
// metadata by shelling `git` in the current working directory. It NEVER returns
// an error — any git failure simply yields fewer (or no) keys, and the handler
// falls back to whatever metadata flags were supplied. This keeps the handler
// logic testable without a git repo (tests inject a fake gatherer) while the
// real path Just Works when run inside a checkout.
func gatherGitMeta(sha string) (map[string]string, error) {
	meta := make(map[string]string)

	// One call yields subject, "author <email>", and committer ISO-8601 date,
	// separated by an ASCII unit-separator (0x1f) that can't appear in any field.
	const sep = "\x1f"
	format := "--format=%s" + sep + "%an <%ae>" + sep + "%cI"
	if out, err := exec.Command("git", "show", "-s", format, sha).Output(); err == nil {
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
	}

	// Branch is the current HEAD's branch name (best-effort; "HEAD" means a
	// detached checkout, which we skip rather than record).
	if out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" && branch != "HEAD" {
			meta[metaBranch] = branch
		}
	}

	return meta, nil
}

// Package hooks — git_recorder.go
//
// GitRecorder is the in-tree HookHandler that records free-floating git events
// to the unified pasture.db audit trail (PROPOSAL-2 §7.5 ContextGit, §11
// Scenario 6, §8 S9). It is the wire that bridges:
//
//   - the pasture-internal hooks.Manager dispatch surface (this package), and
//   - the protocol.TaskTracker.RecordEvent / AttachContext writes performed by
//     internal/tasks/free_floating.go.
//
// ─── Why a HookHandler at all ────────────────────────────────────────────────
//
// PROPOSAL-2 §8 S9 specifies "Hook handlers and skill-invocation entry points
// record events with non-epoch contexts via protocol.TaskTracker.AttachContext"
// and gives a concrete example: "a Claude Code `Stop` hook fired after `git
// agent-commit` calls tracker.RecordEvent({event_type: 'GitCommit', payload:
// {sha: 'abc123'}})". The Claude Code hook events (SessionStart, Stop, ...) do
// NOT live in the pasture hooks.HookEvent enum — that enum (per Pasture URD
// D7) covers pasture's INTERNAL lifecycle events (phase_transition,
// slice_completed, etc.). S7 owns the Claude Code hook → pasture mapping.
//
// For S9 the GitRecorder is a "stub hook handler that demonstrates the wiring"
// (slice-body verbatim). It exposes two paths:
//
//  1. The HookHandler interface (Events / Handle), so the hooks.Manager can
//     dispatch to it when an upstream Claude Code → pasture mapping (S7+)
//     fires a HookPayload with Data["sha"] populated. The handler subscribes
//     to HookSliceCompleted (the closest existing internal event that could
//     plausibly carry a commit SHA — slice completion frequently follows a
//     commit). Subscribing here is purely defensive: in the wire-test we
//     verify dispatch end-to-end against this surface, but production
//     callers that want to record a git commit directly should prefer
//
//  2. RecordCommit(ctx, sha, payload) — the direct method that bypasses the
//     hooks.Manager and writes straight to the audit trail. This is what
//     `cmd/pastured` will call when a Claude Code Stop hook arrives via
//     whatever mechanism S7+ delivers it (Temporal signal, IPC, etc.).
//
// Both paths funnel through tasks.RecordGitEvent so the storage contract is
// identical. RecordCommit returns the int64 event id so callers can attach
// further contexts (Scenario 7 multi-context attachment).

package hooks

import (
	"context"
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// GitCommitDataKey is the conventional key in HookPayload.Data that carries
// the git commit SHA. The GitRecorder hook handler reads this key when a hook
// payload is dispatched to it; payloads missing or with a non-string value
// are skipped (the hook may be unrelated to git).
const GitCommitDataKey = "sha"

// GitRecorder is a HookHandler that records git commit events to the unified
// pasture.db audit trail.
//
// Construct via NewGitRecorder; the zero value is unusable (the embedded
// TaskTracker / *sql.DB would be nil).
//
// Concurrency: GitRecorder is safe for concurrent calls to Handle and
// RecordCommit because both operations delegate to tasks.RecordGitEvent,
// which is itself safe for concurrent use (the underlying *sql.DB and
// TaskTracker are both goroutine-safe).
type GitRecorder struct {
	tracker protocol.TaskTracker
	auditDB *sql.DB
	// subscribed is the set of pasture-internal HookEvents this recorder
	// listens to. We default to HookSliceCompleted (the closest existing
	// internal event that could plausibly carry a commit SHA in Data["sha"]).
	// Tests / future S7 wiring may pass an explicit set via WithSubscribedEvents.
	subscribed []HookEvent
}

// GitRecorderOption is a functional option for NewGitRecorder.
type GitRecorderOption func(*GitRecorder)

// WithSubscribedEvents replaces the default subscription set. Used by S7+ when
// the Claude Code hook → pasture mapping introduces a new HookEvent (e.g.
// HookGitCommit) — until that lands, callers stick with the default.
func WithSubscribedEvents(events ...HookEvent) GitRecorderOption {
	return func(g *GitRecorder) {
		g.subscribed = append([]HookEvent(nil), events...)
	}
}

// NewGitRecorder constructs a GitRecorder bound to the supplied TaskTracker
// and auxiliary auditDB handle (the same handle returned by
// tasks.OpenAuditDBForFreeFloating). Both arguments MUST be non-nil; the
// constructor returns an actionable *pasterrors.StructuredError otherwise so
// the caller catches the wiring mistake at startup, not at first dispatch.
//
// The auditDB handle is used only for the post-write SELECT MAX(id) lookup
// inside tasks.RecordGitEvent — see free_floating.go for the rationale.
//
// By default, the recorder subscribes to HookSliceCompleted (see the package
// doc comment); pass WithSubscribedEvents to override.
func NewGitRecorder(
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
	opts ...GitRecorderOption,
) (*GitRecorder, error) {
	if tracker == nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A git-event recorder can't be created without a task tracker.",
			Why: "The recorder needs a task tracker so it can save commit events to\n" +
				"the database. The tracker argument was nil.",
			Where: "Setting up the git recorder (internal/hooks/git_recorder.go in hooks.NewGitRecorder).",
			Impact: "The recorder wasn't created. Without it, git commit events from\n" +
				"hooks would be silently dropped instead of saved to the audit log.",
			Fix: "1. Open a task tracker against the database file first.\n" +
				"2. Pass the returned tracker to the recorder constructor.",
		}
	}
	if auditDB == nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A git-event recorder can't be created without an auxiliary database handle.",
			Why: "The recorder needs a second database handle to look up the row ID\n" +
				"of the event it just wrote. The auditDB argument was nil.",
			Where: "Setting up the git recorder (internal/hooks/git_recorder.go in hooks.NewGitRecorder).",
			Impact: "The recorder wasn't created. Without it, git commit events from\n" +
				"hooks would be silently dropped instead of saved to the audit log.",
			Fix: "1. Open the auxiliary database handle for the same file first.\n" +
				"2. Pass the returned handle to the recorder constructor alongside the tracker.",
		}
	}

	g := &GitRecorder{
		tracker:    tracker,
		auditDB:    auditDB,
		subscribed: []HookEvent{HookSliceCompleted},
	}
	for _, opt := range opts {
		opt(g)
	}
	return g, nil
}

// Events returns the set of HookEvent values this recorder subscribes to.
// Implements the HookHandler interface.
func (g *GitRecorder) Events() []HookEvent {
	out := make([]HookEvent, len(g.subscribed))
	copy(out, g.subscribed)
	return out
}

// Handle is called by hooks.Manager when one of the subscribed HookEvents
// fires. The recorder looks for a string SHA at payload.Data[GitCommitDataKey];
// if present and non-empty, it records a free-floating git commit event via
// tasks.RecordGitEvent. If the key is absent / not a string / empty, Handle
// returns nil (the hook fired for a non-git reason, which is normal).
//
// Implements the HookHandler interface.
func (g *GitRecorder) Handle(ctx context.Context, payload HookPayload) error {
	sha, ok := extractSHA(payload.Data)
	if !ok {
		// Not a git-bearing payload; nothing to do. Returning nil here is
		// intentional — the hook fired for a different reason and
		// returning an error would surface in dispatchErrors and
		// confuse operators.
		return nil
	}

	// Build the audit-event payload from the hook data so the recorded row
	// preserves all context (epoch id, phase, sha) for later forensic
	// queries. We add "sha" / "hookEvent" keys explicitly even if Data has
	// them, so the persisted payload is self-describing.
	auditPayload := map[string]any{
		"sha":       sha,
		"hookEvent": string(payload.Event),
	}
	if payload.EpochID != "" {
		auditPayload["epochId"] = payload.EpochID
	}
	if payload.Phase != "" {
		auditPayload["phase"] = string(payload.Phase)
	}
	for k, v := range payload.Data {
		if k == GitCommitDataKey {
			continue // already added under the canonical "sha" key
		}
		auditPayload[k] = v
	}

	_, err := g.RecordCommit(ctx, sha, auditPayload)
	if err != nil {
		// Wrap so dispatchErrors shows the hook origin.
		return fmt.Errorf("hooks.GitRecorder.Handle: failed to record git commit (sha=%q event=%q): %w", sha, payload.Event, err)
	}
	return nil
}

// RecordCommit records a free-floating git commit event directly, bypassing
// the hooks.Manager dispatch path. This is the entry point production wiring
// (cmd/pastured + the Claude Code Stop-hook bridge S7 will introduce) calls
// when a commit completes.
//
// sha MUST be the non-empty git commit SHA (becomes the ContextGit edge's
// context_id). payload SHOULD include "sha" for symmetry; it is NOT enforced.
//
// Returns the int64 audit_events.id so callers can attach further contexts
// (Scenario 7 multi-context attachment — e.g., a post-epoch commit citing
// epoch X also gets ContextEpoch).
//
// Errors are *pasterrors.StructuredError; see tasks.RecordGitEvent for the
// full error contract.
func (g *GitRecorder) RecordCommit(
	ctx context.Context,
	sha string,
	payload map[string]any,
) (int64, error) {
	return tasks.RecordGitEvent(
		ctx,
		g.tracker,
		g.auditDB,
		sha,
		tasks.EventGitCommit,
		payload,
	)
}

// extractSHA reads the canonical commit-SHA key out of a HookPayload.Data
// map, returning (sha, true) only when the value is a non-empty string. All
// other shapes (missing key, wrong type, empty string) return ("", false) so
// Handle can no-op without raising an error on hooks unrelated to git.
func extractSHA(data map[string]any) (string, bool) {
	if data == nil {
		return "", false
	}
	raw, ok := data[GitCommitDataKey]
	if !ok {
		return "", false
	}
	sha, ok := raw.(string)
	if !ok || sha == "" {
		return "", false
	}
	return sha, true
}

// Compile-time check that *GitRecorder satisfies HookHandler.
var _ HookHandler = (*GitRecorder)(nil)

// ─── Registration helper for cmd/pastured wiring ─────────────────────────────

// RegisterDefaultRecorders registers the in-tree free-floating event recorders
// (currently: GitRecorder) with the supplied hooks.Manager. Returns the
// constructed GitRecorder so the caller can also call its RecordCommit method
// directly (e.g., from a Claude Code Stop-hook bridge S7+ will introduce).
//
// This helper exists so cmd/pastured/main.go can wire the recorder with a
// single line of glue, keeping the daemon's main.go change minimal — the goal
// is to avoid stepping on S7's parallel modifications to the same file (per
// SLICE-9 coordination plan with aura-plugins-9ye50).
//
// Both `tracker` and `auditDB` MUST be non-nil; this function returns the
// underlying *pasterrors.StructuredError surface from NewGitRecorder when
// either is nil so the caller's startup fails fast with an actionable error.
//
// When the audit backend is the in-memory implementation (no SQLite file),
// callers should NOT invoke this helper — there is no auxiliary *sql.DB to
// pass, and free-floating event recording requires a durable store.
func RegisterDefaultRecorders(
	mgr *Manager,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
) (*GitRecorder, error) {
	if mgr == nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Default hook recorders can't be registered without a hook manager.",
			Why: "The registrar needs a hook manager so it can attach the recorders to\n" +
				"it. The mgr argument was nil.",
			Where: "Registering default recorders (internal/hooks/git_recorder.go in hooks.RegisterDefaultRecorders).",
			Impact: "No git-event recorders were registered. Hook events that fire after\n" +
				"this point will be dispatched without anyone listening for git\n" +
				"commits, so those commits won't reach the audit log.",
			Fix: "1. Create a hook manager first, then pass it to the registrar.\n" +
				"2. If you hit this from production code, this is a wiring bug — please\n" +
				"   file a bug.",
		}
	}
	gr, err := NewGitRecorder(tracker, auditDB)
	if err != nil {
		return nil, err
	}
	mgr.Register(gr)
	return gr, nil
}

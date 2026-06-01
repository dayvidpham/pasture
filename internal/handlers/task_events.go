// Package handlers — task_events.go
//
// Handler for `pasture task events` (PROPOSAL-2 §7.9 / §11 Scenario 6).
//
// Surface:
//
//	pasture task events [--epoch-id E] [--phase P] [--agent A] [--type T]
//	                    [--since TS]
//	                    [--context-kind K --context-id ID]
//	                    [--format json|text]
//
// At least one of {--epoch-id, --context-kind+--context-id} MUST be provided
// — without a top-level filter the query would return the full event log,
// which is rarely what the user wants and is inefficient.
//
// Routing through protocol.TaskTracker:
//
//   - When --context-kind / --context-id are given, the handler uses
//     TaskTracker.Timeline(ctx, kind, contextId) which JOINs context_edges
//     against audit_events. This is the supported way to query Git, Skill,
//     Session and (post-S4) Epoch contexts.
//
//   - When only --epoch-id is given, the handler uses TaskTracker.QueryEvents
//     against the legacy v1 epoch_id column. After S4 lands and removes the
//     epoch_id column, this branch will switch to Timeline(ContextEpoch,
//     epochId) — same code path as the context-kind branch. The CLI surface
//     does not change.
//
// Post-fetch filtering (--phase, --agent, --type, --since) is done in Go
// because it is cheap (event lists are typically small per epoch) and avoids
// duplicating the SQL projection in two places. A future optimisation could
// push these into the SQL WHERE clause, but until that's measured-needed the
// straightforward pattern is preferred.
package handlers

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TaskEventsInput captures the CLI inputs for `pasture task events`.
type TaskEventsInput struct {
	DBPath      string
	EpochId     string                // empty when no --epoch-id flag
	Phase       *protocol.PhaseId     // nil when no --phase flag
	Agent       string                // empty when no --agent flag (matched against AuditEvent.Role until v3 backfill lands)
	EventType   *protocol.EventType   // nil when no --type flag
	Since       *time.Time            // nil when no --since flag
	ContextKind *protocol.ContextKind // nil when no --context-kind flag
	ContextId   string                // empty when no --context-id flag
}

// TaskEvents queries audit events and prints them. Returns the standard
// (exitCode, error) tuple.
func TaskEvents(w io.Writer, in TaskEventsInput, format types.OutputFormat) (int, error) {
	// Validation: must have either an epoch-id OR a (context-kind +
	// context-id) pair. Without one of these the query is unbounded.
	hasEpoch := in.EpochId != ""
	hasContext := in.ContextKind != nil || in.ContextId != ""

	if !hasEpoch && !hasContext {
		se := &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     "A top-level filter is required to list events — no top-level filter was supplied.",
			Why:      "Neither --epoch-id nor the --context-kind + --context-id pair was passed.",
			Impact:   "Without a filter the query would return the entire event log, so the command intentionally refuses to run.",
			Fix: "1. Narrow the query to one epoch by passing its ID:\n" +
				"     pasture task events --epoch-id <id>\n" +
				"2. Or list events attached to a specific context (commit, skill, session, etc.):\n" +
				"     pasture task events --context-kind <kind> --context-id <id>\n" +
				"   Supported kinds: " + listContextKindWireValues() + "\n" +
				"3. To find epoch IDs, list recent tasks:\n" +
				"     pasture task list",
		}
		return errors.ExitCode(se), se
	}

	// Both --context-kind and --context-id must be paired; one without the
	// other is ambiguous.
	if (in.ContextKind != nil) != (in.ContextId != "") {
		se := &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     "The --context-kind and --context-id flags must be passed together.",
			Why:      "Only one of the pair was provided. Each flag is meaningless on its own — the kind names what type of context to look at, and the ID names which one.",
			Impact:   "The contexts query can't be assembled with only half of the pair.",
			Fix: "1. Pass both flags together:\n" +
				"     pasture task events --context-kind=GitContext --context-id=abc123\n" +
				"2. Or omit both and use --epoch-id instead:\n" +
				"     pasture task events --epoch-id <id>\n" +
				"3. Supported kinds: " + listContextKindWireValues(),
		}
		return errors.ExitCode(se), se
	}

	tracker, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return errors.ExitCode(err), err
	}
	defer tracker.Close()

	ctx := context.Background()

	var events []protocol.AuditEvent
	switch {
	case hasContext:
		// Context-edge query takes precedence; after S4 the epoch path will
		// fold into this branch via ContextEpoch.
		events, err = tracker.Timeline(ctx, *in.ContextKind, in.ContextId)
		if err != nil {
			return errors.ExitCode(err), err
		}
		// If the user ALSO passed --epoch-id alongside the context filter,
		// narrow the result to events whose EpochId matches. Useful for
		// "events on commit X that happened during epoch Y".
		if hasEpoch {
			events = filterByEpoch(events, in.EpochId)
		}
	case hasEpoch:
		events, err = tracker.QueryEvents(ctx, in.EpochId, in.Phase, agentRoleFilter(in.Agent))
		if err != nil {
			return errors.ExitCode(err), err
		}
	}

	// Post-fetch filtering. Phase/Agent are already applied SQL-side for the
	// epoch path; we apply them here too for the context path so the same
	// flag semantics hold regardless of the top-level filter.
	if in.Phase != nil {
		events = filterByPhase(events, *in.Phase)
	}
	if in.Agent != "" {
		events = filterByAgent(events, in.Agent)
	}
	if in.EventType != nil {
		events = filterByEventType(events, *in.EventType)
	}
	if in.Since != nil {
		events = filterBySince(events, *in.Since)
	}

	out, fErr := formatters.FormatAuditEvents(events, format)
	if fErr != nil {
		return errors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// agentRoleFilter returns a *string pointer for the Agent flag value. Until
// S3's v3 backfill lands, AuditEvent's Role column doubles as the "who fired
// this" attribution, so filtering by --agent maps to the role column. After
// S3, this branch will instead resolve --agent against agents_software.name
// or agent_id directly.
func agentRoleFilter(agent string) *string {
	if agent == "" {
		return nil
	}
	return &agent
}

func filterByEpoch(events []protocol.AuditEvent, epochId string) []protocol.AuditEvent {
	out := make([]protocol.AuditEvent, 0, len(events))
	for _, e := range events {
		if e.EpochId == epochId {
			out = append(out, e)
		}
	}
	return out
}

func filterByPhase(events []protocol.AuditEvent, phase protocol.PhaseId) []protocol.AuditEvent {
	out := make([]protocol.AuditEvent, 0, len(events))
	for _, e := range events {
		if e.Phase == phase {
			out = append(out, e)
		}
	}
	return out
}

func filterByAgent(events []protocol.AuditEvent, agent string) []protocol.AuditEvent {
	out := make([]protocol.AuditEvent, 0, len(events))
	for _, e := range events {
		// Until S3 lands, Role is the attribution column. After S3, the
		// filter will switch to AgentId resolved through agents_software.
		if e.Role == agent {
			out = append(out, e)
		}
	}
	return out
}

func filterByEventType(events []protocol.AuditEvent, eventType protocol.EventType) []protocol.AuditEvent {
	out := make([]protocol.AuditEvent, 0, len(events))
	for _, e := range events {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func filterBySince(events []protocol.AuditEvent, since time.Time) []protocol.AuditEvent {
	out := make([]protocol.AuditEvent, 0, len(events))
	for _, e := range events {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// ParseSinceFlag converts the user's --since string to a time.Time. Accepts
// RFC3339 (e.g. "2026-04-25T00:00:00Z"), Unix nanoseconds (e.g.
// "1745539200000000000"), or Unix seconds (e.g. "1745539200").
//
// Exposed as a public helper so the cobra-side Cmd init can validate the flag
// up-front and surface an actionable error before opening the database.
func ParseSinceFlag(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     "The --since flag needs a timestamp value.",
			Why:      "The flag was passed without a value.",
			Impact:   "The time filter can't be applied without knowing what cutoff to use.",
			Fix: "1. Pass a timestamp in one of these formats:\n" +
				"     --since=2026-04-25T00:00:00Z         (date and time)\n" +
				"     --since=1745539200                   (Unix seconds)\n" +
				"     --since=1745539200000000000          (Unix nanoseconds)\n" +
				"2. Or omit --since to include events from any time.",
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	// Numeric epoch — heuristic on length: <=10 digits is seconds; longer is
	// nanoseconds. (Milliseconds and microseconds would be ambiguous; we
	// don't currently accept them.)
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if len(raw) <= 10 {
			return time.Unix(n, 0).UTC(), nil
		}
		return time.Unix(0, n).UTC(), nil
	}
	return time.Time{}, &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     fmt.Sprintf("%q isn't a valid timestamp for --since.", raw),
		Why:      "The value isn't a recognised date-time format and isn't a number we can read as a Unix epoch.",
		Impact:   "The time filter can't be applied because we can't tell what cutoff you meant.",
		Fix: "1. Pass a timestamp in one of these formats:\n" +
			"     --since=2026-04-25T00:00:00Z         (date and time)\n" +
			"     --since=1745539200                   (Unix seconds)\n" +
			"     --since=1745539200000000000          (Unix nanoseconds)\n" +
			"2. Or omit --since to include events from any time.",
	}
}

// ParseContextKindFlag converts the user's --context-kind string to a
// protocol.ContextKind. The wire values match the enum's String() exactly
// (e.g. "GitContext", "EpochContext"); rejection is via IsValid so a typo
// surfaces an actionable list of valid kinds rather than a silent miss.
func ParseContextKindFlag(raw string) (protocol.ContextKind, error) {
	k := protocol.ContextKind(strings.TrimSpace(raw))
	if !k.IsValid() {
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("%q isn't a recognised value for --context-kind.", raw),
			Why:      "The value doesn't match any of the supported context kinds.",
			Impact:   "The contexts query can't be assembled because no events would ever match an unknown kind.",
			Fix: "1. Pass one of the supported kinds (case-sensitive):\n" +
				"     " + listContextKindWireValues() + "\n" +
				"   For example:\n" +
				"     pasture task events --context-kind=GitContext --context-id=<sha>",
		}
	}
	return k, nil
}

// ParseEventTypeFlag converts the user's --type string to a protocol.EventType.
// IsValid is enforced so unknown event types surface an actionable error.
func ParseEventTypeFlag(raw string) (protocol.EventType, error) {
	et := protocol.EventType(strings.TrimSpace(raw))
	if !et.IsValid() {
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("%q isn't a recognised value for --type.", raw),
			Why:      "The value doesn't match any of the supported event types.",
			Impact:   "The event-type filter can't be applied because no events would ever match an unknown type.",
			Fix: "1. Pass one of the supported event types (case-sensitive):\n" +
				"     " + listEventTypeWireValues() + "\n" +
				"   For example:\n" +
				"     pasture task events --epoch-id <id> --type=PhaseTransition",
		}
	}
	return et, nil
}

// listContextKindWireValues renders the sorted, comma-separated wire values
// for the help text and error messages. Centralised so a future enum
// addition picks up here automatically (per protocol.AllContextKinds).
func listContextKindWireValues() string {
	parts := make([]string, len(protocol.AllContextKinds))
	for i, k := range protocol.AllContextKinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, ", ")
}

func listEventTypeWireValues() string {
	parts := make([]string, len(protocol.AllEventTypes))
	for i, t := range protocol.AllEventTypes {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}

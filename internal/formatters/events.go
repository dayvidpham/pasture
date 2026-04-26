// Package formatters — events.go
//
// Output formatting for the new `pasture task events / timeline / contexts /
// agents` subcommands introduced by PROPOSAL-2 §7.9 (S6).
//
// Each formatter supports two output modes per the project-wide convention:
//   - OutputJSON: json.MarshalIndent with camelCase keys.
//   - OutputText: human-readable multi-line summary.
//
// Public API is symmetrical with formatters/task.go (FormatTask / FormatTasks):
//
//   - FormatAuditEvent      — single audit event.
//   - FormatAuditEvents     — list of audit events (events / timeline output).
//   - FormatContextList     — list of (Kind, ContextID) edges for one event.
//   - FormatAgentEntry      — single registered well-known agent + categories.
//   - FormatAgentEntries    — list of registered agents (agents list).
package formatters

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// auditEventJSON is the JSON wire representation of a protocol.AuditEvent.
// camelCase keys mirror protocol.AuditEvent's existing struct tags so the
// formatter shape is byte-stable across the public façade.
type auditEventJSON struct {
	EpochID   string         `json:"epochId"`
	Phase     string         `json:"phase"`
	Role      string         `json:"role"`
	EventType string         `json:"eventType"`
	Payload   map[string]any `json:"payload"`
	Timestamp string         `json:"timestamp"`
}

func toAuditEventJSON(e protocol.AuditEvent) auditEventJSON {
	return auditEventJSON{
		EpochID:   e.EpochID,
		Phase:     string(e.Phase),
		Role:      e.Role,
		EventType: string(e.EventType),
		Payload:   e.Payload,
		Timestamp: e.Timestamp.UTC().Format(time.RFC3339Nano),
	}
}

// FormatAuditEvent renders a single audit event in the requested format.
//
// Used by `pasture task contexts <event-id>` (paired with the contexts list)
// and as a building block for FormatAuditEvents.
func FormatAuditEvent(e protocol.AuditEvent, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(toAuditEventJSON(e), "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatAuditEvent: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the audit event cannot be rendered as JSON",
				Fix:      "inspect the event Payload for non-JSON-serializable values (channels, functions, complex numbers)",
			}
		}
		return string(b), nil
	case types.OutputText:
		return renderAuditEventText(e), nil
	default:
		return "", unknownFormatErr("FormatAuditEvent", format)
	}
}

// FormatAuditEvents renders a list of audit events. JSON mode is a top-level
// array; text mode is one line per event suitable for piping to grep/fzf.
//
// Used by `pasture task events` and `pasture task timeline`.
func FormatAuditEvents(events []protocol.AuditEvent, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]auditEventJSON, len(events))
		for i, e := range events {
			js[i] = toAuditEventJSON(e)
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatAuditEvents: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the audit event list cannot be rendered as JSON",
				Fix:      "inspect each event's Payload for non-JSON-serializable values (channels, functions, complex numbers)",
			}
		}
		return string(b), nil
	case types.OutputText:
		if len(events) == 0 {
			return "(no events)", nil
		}
		lines := make([]string, 0, len(events))
		for _, e := range events {
			lines = append(lines, renderAuditEventListLine(e))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", unknownFormatErr("FormatAuditEvents", format)
	}
}

func renderAuditEventText(e protocol.AuditEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "EpochID:   %s\n", e.EpochID)
	fmt.Fprintf(&b, "Phase:     %s\n", e.Phase)
	fmt.Fprintf(&b, "Role:      %s\n", e.Role)
	fmt.Fprintf(&b, "EventType: %s\n", e.EventType)
	fmt.Fprintf(&b, "Timestamp: %s\n", e.Timestamp.UTC().Format(time.RFC3339Nano))
	if len(e.Payload) > 0 {
		// Render payload as compact JSON for readability; failures fall back to
		// the Go fmt-string (which never errors).
		if pj, err := json.Marshal(e.Payload); err == nil {
			fmt.Fprintf(&b, "Payload:   %s\n", string(pj))
		} else {
			fmt.Fprintf(&b, "Payload:   %v\n", e.Payload)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderAuditEventListLine(e protocol.AuditEvent) string {
	ts := e.Timestamp.UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s [%s] [%s] %s/%s — %s",
		ts, e.Phase, e.Role, e.EpochID, e.EventType, summarisePayload(e.Payload))
}

// summarisePayload returns a one-line summary suitable for the list view.
// Empty payload renders as "(empty)"; otherwise the JSON is truncated to keep
// list output one-line-per-event for grep/fzf piping.
func summarisePayload(payload map[string]any) string {
	if len(payload) == 0 {
		return "(empty)"
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("(unrenderable payload: %v)", err)
	}
	const maxLen = 120
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// contextJSON is the JSON wire representation of one (Kind, ContextID) edge.
// Mirrors protocol.Context's existing tags.
type contextJSON struct {
	Kind      string `json:"kind"`
	ContextID string `json:"contextId"`
}

func toContextJSON(c protocol.Context) contextJSON {
	return contextJSON{
		Kind:      string(c.Kind),
		ContextID: c.ContextID,
	}
}

// FormatContextList renders the context_edges attached to one event.
//
// Used by `pasture task contexts <event-id>`.
func FormatContextList(contexts []protocol.Context, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]contextJSON, len(contexts))
		for i, c := range contexts {
			js[i] = toContextJSON(c)
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatContextList: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the context-edge list cannot be rendered as JSON",
				Fix:      "this should not happen with the typed Context shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		if len(contexts) == 0 {
			return "(no contexts attached to this event)", nil
		}
		lines := make([]string, 0, len(contexts))
		for _, c := range contexts {
			lines = append(lines, fmt.Sprintf("%s: %s", c.Kind, c.ContextID))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", unknownFormatErr("FormatContextList", format)
	}
}

// AgentEntry is the tuple presented by `pasture task agents list / show`. It
// pairs a Provenance AgentID (wire string) with its registered well-known
// name (if any), and the AutomatonRole / PastureRole stored in
// pasture_agent_categories. Lives in the formatters package because it is the
// view-model for one CLI subcommand and has no business existing inside the
// tracker (which deals in raw rows).
type AgentEntry struct {
	AgentID       string
	WellKnownName string // empty when no row exists in pasture_well_known_agents
	AutomatonRole protocol.AutomatonRole
	PastureRole   protocol.PastureRole
}

type agentEntryJSON struct {
	AgentID       string `json:"agentId"`
	WellKnownName string `json:"wellKnownName,omitempty"`
	AutomatonRole string `json:"automatonRole"`
	PastureRole   string `json:"pastureRole"`
}

func toAgentEntryJSON(a AgentEntry) agentEntryJSON {
	return agentEntryJSON{
		AgentID:       a.AgentID,
		WellKnownName: a.WellKnownName,
		AutomatonRole: string(a.AutomatonRole),
		PastureRole:   string(a.PastureRole),
	}
}

// FormatAgentEntry renders one agent + its categories.
//
// Used by `pasture task agents show <id>`.
func FormatAgentEntry(a AgentEntry, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(toAgentEntryJSON(a), "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatAgentEntry: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the agent entry cannot be rendered as JSON",
				Fix:      "this should not happen with the typed AgentEntry shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		var b strings.Builder
		fmt.Fprintf(&b, "AgentID:        %s\n", a.AgentID)
		if a.WellKnownName != "" {
			fmt.Fprintf(&b, "WellKnownName:  %s\n", a.WellKnownName)
		}
		fmt.Fprintf(&b, "AutomatonRole:  %s\n", a.AutomatonRole)
		fmt.Fprintf(&b, "PastureRole:    %s\n", a.PastureRole)
		return strings.TrimRight(b.String(), "\n"), nil
	default:
		return "", unknownFormatErr("FormatAgentEntry", format)
	}
}

// FormatAgentEntries renders a list of registered agents.
//
// Used by `pasture task agents list`.
func FormatAgentEntries(entries []AgentEntry, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]agentEntryJSON, len(entries))
		for i, e := range entries {
			js[i] = toAgentEntryJSON(e)
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatAgentEntries: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the agent list cannot be rendered as JSON",
				Fix:      "this should not happen with the typed AgentEntry shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		if len(entries) == 0 {
			return "(no registered agents)", nil
		}
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.WellKnownName
			if name == "" {
				name = "(unnamed)"
			}
			lines = append(lines, fmt.Sprintf("%s [%s/%s] %s",
				e.AgentID, e.AutomatonRole, e.PastureRole, name))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", unknownFormatErr("FormatAgentEntries", format)
	}
}

// MigratePlan is the data the `pasture migrate --dry-run` handler hands to the
// formatter: each entry describes one forward step the migrator WOULD apply.
// FromVersion is the currently-recorded on-disk version; ToVersion is the
// version after applying this step. The CurrentVersion / TargetVersion fields
// describe the overall plan span.
type MigratePlan struct {
	DBPath         string
	CurrentVersion int
	TargetVersion  int
	Steps          []MigratePlanStep
	DryRun         bool
}

// MigratePlanStep is one row in a migration plan.
type MigratePlanStep struct {
	FromVersion int
	ToVersion   int
	Description string
}

type migratePlanJSON struct {
	DBPath         string                `json:"dbPath"`
	CurrentVersion int                   `json:"currentVersion"`
	TargetVersion  int                   `json:"targetVersion"`
	DryRun         bool                  `json:"dryRun"`
	Steps          []migratePlanStepJSON `json:"steps"`
}

type migratePlanStepJSON struct {
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
	Description string `json:"description"`
}

// FormatMigratePlan renders the dry-run plan output for `pasture migrate`.
//
// JSON mode is a structured object so CI scripts can parse it; text mode is
// a human-readable summary keyed by audit.stepDescription (Phase 11 R1-A
// uses plain-language, backfill-first phrasing — e.g. "v3->v4: backfill
// epoch IDs into the context-edge table, then drop the legacy epoch_id
// column").
func FormatMigratePlan(plan MigratePlan, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := migratePlanJSON{
			DBPath:         plan.DBPath,
			CurrentVersion: plan.CurrentVersion,
			TargetVersion:  plan.TargetVersion,
			DryRun:         plan.DryRun,
			Steps:          make([]migratePlanStepJSON, len(plan.Steps)),
		}
		for i, s := range plan.Steps {
			js.Steps[i] = migratePlanStepJSON{
				FromVersion: s.FromVersion,
				ToVersion:   s.ToVersion,
				Description: s.Description,
			}
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatMigratePlan: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the migration plan cannot be rendered as JSON",
				Fix:      "this should not happen with the typed MigratePlan shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		var b strings.Builder
		if plan.DryRun {
			fmt.Fprintf(&b, "Dry run: %s (v%d -> v%d)\n", plan.DBPath, plan.CurrentVersion, plan.TargetVersion)
		} else {
			fmt.Fprintf(&b, "Plan: %s (v%d -> v%d)\n", plan.DBPath, plan.CurrentVersion, plan.TargetVersion)
		}
		if len(plan.Steps) == 0 {
			b.WriteString("  (no migrations needed; already at the highest known schema version)\n")
		} else {
			for _, s := range plan.Steps {
				fmt.Fprintf(&b, "  v%d->v%d: %s\n", s.FromVersion, s.ToVersion, s.Description)
			}
		}
		return strings.TrimRight(b.String(), "\n"), nil
	default:
		return "", unknownFormatErr("FormatMigratePlan", format)
	}
}

// MigrateResult is the data the `pasture migrate` handler hands the formatter
// after a successful (non-dry-run) migration.
type MigrateResult struct {
	DBPath      string
	FromVersion int
	ToVersion   int
}

type migrateResultJSON struct {
	DBPath      string `json:"dbPath"`
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
}

// FormatMigrateResult renders the post-migration success line.
//
// Text mode matches the §7.9 wording exactly:
//
//	migrated <db-path> from v<from> to v<to>
func FormatMigrateResult(r MigrateResult, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(migrateResultJSON{
			DBPath:      r.DBPath,
			FromVersion: r.FromVersion,
			ToVersion:   r.ToVersion,
		}, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatMigrateResult: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the migration result cannot be rendered as JSON",
				Fix:      "this should not happen with the typed MigrateResult shape; file a bug if it does",
			}
		}
		return string(b), nil
	case types.OutputText:
		return fmt.Sprintf("migrated %s from v%d to v%d", r.DBPath, r.FromVersion, r.ToVersion), nil
	default:
		return "", unknownFormatErr("FormatMigrateResult", format)
	}
}

// unknownFormatErr returns the shared "unrecognized output format" error used
// across all formatters in this file. Centralised so the wording stays
// consistent and a single edit updates every formatter.
func unknownFormatErr(fn string, format types.OutputFormat) error {
	return &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     fmt.Sprintf("formatters.%s: unrecognized output format %q", fn, format),
		Why:      "OutputFormat must be one of: json, text",
		Impact:   "Output cannot be rendered",
		Fix:      "Pass --format json or --format text (or omit for default text)",
	}
}

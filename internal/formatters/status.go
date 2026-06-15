// Package formatters — status.go
//
// Output formatting for `pasture status [--epoch <id>]`.
//
// Two output shapes:
//   - EpochStatusResult: full status view for a single epoch — phase, role,
//     available transitions, slice progress, active sessions, and recent audit
//     events (including any EpochCancelled event with its reason).
//   - EpochSummaryList: the listing view when --epoch is omitted — one row per
//     known epoch, showing its current phase and how many events it has.
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

// EpochStatusResult is the data model for `pasture status --epoch <id>`.
// It combines the projected EpochState snapshot with recent audit events so
// the CLI can present a single-screen overview without querying multiple
// commands.
type EpochStatusResult struct {
	// EpochId is the epoch being described.
	EpochId string
	// CurrentPhase is the epoch's current phase.
	CurrentPhase protocol.PhaseId
	// CurrentRole is the agent role expected to act at the current phase.
	CurrentRole protocol.RoleId
	// AvailableTransitions lists the phases the epoch can advance to from its
	// current state (computed from the FSM — not a value frozen at write time).
	AvailableTransitions []protocol.PhaseId
	// TransitionHistory is the ordered list of phase transitions made so far.
	TransitionHistory []protocol.TransitionRecord
	// SliceProgress is the slice-completion events reported to this epoch.
	SliceProgress []protocol.SliceProgressSignal
	// ActiveSessions is the set of sessions currently registered with this epoch.
	ActiveSessions []protocol.RegisterSessionSignal
	// RecentEvents is the N most-recent audit events for quick history review.
	// Events are in ascending (oldest-first) order so the most recent is last.
	RecentEvents []protocol.AuditEvent
	// CancelReason is non-nil when an EpochCancelled event appears in the audit
	// trail, carrying the operator reason (which may be an empty string if no
	// reason was provided).
	CancelReason *string
}

// epochStatusJSON is the JSON wire representation of EpochStatusResult.
type epochStatusJSON struct {
	EpochId              string                 `json:"epochId"`
	CurrentPhase         string                 `json:"currentPhase"`
	CurrentRole          string                 `json:"currentRole"`
	AvailableTransitions []string               `json:"availableTransitions"`
	TransitionHistory    []transitionRecordJSON `json:"transitionHistory"`
	SliceProgress        []sliceProgressJSON    `json:"sliceProgress"`
	ActiveSessions       []activeSessionJSON    `json:"activeSessions"`
	RecentEvents         []auditEventJSON       `json:"recentEvents"`
	CancelReason         *string                `json:"cancelReason,omitempty"`
}

// FormatEpochStatus renders a single-epoch status view.
//
// Text mode: a human-readable overview with labeled sections.
// JSON mode: a structured object with camelCase keys.
func FormatEpochStatus(r EpochStatusResult, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		avail := make([]string, len(r.AvailableTransitions))
		for i, p := range r.AvailableTransitions {
			avail[i] = string(p)
		}
		history := make([]transitionRecordJSON, len(r.TransitionHistory))
		for i, tr := range r.TransitionHistory {
			history[i] = transitionRecordJSON{
				FromPhase:    string(tr.FromPhase),
				ToPhase:      string(tr.ToPhase),
				Timestamp:    tr.Timestamp.UTC().Format(time.RFC3339),
				TriggeredBy:  tr.TriggeredBy,
				ConditionMet: tr.ConditionMet,
				Success:      tr.Success,
			}
		}
		progress := make([]sliceProgressJSON, len(r.SliceProgress))
		for i, p := range r.SliceProgress {
			progress[i] = sliceProgressJSON{
				SliceId:    p.SliceId,
				LeafTaskId: p.LeafTaskId,
				StageName:  p.StageName,
				Completed:  p.Completed,
			}
		}
		sessions := make([]activeSessionJSON, len(r.ActiveSessions))
		for i, s := range r.ActiveSessions {
			sessions[i] = activeSessionJSON{
				EpochId:      s.EpochId,
				SessionId:    s.SessionId,
				Role:         s.Role,
				ModelHarness: s.ModelHarness,
				Model:        s.Model,
			}
		}
		events := make([]auditEventJSON, len(r.RecentEvents))
		for i, e := range r.RecentEvents {
			events[i] = toAuditEventJSON(e)
		}
		data := epochStatusJSON{
			EpochId:              r.EpochId,
			CurrentPhase:         string(r.CurrentPhase),
			CurrentRole:          string(r.CurrentRole),
			AvailableTransitions: avail,
			TransitionHistory:    history,
			SliceProgress:        progress,
			ActiveSessions:       sessions,
			RecentEvents:         events,
			CancelReason:         r.CancelReason,
		}
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatEpochStatus: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the epoch status cannot be rendered as JSON",
				Fix:      "inspect the EpochStatusResult fields for non-JSON-serializable values",
			}
		}
		return string(b), nil

	case types.OutputText:
		var lines []string
		lines = append(lines, fmt.Sprintf("Epoch:  %s", r.EpochId))
		lines = append(lines, fmt.Sprintf("Phase:  %s", r.CurrentPhase))
		lines = append(lines, fmt.Sprintf("Role:   %s", r.CurrentRole))

		if r.CancelReason != nil {
			if *r.CancelReason == "" {
				lines = append(lines, "Status: CANCELLED (no reason recorded)")
			} else {
				lines = append(lines, fmt.Sprintf("Status: CANCELLED — %s", *r.CancelReason))
			}
		}

		if len(r.AvailableTransitions) > 0 {
			lines = append(lines, "Available transitions:")
			for _, p := range r.AvailableTransitions {
				lines = append(lines, fmt.Sprintf("  -> %s", p))
			}
		} else {
			lines = append(lines, "Available transitions: (none)")
		}

		if len(r.SliceProgress) > 0 {
			completed := 0
			for _, p := range r.SliceProgress {
				if p.Completed {
					completed++
				}
			}
			lines = append(lines, fmt.Sprintf("Slice progress: %d/%d complete", completed, len(r.SliceProgress)))
			for _, p := range r.SliceProgress {
				mark := "·"
				if p.Completed {
					mark = "✓"
				}
				lines = append(lines, fmt.Sprintf("  %s %s  stage=%s", mark, p.SliceId, p.StageName))
			}
		} else {
			lines = append(lines, "Slice progress: (none)")
		}

		if len(r.ActiveSessions) > 0 {
			lines = append(lines, fmt.Sprintf("Active sessions: %d", len(r.ActiveSessions)))
			for _, s := range r.ActiveSessions {
				lines = append(lines, fmt.Sprintf("  %s  role=%s", s.SessionId, s.Role))
			}
		} else {
			lines = append(lines, "Active sessions: (none)")
		}

		lines = append(lines, fmt.Sprintf("Transitions recorded: %d", len(r.TransitionHistory)))

		if len(r.RecentEvents) > 0 {
			lines = append(lines, fmt.Sprintf("Recent events: (last %d)", len(r.RecentEvents)))
			for _, e := range r.RecentEvents {
				ts := e.Timestamp.UTC().Format(time.RFC3339)
				lines = append(lines, fmt.Sprintf("  %s  [%s]  %s", ts, e.Phase, e.EventType))
			}
		} else {
			lines = append(lines, "Recent events: (none)")
		}

		return strings.Join(lines, "\n"), nil

	default:
		return "", unknownFormatErr("FormatEpochStatus", format)
	}
}

// EpochSummary is one row in the epoch listing when --epoch is omitted.
type EpochSummary struct {
	EpochId      string
	CurrentPhase protocol.PhaseId
	EventCount   int
}

// epochSummaryJSON is the JSON wire representation of EpochSummary.
type epochSummaryJSON struct {
	EpochId      string `json:"epochId"`
	CurrentPhase string `json:"currentPhase"`
	EventCount   int    `json:"eventCount"`
}

// FormatEpochList renders the epoch listing.
//
// Text mode: one epoch per line with phase and event count; an actionable empty
// message when no epochs exist.
// JSON mode: an array of epoch summary objects.
func FormatEpochList(epochs []EpochSummary, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]epochSummaryJSON, len(epochs))
		for i, e := range epochs {
			js[i] = epochSummaryJSON{
				EpochId:      e.EpochId,
				CurrentPhase: string(e.CurrentPhase),
				EventCount:   e.EventCount,
			}
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", &errors.StructuredError{
				Category: errors.CategoryStorage,
				What:     "formatters.FormatEpochList: json.MarshalIndent failed",
				Why:      err.Error(),
				Impact:   "the epoch list cannot be rendered as JSON",
				Fix:      "this should not happen with the typed EpochSummary shape; file a bug if it does",
			}
		}
		return string(b), nil

	case types.OutputText:
		if len(epochs) == 0 {
			return "No epochs recorded yet.\n\n" +
				"An epoch begins when a task is started with:\n" +
				"  pasture epoch start --epoch-id <id>\n\n" +
				"Create a task first if you don't have an ID:\n" +
				"  pasture task create \"<title>\" --type=feature", nil
		}
		lines := make([]string, 0, len(epochs)+1)
		lines = append(lines, fmt.Sprintf("Epochs: %d", len(epochs)))
		for _, e := range epochs {
			lines = append(lines, fmt.Sprintf("  %-50s  phase=%-20s  events=%d",
				e.EpochId, e.CurrentPhase, e.EventCount))
		}
		return strings.Join(lines, "\n"), nil

	default:
		return "", unknownFormatErr("FormatEpochList", format)
	}
}

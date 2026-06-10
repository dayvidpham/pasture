// Package formatters provides output formatting functions for pasture CLI commands.
//
// Each formatter supports two output modes:
//   - OutputJSON: json.MarshalIndent with camelCase keys
//   - OutputText: human-readable multi-line with labeled sections
//
// The FormatError function always returns a string (never errors) and is safe
// to call in defer/cleanup paths.
package formatters

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// epochStateJSON is the JSON wire representation of a QueryStateResult.
// camelCase keys match the Python aura-protocol output format.
type epochStateJSON struct {
	CurrentPhase         string                 `json:"currentPhase"`
	CurrentRole          string                 `json:"currentRole"`
	TransitionHistory    []transitionRecordJSON `json:"transitionHistory"`
	Votes                map[string]string      `json:"votes"`
	LastError            *string                `json:"lastError,omitempty"`
	AvailableTransitions []string               `json:"availableTransitions"`
	ActiveSessionCount   int                    `json:"activeSessionCount"`
}

type transitionRecordJSON struct {
	FromPhase    string `json:"fromPhase"`
	ToPhase      string `json:"toPhase"`
	Timestamp    string `json:"timestamp"`
	TriggeredBy  string `json:"triggeredBy"`
	ConditionMet string `json:"conditionMet"`
	Success      bool   `json:"success"`
}

// FormatEpochState formats a QueryStateResult for CLI output.
//
// JSON mode: json.MarshalIndent with camelCase keys.
// Text mode: human-readable multi-line with labeled sections.
func FormatEpochState(result protocol.QueryStateResult, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		history := make([]transitionRecordJSON, len(result.TransitionHistory))
		for i, r := range result.TransitionHistory {
			history[i] = transitionRecordJSON{
				FromPhase:    string(r.FromPhase),
				ToPhase:      string(r.ToPhase),
				Timestamp:    r.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00"),
				TriggeredBy:  r.TriggeredBy,
				ConditionMet: r.ConditionMet,
				Success:      r.Success,
			}
		}

		votes := make(map[string]string, len(result.Votes))
		for axis, vote := range result.Votes {
			votes[string(axis)] = string(vote)
		}

		avail := make([]string, len(result.AvailableTransitions))
		for i, p := range result.AvailableTransitions {
			avail[i] = string(p)
		}

		data := epochStateJSON{
			CurrentPhase:         string(result.CurrentPhase),
			CurrentRole:          string(result.CurrentRole),
			TransitionHistory:    history,
			Votes:                votes,
			LastError:            result.LastError,
			AvailableTransitions: avail,
			ActiveSessionCount:   result.ActiveSessionCount,
		}
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	case types.OutputText:
		var lines []string
		lines = append(lines, fmt.Sprintf("Phase: %s", result.CurrentPhase))
		lines = append(lines, fmt.Sprintf("Role:  %s", result.CurrentRole))

		if len(result.Votes) > 0 {
			lines = append(lines, "Votes:")
			for axis, vote := range result.Votes {
				lines = append(lines, fmt.Sprintf("  %s: %s", axis, vote))
			}
		} else {
			lines = append(lines, "Votes: (none)")
		}

		if result.LastError != nil {
			lines = append(lines, fmt.Sprintf("Last Error: %s", *result.LastError))
		}

		if len(result.AvailableTransitions) > 0 {
			lines = append(lines, "Available Transitions:")
			for _, p := range result.AvailableTransitions {
				lines = append(lines, fmt.Sprintf("  -> %s", p))
			}
		}

		lines = append(lines, fmt.Sprintf("Transitions: %d", len(result.TransitionHistory)))
		lines = append(lines, fmt.Sprintf("Active Sessions: %d", result.ActiveSessionCount))
		return strings.Join(lines, "\n"), nil

	default:
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("unrecognized output format %q", format),
			Why:      "OutputFormat must be one of: json, text",
			Impact:   "Output cannot be rendered",
			Fix:      "Pass --format json or --format text (or omit for default text)",
		}
	}
}

// FormatEpochQuery renders the slice of a QueryStateResult selected by query.
//
// full_state renders the complete view (via FormatEpochState); current_state
// renders only the phase + role; available_transitions renders only the
// reachable transitions. JSON keys stay camelCase and consistent with
// FormatEpochState so consumers parse one shape.
func FormatEpochQuery(result protocol.QueryStateResult, query protocol.QueryName, format types.OutputFormat) (string, error) {
	switch query {
	case protocol.QueryFullState:
		return FormatEpochState(result, format)

	case protocol.QueryCurrentState:
		switch format {
		case types.OutputJSON:
			b, err := json.MarshalIndent(struct {
				CurrentPhase string `json:"currentPhase"`
				CurrentRole  string `json:"currentRole"`
			}{string(result.CurrentPhase), string(result.CurrentRole)}, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		case types.OutputText:
			return fmt.Sprintf("Phase: %s\nRole:  %s", result.CurrentPhase, result.CurrentRole), nil
		default:
			return "", unrecognizedFormat(format)
		}

	case protocol.QueryAvailableTransitions:
		avail := make([]string, len(result.AvailableTransitions))
		for i, p := range result.AvailableTransitions {
			avail[i] = string(p)
		}
		switch format {
		case types.OutputJSON:
			b, err := json.MarshalIndent(struct {
				AvailableTransitions []string `json:"availableTransitions"`
			}{avail}, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		case types.OutputText:
			if len(avail) == 0 {
				return "Available Transitions: (none)", nil
			}
			var lines []string
			lines = append(lines, "Available Transitions:")
			for _, p := range avail {
				lines = append(lines, fmt.Sprintf("  -> %s", p))
			}
			return strings.Join(lines, "\n"), nil
		default:
			return "", unrecognizedFormat(format)
		}

	default:
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a state query this formatter renders.", query),
			Why:      "FormatEpochQuery renders current_state, available_transitions, and full_state.",
			Where:    "Formatting an epoch query (internal/formatters/formatters.go in formatters.FormatEpochQuery).",
			Impact:   "The query result can't be rendered.",
			Fix:      "Pass current_state, available_transitions, or full_state.",
		}
	}
}

// activeSessionJSON is the JSON wire representation of a registered session.
type activeSessionJSON struct {
	EpochId      string `json:"epochId,omitempty"`
	SessionId    string `json:"sessionId"`
	Role         string `json:"role"`
	ModelHarness string `json:"modelHarness,omitempty"`
	Model        string `json:"model,omitempty"`
}

// FormatActiveSessions renders the sessions registered with an epoch (the
// active_sessions query). JSON mode emits an array; text mode lists one session
// per line, or a "(none)" marker when empty.
func FormatActiveSessions(sessions []protocol.RegisterSessionSignal, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		out := make([]activeSessionJSON, len(sessions))
		for i, s := range sessions {
			out[i] = activeSessionJSON{
				EpochId:      s.EpochId,
				SessionId:    s.SessionId,
				Role:         s.Role,
				ModelHarness: s.ModelHarness,
				Model:        s.Model,
			}
		}
		b, err := json.MarshalIndent(struct {
			ActiveSessions []activeSessionJSON `json:"activeSessions"`
		}{out}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	case types.OutputText:
		if len(sessions) == 0 {
			return "Active Sessions: (none)", nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("Active Sessions: %d", len(sessions)))
		for _, s := range sessions {
			lines = append(lines, fmt.Sprintf("  %s  role=%s  model=%s", s.SessionId, s.Role, s.Model))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", unrecognizedFormat(format)
	}
}

// sliceProgressJSON is the JSON wire representation of a slice-progress event.
type sliceProgressJSON struct {
	SliceId    string `json:"sliceId"`
	LeafTaskId string `json:"leafTaskId"`
	StageName  string `json:"stageName"`
	Completed  bool   `json:"completed"`
}

// FormatSliceProgressState renders the slice-progress events reported to an
// epoch (the slice_progress_state query). JSON mode emits an array; text mode
// lists one event per line, or a "(none)" marker when empty.
func FormatSliceProgressState(events []protocol.SliceProgressSignal, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		out := make([]sliceProgressJSON, len(events))
		for i, p := range events {
			out[i] = sliceProgressJSON{
				SliceId:    p.SliceId,
				LeafTaskId: p.LeafTaskId,
				StageName:  p.StageName,
				Completed:  p.Completed,
			}
		}
		b, err := json.MarshalIndent(struct {
			SliceProgress []sliceProgressJSON `json:"sliceProgress"`
		}{out}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	case types.OutputText:
		if len(events) == 0 {
			return "Slice Progress: (none)", nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("Slice Progress: %d", len(events)))
		for _, p := range events {
			lines = append(lines, fmt.Sprintf("  slice=%s leaf=%s stage=%s completed=%t",
				p.SliceId, p.LeafTaskId, p.StageName, p.Completed))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", unrecognizedFormat(format)
	}
}

// unrecognizedFormat builds the standard actionable error for an unknown
// OutputFormat, shared by the per-query renderers above.
func unrecognizedFormat(format types.OutputFormat) error {
	return &errors.StructuredError{
		Category: errors.CategoryValidation,
		What:     fmt.Sprintf("unrecognized output format %q", format),
		Why:      "OutputFormat must be one of: json, text",
		Impact:   "Output cannot be rendered",
		Fix:      "Pass --format json or --format text (or omit for default text)",
	}
}

// hookRecordJSON is the JSON wire representation of a recorded hook event.
// camelCase keys match the package convention. Metadata fields use omitempty
// so keys absent from the recording (e.g. branch on a detached HEAD, or
// remotes in a repo-less context) are omitted rather than emitted as zero values.
type hookRecordJSON struct {
	EventType string            `json:"eventType"`
	SHA       string            `json:"sha"`
	EventID   int64             `json:"eventId"`
	Message   string            `json:"message,omitempty"`
	Author    string            `json:"author,omitempty"`
	Branch    string            `json:"branch,omitempty"`
	Timestamp string            `json:"timestamp,omitempty"`
	Repo      string            `json:"repo,omitempty"`
	Remotes   map[string]string `json:"remotes,omitempty"`
}

// FormatHookRecord formats the result of `pasture hook record` for CLI output.
//
// JSON mode: camelCase keys for all recorded fields; metadata fields (including
// repo and remotes) are omitted via omitempty when absent.
//
// Text mode: "recorded <eventType> event for sha <sha> (event #N)" (unchanged).
func FormatHookRecord(eventType, sha string, eventID int64, message, author, branch, timestamp, repo string, remotes map[string]string, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(hookRecordJSON{
			EventType: eventType,
			SHA:       sha,
			EventID:   eventID,
			Message:   message,
			Author:    author,
			Branch:    branch,
			Timestamp: timestamp,
			Repo:      repo,
			Remotes:   remotes,
		}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	case types.OutputText:
		return fmt.Sprintf("recorded %s event for sha %s (event #%d)", eventType, sha, eventID), nil

	default:
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("unrecognized output format %q", format),
			Why:      "OutputFormat must be one of: json, text",
			Impact:   "Output cannot be rendered",
			Fix:      "Pass --format json or --format text (or omit for default text)",
		}
	}
}

// startResultJSON is the JSON wire representation of a start result.
type startResultJSON struct {
	WorkflowId string `json:"workflowId"`
	RunId      string `json:"runId"`
}

// FormatStartResult formats an epoch start result for CLI output.
//
// JSON mode: {"workflowId": "...", "runId": "..."}
// Text mode: "Started epoch: workflow_id=..., run_id=..."
func FormatStartResult(workflowId, runId string, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(startResultJSON{WorkflowId: workflowId, RunId: runId}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	case types.OutputText:
		return fmt.Sprintf("Started epoch: workflow_id=%s, run_id=%s", workflowId, runId), nil

	default:
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("unrecognized output format %q", format),
			Why:      "OutputFormat must be one of: json, text",
			Impact:   "Output cannot be rendered",
			Fix:      "Pass --format json or --format text (or omit for default text)",
		}
	}
}

// signalResultJSON is the JSON wire representation of a signal result.
type signalResultJSON struct {
	Success bool `json:"success"`
}

// FormatSignalResult formats a signal delivery result for CLI output.
//
// JSON mode: {"success": true/false}
// Text mode: "Signal delivered successfully" / "Signal delivery failed"
func FormatSignalResult(success bool, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(signalResultJSON{Success: success}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil

	case types.OutputText:
		if success {
			return "Signal delivered successfully", nil
		}
		return "Signal delivery failed", nil

	default:
		return "", &errors.StructuredError{
			Category: errors.CategoryValidation,
			What:     fmt.Sprintf("unrecognized output format %q", format),
			Why:      "OutputFormat must be one of: json, text",
			Impact:   "Output cannot be rendered",
			Fix:      "Pass --format json or --format text (or omit for default text)",
		}
	}
}

// errorJSON is the JSON wire representation of a StructuredError.
type errorJSON struct {
	Category string `json:"category"`
	What     string `json:"what"`
	Why      string `json:"why"`
	Impact   string `json:"impact"`
	Fix      string `json:"fix"`
}

// FormatError formats an error for CLI output.
//
// If err is a *errors.StructuredError, the full diagnostic fields are included.
// For JSON format, returns a JSON object with category/what/why/impact/fix fields.
// For Text format, returns the multi-line Report() output.
// For unknown formats or plain errors, falls back to err.Error().
// Always returns a non-empty string; never returns an error itself.
func FormatError(err error, format types.OutputFormat) string {
	if err == nil {
		return ""
	}

	var se *errors.StructuredError
	if stderrors.As(err, &se) {
		switch format {
		case types.OutputJSON:
			b, jsonErr := json.MarshalIndent(errorJSON{
				Category: string(se.Category),
				What:     se.What,
				Why:      se.Why,
				Impact:   se.Impact,
				Fix:      se.Fix,
			}, "", "  ")
			if jsonErr != nil {
				return err.Error()
			}
			return string(b)

		case types.OutputText:
			var sb strings.Builder
			se.Report(&sb)
			return strings.TrimRight(sb.String(), "\n")
		}
	}

	return err.Error()
}

// Package formatters provides output formatting functions for pasture-msg CLI commands.
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
func FormatEpochState(result types.QueryStateResult, format types.OutputFormat) (string, error) {
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

// hookRecordJSON is the JSON wire representation of a recorded hook event.
// camelCase keys match the package convention. Metadata fields use omitempty
// so keys absent from the recording (e.g. branch on a detached HEAD) are
// omitted rather than emitted as empty strings.
type hookRecordJSON struct {
	EventType string `json:"eventType"`
	SHA       string `json:"sha"`
	EventID   int64  `json:"eventId"`
	Message   string `json:"message,omitempty"`
	Author    string `json:"author,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// FormatHookRecord formats the result of `pasture hook record` for CLI output.
//
// JSON mode: {"eventType": "...", "sha": "...", "eventId": N, "message": "...",
//
//	"author": "...", "branch": "...", "timestamp": "..."} — metadata fields
//	are omitted (omitempty) when not recorded (e.g. branch on detached HEAD).
//
// Text mode: "recorded <eventType> event for sha <sha> (event #N)" (unchanged).
func FormatHookRecord(eventType, sha string, eventID int64, message, author, branch, timestamp string, format types.OutputFormat) (string, error) {
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

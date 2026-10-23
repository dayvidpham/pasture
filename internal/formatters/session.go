// Package formatters — session.go
//
// Output formatting for `pasture session list` and `pasture session show`.
//
// Both renderers keep the JSON wire shape owned by internal/sessionapi (Page and
// Detail) so a script reading the CLI sees the same object a script reading the
// local API sees. Text mode renders a compact table for a page and a labelled
// block for one session.
package formatters

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/types"
)

// sessionMissing is the text-mode placeholder for an activity field the cache
// does not carry (an unbound session has no epoch, a binding-only session has
// no lifecycle event).
const sessionMissing = "-"

// sessionTimeLayout renders session instants in UTC at second precision.
const sessionTimeLayout = time.RFC3339

// FormatSessionPage renders one page of sessions.
//
// JSON mode: the sessionapi.Page shape, so the CLI and the local API agree on
// field names and nesting.
// Text mode: one row per session with state, harness, session, epoch, last
// event, and last seen; a next-page cursor is printed when the page is full.
func FormatSessionPage(page sessionapi.Page, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		if page.Items == nil {
			page.Items = []sessionapi.SessionView{}
		}
		b, err := json.MarshalIndent(page, "", "  ")
		if err != nil {
			return "", sessionJSONError("FormatSessionPage", err)
		}
		return string(b), nil

	case types.OutputText:
		return formatSessionPageText(page), nil

	default:
		return "", unknownFormatErr("FormatSessionPage", format)
	}
}

// FormatSessionDetail renders one session.
//
// JSON mode: the sessionapi.Detail shape.
// Text mode: a labelled block including the epoch, binding, activity instants,
// and the end marker when the session has ended.
func FormatSessionDetail(detail sessionapi.Detail, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(detail, "", "  ")
		if err != nil {
			return "", sessionJSONError("FormatSessionDetail", err)
		}
		return string(b), nil

	case types.OutputText:
		return formatSessionDetailText(detail), nil

	default:
		return "", unknownFormatErr("FormatSessionDetail", format)
	}
}

func formatSessionPageText(page sessionapi.Page) string {
	if len(page.Items) == 0 {
		return "No sessions recorded yet.\n\n" +
			"A session appears after its harness records a lifecycle event, or\n" +
			"after an explicit session bind."
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STATE\tHARNESS\tSESSION\tEPOCH\tLAST EVENT\tLAST SEEN")
	for _, s := range page.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Activity.State,
			s.Harness,
			s.NativeSessionID,
			sessionEpochCell(s),
			sessionTextOrMissing(s.Activity.LastEvent),
			sessionInstant(s.Activity.LastSeen),
		)
	}
	_ = tw.Flush()
	out := strings.TrimRight(b.String(), "\n")
	if page.Next != "" {
		out += "\n\nNext cursor: " + page.Next
	}
	return out
}

func formatSessionDetailText(detail sessionapi.Detail) string {
	s := detail.Session
	lines := []string{
		fmt.Sprintf("Harness:    %s", s.Harness),
		fmt.Sprintf("Session:    %s", s.NativeSessionID),
		fmt.Sprintf("State:      %s", s.Activity.State),
	}
	if s.Epoch != nil {
		lines = append(lines, fmt.Sprintf("Epoch:      %s", s.Epoch.ID))
		if s.Epoch.Title != "" {
			lines = append(lines, fmt.Sprintf("Epoch title: %s", s.Epoch.Title))
		}
	} else {
		lines = append(lines, "Epoch:      "+sessionMissing)
	}
	if s.Binding != nil {
		lines = append(lines, fmt.Sprintf("Role:       %s", s.Binding.Role))
		lines = append(lines, fmt.Sprintf("Binding:    %s", s.Binding.BindingID))
		lines = append(lines, fmt.Sprintf("Bound at:   %s", sessionInstant(&s.Binding.BoundAt)))
	}
	lines = append(lines,
		fmt.Sprintf("First seen: %s", sessionInstant(s.Activity.FirstSeen)),
		fmt.Sprintf("Last seen:  %s", sessionInstant(s.Activity.LastSeen)),
		fmt.Sprintf("Last event: %s", sessionTextOrMissing(s.Activity.LastEvent)),
	)
	if s.End != nil {
		lines = append(lines,
			fmt.Sprintf("Ended at:   %s", sessionInstant(&s.End.EndedAt)),
			fmt.Sprintf("End reason: %s", sessionTextOrMissing(s.End.EndReason)),
		)
	}
	lines = append(lines,
		fmt.Sprintf("Revision:   %d", detail.Revision),
		fmt.Sprintf("As of:      %s", detail.AsOf.UTC().Format(sessionTimeLayout)),
	)
	return strings.Join(lines, "\n")
}

func sessionEpochCell(s sessionapi.SessionView) string {
	if s.Epoch == nil || s.Epoch.ID == "" {
		return sessionMissing
	}
	return s.Epoch.ID
}

// sessionInstant renders one optional instant in UTC at second precision; the
// session cache owns nanosecond timestamps but an operator reads seconds.
func sessionInstant(t *time.Time) string {
	if t == nil {
		return sessionMissing
	}
	return t.UTC().Format(sessionTimeLayout)
}

func sessionTextOrMissing(value string) string {
	if value == "" {
		return sessionMissing
	}
	return value
}

func sessionJSONError(fn string, err error) error {
	return &errors.StructuredError{
		Category: errors.CategoryStorage,
		What:     fmt.Sprintf("formatters.%s: json.MarshalIndent failed", fn),
		Why:      err.Error(),
		Impact:   "the session view cannot be rendered as JSON",
		Fix:      "this should not happen with the typed sessionapi shapes; file a bug if it does",
	}
}

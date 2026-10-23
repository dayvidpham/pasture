package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/types"
)

func sessionTestTime(minutesAgo int) time.Time {
	return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Add(-time.Duration(minutesAgo) * time.Minute)
}

func sampleSessionPage() sessionapi.Page {
	first := sessionTestTime(30)
	activeSeen := sessionTestTime(1)
	idleSeen := sessionTestTime(20)
	endedSeen := sessionTestTime(5)
	return sessionapi.Page{
		Revision: 11,
		AsOf:     sessionTestTime(0),
		Items: []sessionapi.SessionView{
			{
				Harness:         "claude-code",
				NativeSessionID: "active-session",
				Epoch:           &sessionapi.EpochRef{ID: "file://epoch/active", Title: "Active epoch"},
				Activity: sessionapi.SessionActivity{
					State:     sessionapi.StateActive,
					FirstSeen: &first,
					LastSeen:  &activeSeen,
					LastEvent: "PreToolUse",
				},
				Binding: &sessionapi.SessionBinding{
					BindingID: "binding-active",
					Role:      "implementer",
					BoundAt:   first,
				},
			},
			{
				Harness:         "claude-code",
				NativeSessionID: "idle-session",
				Activity: sessionapi.SessionActivity{
					State:    sessionapi.StateIdle,
					LastSeen: &idleSeen,
				},
			},
			{
				Harness:         "opencode",
				NativeSessionID: "ended-session",
				Activity: sessionapi.SessionActivity{
					State:     sessionapi.StateEnded,
					LastSeen:  &endedSeen,
					LastEvent: "SessionEnd",
				},
				End: &sessionapi.EndMarker{EndedAt: endedSeen, EndReason: "session_end"},
			},
			{
				Harness:         "codex",
				NativeSessionID: "unknown-session",
				Activity: sessionapi.SessionActivity{
					State: sessionapi.StateUnknown,
				},
			},
		},
		Next: "next-page-token",
	}
}

func TestFormatSessionPageTextAndJSONShowTheSameRows(t *testing.T) {
	t.Parallel()

	page := sampleSessionPage()
	text, err := formatters.FormatSessionPage(page, types.OutputText)
	if err != nil {
		t.Fatalf("text render: %v", err)
	}
	jsonOut, err := formatters.FormatSessionPage(page, types.OutputJSON)
	if err != nil {
		t.Fatalf("json render: %v", err)
	}

	var decoded sessionapi.Page
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("decode page json: %v\n%s", err, jsonOut)
	}
	if decoded.Revision != page.Revision {
		t.Errorf("revision = %d, want %d", decoded.Revision, page.Revision)
	}
	if decoded.Next != page.Next {
		t.Errorf("next = %q, want %q", decoded.Next, page.Next)
	}
	if len(decoded.Items) != len(page.Items) {
		t.Fatalf("json items = %d, want %d", len(decoded.Items), len(page.Items))
	}

	// Text must carry every row, in the same order the JSON page emits.
	lastIndex := -1
	for _, item := range decoded.Items {
		index := strings.Index(text, item.NativeSessionID)
		if index < 0 {
			t.Errorf("text output is missing session %q:\n%s", item.NativeSessionID, text)
			continue
		}
		if index < lastIndex {
			t.Errorf("text row order differs from JSON order at %q:\n%s", item.NativeSessionID, text)
		}
		lastIndex = index
	}
	for _, state := range []sessionapi.ActivityState{sessionapi.StateActive, sessionapi.StateIdle, sessionapi.StateEnded, sessionapi.StateUnknown} {
		if !strings.Contains(text, string(state)) {
			t.Errorf("text output is missing state %q:\n%s", state, text)
		}
	}
	if !strings.Contains(text, "STATE") || !strings.Contains(text, "LAST EVENT") || !strings.Contains(text, "LAST SEEN") {
		t.Errorf("text output is missing its table header:\n%s", text)
	}
	if !strings.Contains(text, "file://epoch/active") {
		t.Errorf("text output is missing the bound epoch:\n%s", text)
	}
	if !strings.Contains(text, page.Next) {
		t.Errorf("text output is missing the next cursor:\n%s", text)
	}
}

func TestFormatSessionPageEmpty(t *testing.T) {
	t.Parallel()

	page := sessionapi.Page{Revision: 3, AsOf: sessionTestTime(0)}
	jsonOut, err := formatters.FormatSessionPage(page, types.OutputJSON)
	if err != nil {
		t.Fatalf("json render: %v", err)
	}
	if !strings.Contains(jsonOut, `"items": []`) {
		t.Errorf("empty JSON page must serialize items as an array:\n%s", jsonOut)
	}

	text, err := formatters.FormatSessionPage(page, types.OutputText)
	if err != nil {
		t.Fatalf("text render: %v", err)
	}
	if !strings.Contains(text, "No sessions recorded") {
		t.Errorf("empty text page must explain the empty state:\n%s", text)
	}
}

func TestFormatSessionDetailTextAndJSON(t *testing.T) {
	t.Parallel()

	first := sessionTestTime(30)
	last := sessionTestTime(2)
	ended := sessionTestTime(1)
	detail := sessionapi.Detail{
		Revision: 9,
		AsOf:     sessionTestTime(0),
		Session: sessionapi.SessionView{
			Harness:         "claude-code",
			NativeSessionID: "detail-session",
			Epoch:           &sessionapi.EpochRef{ID: "file://epoch/detail", Title: "Detail epoch"},
			Activity: sessionapi.SessionActivity{
				State:     sessionapi.StateEnded,
				FirstSeen: &first,
				LastSeen:  &last,
				LastEvent: "SessionEnd",
			},
			Binding: &sessionapi.SessionBinding{BindingID: "binding-detail", Role: "reviewer", BoundAt: first},
			End:     &sessionapi.EndMarker{EndedAt: ended, EndReason: "unbind"},
		},
	}

	text, err := formatters.FormatSessionDetail(detail, types.OutputText)
	if err != nil {
		t.Fatalf("text render: %v", err)
	}
	for _, want := range []string{
		"Harness:    claude-code",
		"Session:    detail-session",
		"State:      ended",
		"Epoch:      file://epoch/detail",
		"Epoch title: Detail epoch",
		"Role:       reviewer",
		"Binding:    binding-detail",
		"Last event: SessionEnd",
		"End reason: unbind",
		"Revision:   9",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("detail text is missing %q:\n%s", want, text)
		}
	}

	jsonOut, err := formatters.FormatSessionDetail(detail, types.OutputJSON)
	if err != nil {
		t.Fatalf("json render: %v", err)
	}
	var decoded sessionapi.Detail
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("decode detail json: %v\n%s", err, jsonOut)
	}
	if decoded.Session.NativeSessionID != detail.Session.NativeSessionID {
		t.Errorf("json session = %q, want %q", decoded.Session.NativeSessionID, detail.Session.NativeSessionID)
	}
	if decoded.Session.End == nil || decoded.Session.End.EndReason != "unbind" {
		t.Errorf("json detail lost the end marker: %+v", decoded.Session)
	}
}

func TestFormatSessionRejectsUnknownFormat(t *testing.T) {
	t.Parallel()

	if _, err := formatters.FormatSessionPage(sessionapi.Page{}, types.OutputFormat("yaml")); err == nil {
		t.Fatal("expected an error for an unknown page format")
	}
	if _, err := formatters.FormatSessionDetail(sessionapi.Detail{}, types.OutputFormat("yaml")); err == nil {
		t.Fatal("expected an error for an unknown detail format")
	}
}

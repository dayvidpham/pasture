package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/sessionapi/sessionapitest"
)

func TestCLI_SessionInventoryHasListAndShowOnly(t *testing.T) {
	t.Parallel()

	help := runCLI(t, "session", "--help")
	if help.exitCode != 0 {
		t.Fatalf("session help exit %d: %s", help.exitCode, help.stderr)
	}
	if got, want := commandNames(help.stdout), []string{"list", "show"}; !sameStrings(got, want) {
		t.Fatalf("session commands = %v, want %v\n%s", got, want, help.stdout)
	}
	if strings.Contains(help.stdout, "register") {
		t.Fatalf("retired session register must not be a command:\n%s", help.stdout)
	}
}

func TestCLI_SessionListAndShow(t *testing.T) {
	t.Parallel()

	db := sessionapitest.SeedStore(t)

	list := runCLI(t, "--db", db, "session", "list")
	if list.exitCode != 0 {
		t.Fatalf("session list exit %d: %s", list.exitCode, list.stderr)
	}
	for _, want := range []string{"STATE", sessionapitest.ActiveSession, sessionapitest.IdleSession, sessionapitest.EndedSession, sessionapitest.UnknownSession} {
		if !strings.Contains(list.stdout, want) {
			t.Errorf("text list is missing %q:\n%s", want, list.stdout)
		}
	}

	jsonList := runCLI(t, "--db", db, "--format", "json", "session", "list")
	if jsonList.exitCode != 0 {
		t.Fatalf("json session list exit %d: %s", jsonList.exitCode, jsonList.stderr)
	}
	var page sessionapi.Page
	if err := json.Unmarshal([]byte(jsonList.stdout), &page); err != nil {
		t.Fatalf("decode session page: %v\n%s", err, jsonList.stdout)
	}
	if len(page.Items) != 4 {
		t.Fatalf("json list items = %d, want 4\n%s", len(page.Items), jsonList.stdout)
	}
	states := make(map[string]sessionapi.ActivityState, len(page.Items))
	for _, item := range page.Items {
		states[item.NativeSessionID] = item.Activity.State
	}
	wantStates := map[string]sessionapi.ActivityState{
		sessionapitest.ActiveSession:  sessionapi.StateActive,
		sessionapitest.IdleSession:    sessionapi.StateIdle,
		sessionapitest.EndedSession:   sessionapi.StateEnded,
		sessionapitest.UnknownSession: sessionapi.StateUnknown,
	}
	for session, want := range wantStates {
		if states[session] != want {
			t.Errorf("state[%s] = %q, want %q", session, states[session], want)
		}
	}

	show := runCLI(t, "--db", db, "session", "show", sessionapitest.HarnessClaudeCode, sessionapitest.ActiveSession)
	if show.exitCode != 0 {
		t.Fatalf("session show exit %d: %s", show.exitCode, show.stderr)
	}
	for _, want := range []string{
		"Harness:    claude-code",
		"Session:    active-session",
		"State:      active",
		"Source:     explicit",
		"Confidence: verified",
		"Bindings:   2",
		sessionapitest.EpochOne,
		sessionapitest.EpochTwo,
	} {
		if !strings.Contains(show.stdout, want) {
			t.Errorf("detail text is missing %q:\n%s", want, show.stdout)
		}
	}

	jsonShow := runCLI(t, "--db", db, "--format", "json", "session", "show", sessionapitest.HarnessClaudeCode, sessionapitest.ActiveSession)
	if jsonShow.exitCode != 0 {
		t.Fatalf("json session show exit %d: %s", jsonShow.exitCode, jsonShow.stderr)
	}
	var detail sessionapi.Detail
	if err := json.Unmarshal([]byte(jsonShow.stdout), &detail); err != nil {
		t.Fatalf("decode session detail: %v\n%s", err, jsonShow.stdout)
	}
	if detail.Session.NativeSessionID != sessionapitest.ActiveSession {
		t.Errorf("detail session = %q", detail.Session.NativeSessionID)
	}
	if len(detail.Session.Bindings) != 2 {
		t.Errorf("detail bindings = %d, want 2", len(detail.Session.Bindings))
	}

	missing := runCLI(t, "--db", db, "session", "show", sessionapitest.HarnessClaudeCode, "missing-session")
	if missing.exitCode == 0 {
		t.Fatalf("missing session must exit non-zero:\n%s", missing.stdout)
	}
	if !strings.Contains(missing.stderr, "No recorded session") {
		t.Errorf("missing session error is not actionable:\n%s", missing.stderr)
	}

	invalid := runCLI(t, "--db", db, "session", "list", "--state", "bogus")
	if invalid.exitCode != 1 {
		t.Fatalf("invalid state exit = %d, want validation exit 1\n%s", invalid.exitCode, invalid.stderr)
	}

	zeroLimit := runCLI(t, "--db", db, "session", "list", "--limit", "0")
	if zeroLimit.exitCode != 1 {
		t.Fatalf("--limit 0 exit = %d, want validation exit 1\n%s", zeroLimit.exitCode, zeroLimit.stderr)
	}
	if !strings.Contains(zeroLimit.stderr, "outside the supported range") {
		t.Errorf("--limit 0 error is not actionable:\n%s", zeroLimit.stderr)
	}
}

func TestCLI_SessionListFlagsAndCursorRoundTrip(t *testing.T) {
	t.Parallel()

	db := sessionapitest.SeedStore(t)

	first := runCLI(t, "--db", db, "--format", "json", "session", "list",
		"--harness", sessionapitest.HarnessClaudeCode, "--limit", "1")
	if first.exitCode != 0 {
		t.Fatalf("first page exit %d: %s", first.exitCode, first.stderr)
	}
	var firstPage sessionapi.Page
	if err := json.Unmarshal([]byte(first.stdout), &firstPage); err != nil {
		t.Fatalf("decode first page: %v\n%s", err, first.stdout)
	}
	if len(firstPage.Items) != 1 {
		t.Fatalf("first page items = %d, want 1\n%s", len(firstPage.Items), first.stdout)
	}
	if firstPage.Items[0].Harness != sessionapitest.HarnessClaudeCode {
		t.Errorf("first page harness = %q, want claude-code", firstPage.Items[0].Harness)
	}
	if firstPage.Next == "" {
		t.Fatalf("first page must carry a next cursor for the second claude-code session\n%s", first.stdout)
	}

	second := runCLI(t, "--db", db, "--format", "json", "session", "list",
		"--harness", sessionapitest.HarnessClaudeCode, "--limit", "1", "--cursor", firstPage.Next)
	if second.exitCode != 0 {
		t.Fatalf("second page exit %d: %s", second.exitCode, second.stderr)
	}
	var secondPage sessionapi.Page
	if err := json.Unmarshal([]byte(second.stdout), &secondPage); err != nil {
		t.Fatalf("decode second page: %v\n%s", err, second.stdout)
	}
	if len(secondPage.Items) != 1 {
		t.Fatalf("second page items = %d, want 1\n%s", len(secondPage.Items), second.stdout)
	}
	if secondPage.Items[0].Harness != sessionapitest.HarnessClaudeCode {
		t.Errorf("second page harness = %q, want claude-code", secondPage.Items[0].Harness)
	}
	if secondPage.Items[0].NativeSessionID == firstPage.Items[0].NativeSessionID {
		t.Errorf("cursor round-trip repeated %q", secondPage.Items[0].NativeSessionID)
	}
	if secondPage.Next != "" {
		t.Errorf("second page should be the last claude-code page, got next %q", secondPage.Next)
	}

	badCursor := runCLI(t, "--db", db, "session", "list", "--cursor", "not-a-cursor")
	if badCursor.exitCode != 1 {
		t.Fatalf("invalid cursor exit = %d, want validation exit 1\n%s", badCursor.exitCode, badCursor.stderr)
	}
	if !strings.Contains(badCursor.stderr, "cursor") {
		t.Errorf("invalid cursor error is not actionable:\n%s", badCursor.stderr)
	}
}

func TestCLI_SessionReadDoesNotCreateAMissingStore(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "missing", "pasture.db")
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("precondition: %q must not exist: %v", db, err)
	}

	list := runCLI(t, "--db", db, "session", "list")
	if list.exitCode != 2 {
		t.Fatalf("session list exit = %d, want connection exit 2\n%s", list.exitCode, list.stderr)
	}
	if !strings.Contains(list.stderr, "No pasture database exists") {
		t.Errorf("missing-store error is not actionable:\n%s", list.stderr)
	}
	if !strings.Contains(list.stderr, "pasture task") {
		t.Errorf("missing-store error does not name the creation command:\n%s", list.stderr)
	}

	show := runCLI(t, "--db", db, "session", "show", sessionapitest.HarnessClaudeCode, "any")
	if show.exitCode != 2 {
		t.Fatalf("session show exit = %d, want connection exit 2\n%s", show.exitCode, show.stderr)
	}

	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("a session read created the store at %q: %v", db, err)
	}
}

package main_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// newSessionCLIDB builds a fresh unified store through the production opener and
// seeds the bindings and claims the session cache is derived from. The command
// itself runs the journal-aware rebuild, so the CLI tests exercise the same read
// path an operator runs.
func newSessionCLIDB(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close tracker: %v", err)
	}

	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UnixNano()
	minute := int64(time.Minute)
	endedAt := now - 5*minute
	endedReason := "session_end"

	bindings := []struct {
		id        string
		harness   string
		session   string
		epoch     string
		role      string
		boundAt   int64
		unboundAt *int64
		reason    *string
	}{
		{id: "binding-active", harness: "claude-code", session: "active-session", epoch: "file://epoch/one", role: "implementer", boundAt: now - minute},
		{id: "binding-idle", harness: "claude-code", session: "idle-session", epoch: "file://epoch/two", role: "reviewer", boundAt: now - 20*minute},
		{id: "binding-ended", harness: "opencode", session: "ended-session", epoch: "file://epoch/two", role: "worker", boundAt: now - 30*minute, unboundAt: &endedAt, reason: &endedReason},
	}
	for _, b := range bindings {
		if _, err := db.ExecContext(t.Context(), `
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			b.id, b.harness, b.session, b.epoch, b.role, b.boundAt, b.unboundAt, b.reason); err != nil {
			t.Fatalf("seed binding %s: %v", b.id, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
		VALUES ('codex', 'unknown-session', 'file://actor/session-cli', ?)`, now-2*minute); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return path
}

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

	db := newSessionCLIDB(t)

	list := runCLI(t, "--db", db, "session", "list")
	if list.exitCode != 0 {
		t.Fatalf("session list exit %d: %s", list.exitCode, list.stderr)
	}
	for _, want := range []string{"STATE", "active-session", "idle-session", "ended-session", "unknown-session"} {
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
		"active-session":  sessionapi.StateActive,
		"idle-session":    sessionapi.StateIdle,
		"ended-session":   sessionapi.StateEnded,
		"unknown-session": sessionapi.StateUnknown,
	}
	for session, want := range wantStates {
		if states[session] != want {
			t.Errorf("state[%s] = %q, want %q", session, states[session], want)
		}
	}

	show := runCLI(t, "--db", db, "session", "show", "claude-code", "active-session")
	if show.exitCode != 0 {
		t.Fatalf("session show exit %d: %s", show.exitCode, show.stderr)
	}
	for _, want := range []string{"Harness:    claude-code", "Session:    active-session", "State:      active", "file://epoch/one"} {
		if !strings.Contains(show.stdout, want) {
			t.Errorf("detail text is missing %q:\n%s", want, show.stdout)
		}
	}

	jsonShow := runCLI(t, "--db", db, "--format", "json", "session", "show", "claude-code", "active-session")
	if jsonShow.exitCode != 0 {
		t.Fatalf("json session show exit %d: %s", jsonShow.exitCode, jsonShow.stderr)
	}
	var detail sessionapi.Detail
	if err := json.Unmarshal([]byte(jsonShow.stdout), &detail); err != nil {
		t.Fatalf("decode session detail: %v\n%s", err, jsonShow.stdout)
	}
	if detail.Session.NativeSessionID != "active-session" {
		t.Errorf("detail session = %q", detail.Session.NativeSessionID)
	}

	missing := runCLI(t, "--db", db, "session", "show", "claude-code", "missing-session")
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
}

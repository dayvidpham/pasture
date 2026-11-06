package main_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/sessionapi/sessionapitest"
)

func TestCLI_RebuildSessionIsACommand(t *testing.T) {
	t.Parallel()

	help := runCLI(t, "rebuild", "--help")
	if help.exitCode != 0 {
		t.Fatalf("rebuild help exit %d: %s", help.exitCode, help.stderr)
	}
	if got, want := commandNames(help.stdout), []string{"session"}; !sameStrings(got, want) {
		t.Fatalf("rebuild commands = %v, want %v\n%s", got, want, help.stdout)
	}
}

func TestCLI_RebuildSessionFullThenIncremental(t *testing.T) {
	t.Parallel()

	db := sessionapitest.SeedStore(t)

	full := runCLI(t, "--db", db, "rebuild", "session", "--full")
	if full.exitCode != 0 {
		t.Fatalf("rebuild session --full exit %d: %s", full.exitCode, full.stderr)
	}
	if !strings.Contains(full.stdout, "rebuilt:") {
		t.Errorf("full rebuild did not report a rebuild:\n%s", full.stdout)
	}

	// The cache is now current, so the default form applies only a suffix and
	// reports that nothing changed.
	incremental := runCLI(t, "--db", db, "rebuild", "session")
	if incremental.exitCode != 0 {
		t.Fatalf("rebuild session exit %d: %s", incremental.exitCode, incremental.stderr)
	}
	if !strings.Contains(incremental.stdout, "already current") {
		t.Errorf("incremental rebuild did not report a current cache:\n%s", incremental.stdout)
	}
}

func TestCLI_RebuildSessionJSON(t *testing.T) {
	t.Parallel()

	db := sessionapitest.SeedStore(t)
	out := runCLI(t, "--db", db, "--format", "json", "rebuild", "session", "--full")
	if out.exitCode != 0 {
		t.Fatalf("rebuild session --format json exit %d: %s", out.exitCode, out.stderr)
	}
	var result struct {
		Full                      bool  `json:"full"`
		Changed                   bool  `json:"changed"`
		Sessions                  int   `json:"sessions"`
		Revision                  int64 `json:"revision"`
		CompletedThroughJournalID int64 `json:"completed_through_journal_id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.stdout)), &result); err != nil {
		t.Fatalf("decode rebuild result %q: %v", out.stdout, err)
	}
	// The fixture seeds claims and bindings directly, with no journal facts, so
	// the certified watermark is legitimately zero; the publication still holds
	// the projected sessions and a new revision.
	if !result.Full || !result.Changed || result.Sessions == 0 || result.Revision == 0 {
		t.Fatalf("rebuild result = %+v (raw %q), want a full, changed, non-empty publication", result, out.stdout)
	}
}

func TestCLI_RebuildSessionRequiresAnExistingStore(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.db")
	out := runCLI(t, "--db", missing, "rebuild", "session")
	if out.exitCode == 0 {
		t.Fatalf("rebuild session on a missing store exited 0:\n%s", out.stdout)
	}
	if !strings.Contains(out.stderr, "No pasture database exists") {
		t.Errorf("missing-store error is not actionable:\n%s", out.stderr)
	}
}

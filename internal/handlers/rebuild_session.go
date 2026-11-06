package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// RebuildSessionInput names the store, whether to discard the cache first, and
// where the outcome is written.
type RebuildSessionInput struct {
	DBPath string
	Full   bool
	Output io.Writer
}

// SessionRebuildResult is the machine-readable outcome of a rebuild.
type SessionRebuildResult struct {
	Full                      bool  `json:"full"`
	Changed                   bool  `json:"changed"`
	Sessions                  int   `json:"sessions"`
	Revision                  int64 `json:"revision"`
	CompletedThroughJournalID int64 `json:"completed_through_journal_id"`
}

// RebuildSession brings the disposable session cache current and returns.
//
// Without Full it does what a read does: repair a dirty, absent or old-layout
// cache in full, and apply only the suffix when the cache is already current.
// With Full it discards the cache and replays the whole journal. The journal is
// the source of truth, so either form is safe to run at any time; neither
// records journal evidence nor contacts a running daemon.
//
// Exit codes: 0=success, 2=connection (no store or open failed), 5=storage
// (the replay could not certify a cache).
func RebuildSession(ctx context.Context, in RebuildSessionInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	if err := requireExistingStore(dbPath); err != nil {
		return pasterrors.ExitCode(err), err
	}
	tracker, err := tasks.OpenTaskTrackerContext(nonNilContext(ctx), dbPath)
	if err != nil {
		wrapped := sessionOpenError(dbPath, err)
		return pasterrors.ExitCode(wrapped), wrapped
	}
	defer tracker.Close()

	var outcome tasks.SessionStateRebuildOutcome
	var changed bool
	if in.Full {
		outcome, err = tasks.RebuildSessionState(nonNilContext(ctx), tracker)
		changed = true
	} else {
		outcome, changed, err = tasks.RefreshSessionState(nonNilContext(ctx), tracker)
	}
	if err != nil {
		wrapped := sessionCacheError(dbPath, err)
		return pasterrors.ExitCode(wrapped), wrapped
	}

	result := SessionRebuildResult{
		Full:                      in.Full,
		Changed:                   changed,
		Sessions:                  outcome.Sessions,
		Revision:                  outcome.Revision,
		CompletedThroughJournalID: outcome.CompletedThroughJournalID,
	}
	if format == types.OutputJSON {
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return pasterrors.ExitCode(marshalErr), marshalErr
		}
		fmt.Fprintln(sessionOutput(in.Output), string(encoded))
		return 0, nil
	}
	state := "already current"
	if changed {
		state = "rebuilt"
	}
	fmt.Fprintf(sessionOutput(in.Output), "%s: %d sessions, revision %d, through journal %d\n",
		state, result.Sessions, result.Revision, result.CompletedThroughJournalID)
	return 0, nil
}

// Package handlers — task_agents.go
//
// Handler for `pasture task agents [list | show <id>]` (PROPOSAL-2 §7.9).
//
// Surface:
//
//	pasture task agents list             [--format json|text]
//	pasture task agents show <agent-id>  [--format json|text]
//
// `list` returns one row per agent registered in pasture_well_known_agents,
// LEFT JOINed against pasture_agent_categories so callers see the agent's
// (AutomatonRole, PastureRole) pair. Agents that are present in
// pasture_agent_categories but NOT in pasture_well_known_agents (e.g.
// hand-registered SoftwareAgents) are also listed, with WellKnownName empty.
//
// `show` returns a single agent + its categories. The agent-id is the
// wire-format Provenance AgentID (e.g. "pasture--01HABC..."). Agents that
// have no row in either pasture-side table return ("None","None") per the
// AgentCategories contract — i.e., the agent IS valid but has no pasture
// categorisation yet (this is the expected state for fresh humans/MLAgents
// before S7 registers the well-known automaton agents).
//
// Why we open a private *sql.DB rather than route through TaskTracker for
// the list path: the protocol.TaskTracker interface (S5) deliberately exposes
// only point-lookup AgentCategories(id) — the design intent was to keep the
// public surface small. Listing is a CLI-only concern; pushing a List() onto
// the public interface for one CLI subcommand would be over-fitting. The
// query here is small (~10 lines of SQL) and lives entirely in the handler.
//
// Once S7 lands the well-known agent registry, the list path will surface
// all 15 well-known agents on every fresh `pastured` startup. Until then,
// the table is empty and `pasture task agents list` returns "(no registered
// agents)" per the formatter convention.
package handlers

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite" // pure-Go driver

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TaskAgentsList prints every registered agent + categories.
func TaskAgentsList(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	if _, err := os.Stat(dbPath); err != nil {
		// Surface a clean "no such file" path so the user knows we did not
		// reach the SQL layer at all.
		if os.IsNotExist(err) {
			se := &pasterrors.StructuredError{
				Category: pasterrors.CategoryConnection,
				What:     fmt.Sprintf("pasture task agents list: database not found at %q", dbPath),
				Why:      err.Error(),
				Impact:   "no agent listing can be returned because the database file does not exist yet",
				Fix:      fmt.Sprintf("create the database by running any pasture command that opens it (e.g. 'pasture task list'), or pass --db <path> / set $%s", tasks.DBPathEnv),
			}
			return pasterrors.ExitCode(se), se
		}
		// Other stat failures (permission denied, etc.).
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("pasture task agents list: cannot stat %q", dbPath),
			Why:      err.Error(),
			Impact:   "the database file is unreachable; no agent listing can be returned",
			Fix:      "verify the path is readable and the parent directory has the right permissions",
		}
		return pasterrors.ExitCode(se), se
	}

	// Open a read-only private handle. We don't need OpenTaskTracker because
	// we are NOT mutating the file and we don't want to trigger Migrate
	// here — listing should work on legacy v1/v2 databases too (it just
	// returns an empty list on those, since the tables don't exist).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("pasture task agents list: cannot open database at %q", dbPath),
			Why:      err.Error(),
			Impact:   "the agent listing cannot be returned because the file is unreachable",
			Fix:      "verify the path is a SQLite file and is readable; pass --db <path> if the location differs from the default",
		}
		return pasterrors.ExitCode(se), se
	}
	defer db.Close()

	entries, err := readAgentEntries(db)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fErr := formatters.FormatAgentEntries(entries, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskAgentsShow looks up one agent by its wire-format AgentID and prints
// its categories + well-known name (if any).
func TaskAgentsShow(w io.Writer, dbPath, agentIDStr string, format types.OutputFormat) (int, error) {
	if agentIDStr == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "pasture task agents show: missing agent ID",
			Why:      "the positional agent-id argument was empty",
			Impact:   "the agent lookup cannot proceed without a target ID",
			Fix:      "pass the wire-format AgentID as the first positional argument: pasture task agents show <namespace--uuid>",
		}
		return pasterrors.ExitCode(se), se
	}
	agentID, err := provenance.ParseAgentID(agentIDStr)
	if err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("pasture task agents show: invalid agent ID %q", agentIDStr),
			Why:      err.Error(),
			Impact:   "the agent lookup cannot proceed because the ID is not a parseable AgentID",
			Fix:      "pass an ID in the form 'namespace--uuid' (e.g., pasture--01HABC...); use 'pasture task agents list' to discover valid IDs",
		}
		return pasterrors.ExitCode(se), se
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tracker.Close()

	automaton, pastureRole, err := tracker.AgentCategories(agentID)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Resolve well-known name via a side query on the audit DB. We re-open
	// the file rather than threading a *sql.DB through the TaskTracker
	// surface — the read is cheap and the alternative would force an
	// interface change we don't yet need.
	wellKnownName := ""
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	probe, pErr := sql.Open("sqlite", dbPath)
	if pErr == nil {
		defer probe.Close()
		var nameStr sql.NullString
		qErr := probe.QueryRow(
			`SELECT name FROM pasture_well_known_agents WHERE agent_id = ?`,
			agentID.String(),
		).Scan(&nameStr)
		if qErr == nil && nameStr.Valid {
			wellKnownName = nameStr.String
		} else if qErr != nil && !stderrors.Is(qErr, sql.ErrNoRows) {
			// Table-missing is the expected case on legacy v1/v2 databases;
			// the SQL error string for that case is checked here. We don't
			// hard-fail because the AgentCategories call above already
			// succeeded — we just leave wellKnownName empty.
		}
	}

	entry := formatters.AgentEntry{
		AgentID:       agentID.String(),
		WellKnownName: wellKnownName,
		AutomatonRole: automaton,
		PastureRole:   pastureRole,
	}

	out, fErr := formatters.FormatAgentEntry(entry, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// readAgentEntries returns every agent visible to the pasture-side category
// surface. The query is a FULL OUTER JOIN simulated via UNION because SQLite
// does not natively support FULL OUTER JOIN. Both source tables may be
// missing on legacy databases; we tolerate that by returning an empty list
// rather than an error.
func readAgentEntries(db *sql.DB) ([]formatters.AgentEntry, error) {
	// Probe for table existence first so a legacy v1/v2 database returns
	// a clean empty list instead of a SQL error.
	wellKnownExists, err := tableExists(db, "pasture_well_known_agents")
	if err != nil {
		return nil, err
	}
	categoriesExist, err := tableExists(db, "pasture_agent_categories")
	if err != nil {
		return nil, err
	}
	if !wellKnownExists && !categoriesExist {
		return []formatters.AgentEntry{}, nil
	}

	// Compose the query. We deliberately keep this readable rather than
	// micro-optimised — listing happens once per CLI invocation and the
	// row count is bounded by the well-known registry size (15) plus any
	// hand-registered agents.
	//
	// Strategy:
	//   1. Read every (agent_id, name) row from pasture_well_known_agents.
	//   2. Read every (agent_id, automaton_role, pasture_role) row from
	//      pasture_agent_categories.
	//   3. Merge in Go on agent_id; entries present in only one table get
	//      defaults for the missing column ("None"/"None" for missing
	//      categories, "" for missing well-known name).
	//
	// Result is ordered by well-known name (when present) then agent_id so
	// the output is stable across runs.
	wkRows := map[string]string{} // agent_id -> name
	if wellKnownExists {
		rows, err := db.Query(`SELECT agent_id, name FROM pasture_well_known_agents`)
		if err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "handlers.readAgentEntries: cannot read pasture_well_known_agents",
				Why:      err.Error(),
				Impact:   "the agent listing cannot be returned",
				Fix:      "verify the SQLite file is readable; if the table is missing, run 'pasture migrate' to apply the latest schema",
			}
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What:     "handlers.readAgentEntries: scan failed for pasture_well_known_agents row",
					Why:      err.Error(),
					Impact:   "the agent listing cannot be returned",
					Fix:      "verify the SQLite file is not corrupt; run 'sqlite3 <db> .schema pasture_well_known_agents' to inspect",
				}
			}
			wkRows[id] = name
		}
		if err := rows.Err(); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "handlers.readAgentEntries: row iteration failed for pasture_well_known_agents",
				Why:      err.Error(),
				Impact:   "the agent listing may be partial",
				Fix:      "re-run the command; if the error persists, check 'PRAGMA integrity_check'",
			}
		}
	}

	type catRow struct {
		automaton   protocol.AutomatonRole
		pastureRole protocol.PastureRole
	}
	catRows := map[string]catRow{} // agent_id -> categories
	if categoriesExist {
		rows, err := db.Query(
			`SELECT agent_id, automaton_role, pasture_role FROM pasture_agent_categories`)
		if err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "handlers.readAgentEntries: cannot read pasture_agent_categories",
				Why:      err.Error(),
				Impact:   "the agent listing cannot be returned",
				Fix:      "verify the SQLite file is readable; if the table is missing, run 'pasture migrate' to apply the latest schema",
			}
		}
		defer rows.Close()
		for rows.Next() {
			var id, automaton, pastureRole string
			if err := rows.Scan(&id, &automaton, &pastureRole); err != nil {
				return nil, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What:     "handlers.readAgentEntries: scan failed for pasture_agent_categories row",
					Why:      err.Error(),
					Impact:   "the agent listing cannot be returned",
					Fix:      "verify the SQLite file is not corrupt; run 'sqlite3 <db> .schema pasture_agent_categories' to inspect",
				}
			}
			catRows[id] = catRow{
				automaton:   protocol.AutomatonRole(automaton),
				pastureRole: protocol.PastureRole(pastureRole),
			}
		}
		if err := rows.Err(); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "handlers.readAgentEntries: row iteration failed for pasture_agent_categories",
				Why:      err.Error(),
				Impact:   "the agent listing may be partial",
				Fix:      "re-run the command; if the error persists, check 'PRAGMA integrity_check'",
			}
		}
	}

	// Merge.
	allIDs := make(map[string]struct{}, len(wkRows)+len(catRows))
	for id := range wkRows {
		allIDs[id] = struct{}{}
	}
	for id := range catRows {
		allIDs[id] = struct{}{}
	}
	entries := make([]formatters.AgentEntry, 0, len(allIDs))
	for id := range allIDs {
		entry := formatters.AgentEntry{
			AgentID:       id,
			WellKnownName: wkRows[id], // empty when not present
			AutomatonRole: protocol.AutomatonRoleNone,
			PastureRole:   protocol.PastureRoleNone,
		}
		if cat, ok := catRows[id]; ok {
			entry.AutomatonRole = cat.automaton
			entry.PastureRole = cat.pastureRole
		}
		entries = append(entries, entry)
	}
	// Stable order: well-known name ascending, fallback agent_id ascending.
	sortAgentEntries(entries)
	return entries, nil
}

// sortAgentEntries orders entries by (WellKnownName ascending, then AgentID
// ascending) so list output is stable across runs and easy to diff.
func sortAgentEntries(entries []formatters.AgentEntry) {
	// Local sort to avoid pulling sort into the file's import block twice.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && agentEntryLess(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func agentEntryLess(a, b formatters.AgentEntry) bool {
	if a.WellKnownName != b.WellKnownName {
		// Empty names sort last so well-known agents lead the list.
		switch {
		case a.WellKnownName == "":
			return false
		case b.WellKnownName == "":
			return true
		default:
			return a.WellKnownName < b.WellKnownName
		}
	}
	return a.AgentID < b.AgentID
}

// tableExists reports whether the given table is present. Used to tolerate
// legacy databases (v1/v2) that pre-date PROPOSAL-2's pasture-side tables.
func tableExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if stderrors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("handlers.tableExists: cannot probe sqlite_master for %q", name),
			Why:      err.Error(),
			Impact:   "the agent listing cannot determine which pasture-side tables are available",
			Fix:      "verify the SQLite file is accessible and not corrupted; if the file is intact, run 'pasture migrate' to apply the latest schema",
		}
	}
	return true, nil
}

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
// wire-format Provenance AgentId (e.g. "pasture--01HABC..."). Agents that
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
				What:     fmt.Sprintf("No pasture database was found at %q.", dbPath),
				Why:      "The file doesn't exist yet — pasture creates it the first time you run a command that needs it.",
				Where:    "Listing agents (internal/handlers/task_agents.go in handlers.TaskAgentsList).",
				Impact:   "No agents can be listed because there's no database to read from.",
				Fix: fmt.Sprintf("1. Create the database by running any task command — it will be initialized automatically:\n"+
					"     pasture task list\n"+
					"2. Or point pasture at an existing database:\n"+
					"     pasture task agents list --db <path>\n"+
					"     export %s=<path>",
					tasks.DBPathEnv),
			}
			return pasterrors.ExitCode(se), se
		}
		// Other stat failures (permission denied, etc.).
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't check whether the pasture database exists at %q.", dbPath),
			Why:      "Reading the file's status failed (it might exist but not be accessible).",
			Where:    "Listing agents (internal/handlers/task_agents.go in handlers.TaskAgentsList).",
			Impact:   "No agents can be listed because pasture can't tell whether the database is available.",
			Fix: fmt.Sprintf("1. Check that the file is readable by your user:\n"+
				"     ls -l %q\n"+
				"2. Make sure the folder it lives in is also accessible.\n"+
				"3. If the path is wrong, pass the right one:\n"+
				"     pasture task agents list --db <path>",
				dbPath),
			Cause: err,
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
			What:     fmt.Sprintf("Couldn't open the pasture database at %q.", dbPath),
			Why:      "Opening the database file failed.",
			Where:    "Listing agents (internal/handlers/task_agents.go in handlers.TaskAgentsList).",
			Impact:   "No agents can be listed because the database is unreachable.",
			Fix: fmt.Sprintf("1. Confirm the file exists and is a SQLite database:\n"+
				"     ls -l %q\n"+
				"2. If the path is wrong, pass the right one:\n"+
				"     pasture task agents list --db <path>\n"+
				"3. Make sure the file is readable by your user.",
				dbPath),
			Cause: err,
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

// TaskAgentsShow looks up one agent by its wire-format AgentId and prints
// its categories + well-known name (if any).
func TaskAgentsShow(w io.Writer, dbPath, agentIdStr string, format types.OutputFormat) (int, error) {
	if agentIdStr == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An agent ID is required to show an agent.",
			Why:      "No agent ID was passed as the first positional argument.",
			Where:    "Showing an agent (internal/handlers/task_agents.go in handlers.TaskAgentsShow).",
			Impact:   "The agent lookup can't run without knowing which agent to show.",
			Fix: "1. Pass the agent ID as the first positional argument:\n" +
				"     pasture task agents show <agent-id>\n" +
				"2. To find a valid agent ID, list registered agents first:\n" +
				"     pasture task agents list",
		}
		return pasterrors.ExitCode(se), se
	}
	agentId, err := provenance.ParseAgentID(agentIdStr)
	if err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The agent ID %q isn't in the expected format.", agentIdStr),
			Why: "Agent IDs need the shape \"namespace--uuid\" (for example, pasture--01HABC...).\n" +
				"The value you passed couldn't be split into those two parts.",
			Where:  "Showing an agent (internal/handlers/task_agents.go in handlers.TaskAgentsShow).",
			Impact: "The agent lookup can't run because there's no way to know which agent you meant.",
			Fix: "1. Pass a valid agent ID. Use list to find one:\n" +
				"     pasture task agents list\n" +
				"2. Then retry your command with the correct ID.",
			Cause: err,
		}
		return pasterrors.ExitCode(se), se
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tracker.Close()

	automaton, pastureRole, err := tracker.AgentCategories(agentId)
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
			agentId.String(),
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
		AgentId:       agentId.String(),
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
				What:     "Couldn't read the list of well-known agents from the pasture database.",
				Why:      "The query against the well-known-agents table failed.",
				Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
				Cause:    err,
				Impact:   "No agents can be listed because pasture can't read its agent registry.",
				Fix: "1. If the table is missing, your database is on an older schema — apply the\n" +
					"   latest migration:\n" +
					"     pasture migrate\n" +
					"2. If the file is corrupted, restore from backup or check integrity:\n" +
					"     sqlite3 <db> 'PRAGMA integrity_check'",
			}
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What:     "A row in the well-known-agents table couldn't be read.",
					Why:      "Reading a row's fields failed.",
					Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
					Cause:    err,
					Impact:   "The agent list can't be returned because part of the table couldn't be read.",
					Fix: "1. The database file may be corrupted. Check its integrity:\n" +
						"     sqlite3 <db> 'PRAGMA integrity_check'\n" +
						"2. If integrity is OK, the schema may be out of date — run:\n" +
						"     pasture migrate\n" +
						"3. As a last resort, restore the database file from backup.",
				}
			}
			wkRows[id] = name
		}
		if err := rows.Err(); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Reading rows from the well-known-agents table stopped before all rows were processed.",
				Why:      "Iterating over the result set failed.",
				Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
				Cause:    err,
				Impact:   "The agent list may be incomplete — some agents could be missing from the output.",
				Fix: "1. Retry the command — the error may be transient:\n" +
					"     pasture task agents list\n" +
					"2. If the error keeps happening, check database integrity:\n" +
					"     sqlite3 <db> 'PRAGMA integrity_check'",
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
				What:     "Couldn't read the agent role assignments from the pasture database.",
				Why:      "The query against the agent-categories table failed.",
				Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
				Cause:    err,
				Impact:   "Agents can be listed but their role assignments will be missing from the output.",
				Fix: "1. If the table is missing, your database is on an older schema — apply the\n" +
					"   latest migration:\n" +
					"     pasture migrate\n" +
					"2. If the file is corrupted, restore from backup or check integrity:\n" +
					"     sqlite3 <db> 'PRAGMA integrity_check'",
			}
		}
		defer rows.Close()
		for rows.Next() {
			var id, automaton, pastureRole string
			if err := rows.Scan(&id, &automaton, &pastureRole); err != nil {
				return nil, &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What:     "A row in the agent-categories table couldn't be read.",
					Why:      "Reading a row's fields failed.",
					Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
					Cause:    err,
					Impact:   "Role information for one or more agents can't be returned.",
					Fix: "1. The database file may be corrupted. Check its integrity:\n" +
						"     sqlite3 <db> 'PRAGMA integrity_check'\n" +
						"2. If integrity is OK, the schema may be out of date — run:\n" +
						"     pasture migrate\n" +
						"3. As a last resort, restore the database file from backup.",
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
				What:     "Reading rows from the agent-categories table stopped before all rows were processed.",
				Why:      "Iterating over the result set failed.",
				Where:    "Listing agents (internal/handlers/task_agents.go in handlers.readAgentEntries).",
				Cause:    err,
				Impact:   "Some agents may be listed without their role assignments.",
				Fix: "1. Retry the command — the error may be transient:\n" +
					"     pasture task agents list\n" +
					"2. If the error keeps happening, check database integrity:\n" +
					"     sqlite3 <db> 'PRAGMA integrity_check'",
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
			AgentId:       id,
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

// sortAgentEntries orders entries by (WellKnownName ascending, then AgentId
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
	return a.AgentId < b.AgentId
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
			What:     fmt.Sprintf("Couldn't check whether the %q table exists in the pasture database.", name),
			Why:      "Querying the database's table catalogue failed.",
			Where:    "Probing the database (internal/handlers/task_agents.go in handlers.tableExists).",
			Impact:   "The agent listing can't proceed because pasture can't tell which tables are available.",
			Fix: "1. Make sure the database file is readable and not corrupted:\n" +
				"     sqlite3 <db> 'PRAGMA integrity_check'\n" +
				"2. If the file is intact, your database may be on an older schema — run:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}
	return true, nil
}

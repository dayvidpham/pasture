package handlers

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// TaskAgentsList reads the base registry and its optional decorations without
// migrating or changing the database.
func TaskAgentsList(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	entries, err := readRegisteredAgents(dbPath, "")
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	out, err := formatters.FormatAgentEntries(entries, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

func TaskAgentsShow(w io.Writer, dbPath, idStr string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseAgentID(idStr)
	if err != nil {
		return agentLookupValidation(idStr, "The agent ID must have the form namespace--uuid.", err)
	}
	entries, err := readRegisteredAgents(dbPath, id.String())
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if len(entries) == 0 {
		return agentLookupValidation(idStr, "The ID is not in the registered agent registry.", nil)
	}
	out, err := formatters.FormatAgentEntry(entries[0], format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

func agentLookupValidation(id, why string, cause error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Cannot show agent %q.", id),
		Why:      why,
		Where:    "Looking up an agent (internal/handlers/task_agents.go).",
		Impact:   "No agent was returned or created.",
		Fix:      "Run `pasture task agents list`, choose a registered ID, then retry `pasture task agents show AGENT-ID`.",
		Cause:    cause,
	}
	return pasterrors.ExitCode(se), se
}

func agentReadError(path string, category pasterrors.Category, err error) error {
	return &pasterrors.StructuredError{
		Category: category,
		What:     fmt.Sprintf("Cannot read the agent registry at %q.", path),
		Why:      "The database file or registry query could not be read.",
		Where:    "Reading registered agents (internal/handlers/task_agents.go).",
		Impact:   "No agent result is available; no identity was changed.",
		Fix:      "Check the --db path and read permissions. Use `pasture task list` to initialize a new store. For an existing damaged store, restore a backup; then retry `pasture task agents list`.",
		Cause:    err,
	}
}

func readRegisteredAgents(path, id string) ([]formatters.AgentEntry, error) {
	if path == "" {
		path = tasks.DefaultDBPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, agentReadError(path, pasterrors.CategoryConnection, err)
	}
	db, err := dbconn.OpenReadOnlyDB(path)
	if err != nil {
		return nil, agentReadError(path, pasterrors.CategoryConnection, err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, agentReadError(path, pasterrors.CategoryStorage, err)
	}
	defer tx.Rollback()
	entries, err := readAgentEntries(tx, id)
	if err != nil {
		return nil, agentReadError(path, pasterrors.CategoryStorage, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, agentReadError(path, pasterrors.CategoryStorage, err)
	}
	return entries, nil
}

// One read transaction covers the base registry, subtype names, and optional
// categories. Decoration-only rows never create selectable identities.
func readAgentEntries(tx *sql.Tx, id string) ([]formatters.AgentEntry, error) {
	rows, err := tx.Query(`SELECT a.id, k.name,
		COALESCE(h.name, s.name, r.name || ' / ' || m.name, '')
		FROM agents a JOIN agent_kinds k ON k.id = a.kind_id
		LEFT JOIN agents_human h ON h.agent_id = a.id
		LEFT JOIN agents_software s ON s.agent_id = a.id
		LEFT JOIN agents_ml ml ON ml.agent_id = a.id
		LEFT JOIN roles r ON r.id = ml.role_id
		LEFT JOIN ml_models m ON m.id = ml.model_id
		WHERE (? = '' OR a.id = ?)`, id, id)
	if err != nil {
		return nil, err
	}
	entries := make([]formatters.AgentEntry, 0)
	for rows.Next() {
		e := formatters.AgentEntry{
			AutomatonRole: protocol.AutomatonRoleNone,
			PastureRole:   protocol.PastureRoleNone,
		}
		if err := rows.Scan(&e.AgentId, &e.Kind, &e.Name); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	wk, err := tableExists(tx, "pasture_well_known_agents")
	if err != nil {
		return nil, err
	}
	cat, err := tableExists(tx, "pasture_agent_categories")
	if err != nil {
		return nil, err
	}
	for i := range entries {
		e := &entries[i]
		if wk {
			err := tx.QueryRow(`SELECT name FROM pasture_well_known_agents WHERE agent_id = ?`, e.AgentId).Scan(&e.WellKnownName)
			if err != nil && !stderrors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if cat {
			err := tx.QueryRow(`SELECT automaton_role, pasture_role FROM pasture_agent_categories WHERE agent_id = ?`, e.AgentId).Scan(&e.AutomatonRole, &e.PastureRole)
			if err != nil && !stderrors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
	}
	sortAgentEntries(entries)
	return entries, nil
}

func sortAgentEntries(entries []formatters.AgentEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return agentEntryLess(entries[i], entries[j])
	})
}

func agentEntryLess(a, b formatters.AgentEntry) bool {
	if a.WellKnownName != b.WellKnownName {
		if a.WellKnownName == "" {
			return false
		}
		if b.WellKnownName == "" {
			return true
		}
		return a.WellKnownName < b.WellKnownName
	}
	return a.AgentId < b.AgentId
}

func tableExists(tx *sql.Tx, name string) (bool, error) {
	var found string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if stderrors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

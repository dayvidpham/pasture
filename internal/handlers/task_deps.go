package handlers

import (
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// TaskReady prints the set of tasks that are open and have no open blockers.
func TaskReady(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	ts, err := tr.Ready()
	if err != nil {
		return wrapTaskOpError("ready", err)
	}
	out, fErr := formatters.FormatTasks(ts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskBlocked prints the set of tasks that are open but have at least one open blocker.
func TaskBlocked(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	ts, err := tr.Blocked()
	if err != nil {
		return wrapTaskOpError("blocked", err)
	}
	out, fErr := formatters.FormatTasks(ts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskDepAdd creates an edge sourceID --kind--> targetID. The default kind is
// EdgeBlockedBy, which is what `--blocked-by` on the CLI maps to. Other edge
// kinds can be specified by passing the wire-format string for the kind.
//
// Convention: `pasture task dep add A --blocked-by B` means "A is blocked by
// B" — A cannot proceed until B closes. This matches the bd convention.
func TaskDepAdd(w io.Writer, dbPath, sourceIDStr, targetIDStr string, kind provenance.EdgeKind, format types.OutputFormat) (int, error) {
	sourceID, err := provenance.ParseTaskID(sourceIDStr)
	if err != nil {
		return wrapInvalidID("task dep add (source)", sourceIDStr, err)
	}

	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	if err := tr.AddEdge(sourceID, targetIDStr, kind); err != nil {
		return wrapTaskOpError("dep add", err)
	}

	switch format {
	case types.OutputJSON:
		fmt.Fprintf(w, `{"sourceId":%q,"targetId":%q,"kind":%q}`+"\n", sourceID.String(), targetIDStr, kind.String())
	default:
		fmt.Fprintf(w, "added edge: %s --[%s]--> %s\n", sourceID.String(), kind, targetIDStr)
	}
	return 0, nil
}

// TaskDepTree prints the blocked-by tree rooted at the given task in DFS order.
func TaskDepTree(w io.Writer, dbPath, idStr string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task dep tree", idStr, err)
	}

	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	edges, err := tr.DepTree(id)
	if err != nil {
		return wrapTaskOpError("dep tree", err)
	}
	out, fErr := formatters.FormatDepTree(idStr, edges, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

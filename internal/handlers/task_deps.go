package handlers

import (
	"fmt"
	"io"
	"sort"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// TaskReady prints non-closed tasks that have no non-closed stored blockers.
func TaskReady(w io.Writer, dbPath string, format types.OutputFormat, label string) (int, error) {
	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	ts, err := tr.Ready()
	if err != nil {
		return wrapTaskOpError("ready", err)
	}
	ts, err = filterReadinessLabels(tr, ts, label)
	if err != nil {
		return wrapTaskOpError("ready labels", err)
	}
	sortTasks(ts, true)
	out, fErr := formatters.FormatTasks(ts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskBlocked prints non-closed tasks with at least one non-closed stored blocker.
func TaskBlocked(w io.Writer, dbPath string, format types.OutputFormat, label string) (int, error) {
	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	ts, err := tr.Blocked()
	if err != nil {
		return wrapTaskOpError("blocked", err)
	}
	ts, err = filterReadinessLabels(tr, ts, label)
	if err != nil {
		return wrapTaskOpError("blocked labels", err)
	}
	sortTasks(ts, true)
	out, fErr := formatters.FormatTasks(ts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskDepAdd creates an edge sourceId --kind--> targetId. The default kind is
// EdgeBlockedBy, which is what `--blocked-by` on the CLI maps to. Other edge
// kinds can be specified by passing the wire-format string for the kind.
//
// Convention: `pasture task dep add A --blocked-by B` means "A is blocked by
// B" — A cannot proceed until B closes.
func TaskDepAdd(w io.Writer, dbPath, sourceIdStr, targetIdStr string, kind provenance.EdgeKind, format types.OutputFormat) (int, error) {
	sourceId, err := provenance.ParseTaskID(sourceIdStr)
	if err != nil {
		return wrapInvalidId("task dep add (source)", sourceIdStr, err)
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	if err := tr.AddEdge(sourceId, targetIdStr, kind); err != nil {
		return wrapTaskOpError("dep add", err)
	}

	out, fErr := formatters.FormatEdge(provenance.Edge{
		SourceID: sourceId.String(),
		TargetID: targetIdStr,
		Kind:     kind,
	}, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskDepTree prints the outgoing relation tree rooted at the given task in
// DFS order, following the selected kinds.
//
// The blocked_by-only selection keeps the legacy path (one DepTree query and
// FormatDepTree) so its bytes never change. Every other selection — a single
// non-blocked_by kind or all kinds — walks the store through collectTypedTree
// and renders through FormatTypedTree, which marks repeats.
func TaskDepTree(w io.Writer, dbPath, idStr string, kinds []provenance.EdgeKind, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidId("task dep tree", idStr, err)
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	if _, err := tr.Show(id); err != nil {
		return wrapTaskOpError("dep tree root", err)
	}

	selection := normalizeTreeKinds(kinds)
	var out string
	if isBlockedByOnly(selection) {
		edges, err := tr.DepTree(id)
		if err != nil {
			return wrapTaskOpError("dep tree", err)
		}
		out, err = formatters.FormatDepTree(idStr, edges, format)
		if err != nil {
			return pasterrors.ExitCode(err), err
		}
	} else {
		edges, err := collectTypedTree(idStr, selection, func(source provenance.TaskID, kind provenance.EdgeKind) ([]provenance.Edge, error) {
			return tr.Edges(source, &kind)
		})
		if err != nil {
			return wrapTaskOpError("dep tree", err)
		}
		out, err = formatters.FormatTypedTree(idStr, edges, selection, format)
		if err != nil {
			return pasterrors.ExitCode(err), err
		}
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// Readiness is computed by the tracker before the label filter is applied.
func filterReadinessLabels(tr provenance.Tracker, ts []provenance.Task, label string) ([]provenance.Task, error) {
	if label == "" {
		return ts, nil
	}
	out := make([]provenance.Task, 0, len(ts))
	for _, task := range ts {
		labels, err := tr.Labels(task.ID)
		if err != nil {
			return nil, err
		}
		for _, candidate := range labels {
			if candidate == label {
				out = append(out, task)
				break
			}
		}
	}
	return out, nil
}

func sortTasks(ts []provenance.Task, priority bool) {
	sort.Slice(ts, func(i, j int) bool {
		a, b := ts[i], ts[j]
		if priority && a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID.String() < b.ID.String()
	})
}

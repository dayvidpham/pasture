// Package tasks — composed task-centric timeline and assignment-episode projections
// (#43 / S3.3 stage d).
//
// timeline_projection.go holds two DETERMINISTIC, pure read projections:
//
//  1. ComposeTimeline folds a task's raw journal task-events and Activity intervals into
//     one composed, task-centric timeline ordered by the frozen (RecordedAt, EventID)
//     keys. It preserves every source EventID, represents point/open/closed Activity
//     intervals as-is (overlapping intervals are allowed and never merged), and emits each
//     source event exactly once (no duplicate rows). JournalID (the source EventID) is the
//     total-order tiebreak, so equal-RecordedAt / concurrent / backdated rows neither
//     duplicate nor drop within a traversal.
//
//  2. ProjectAssignments folds an ordered sequence of assignment start/end transitions into
//     the current owner/supervisor/reviewer episodes and the derived Task.Owner. Owner is
//     the occupant of the active owner-responsibility episode — responsibility transfer is
//     the atomic authority for Task.Owner. Actors are plain claim/range ActorIDs, so the
//     projection needs no seeded ordinal-zero actor.
//
// Both are pure functions over typed inputs, so they are exhaustively testable without a
// journal or the pending ordinal-zero seed.

package tasks

import (
	"sort"

	"github.com/dayvidpham/provenance"
)

// TimelineEntryKind classifies a composed timeline entry.
type TimelineEntryKind int

const (
	// TimelineEvent is a point task-event (no activity interval).
	TimelineEvent TimelineEntryKind = iota
	// TimelineActivity is an Activity interval (point/open/closed).
	TimelineActivity
)

// ActivityInterval is a task-linked Activity's duration. StartAt is the open stamp; EndAt
// is nil for a still-open interval and equal to StartAt for a point interval.
type ActivityInterval struct {
	ActivityID provenance.ActivityID
	StartAt    int64
	EndAt      *int64
}

// IsPoint reports a zero-duration interval (start == end).
func (i ActivityInterval) IsPoint() bool { return i.EndAt != nil && *i.EndAt == i.StartAt }

// IsOpen reports an interval with no recorded end.
func (i ActivityInterval) IsOpen() bool { return i.EndAt == nil }

// TimelineSource is one raw journal task-event, optionally carrying an Activity interval.
type TimelineSource struct {
	EventID    provenance.JournalID
	Task       provenance.TaskID
	RecordedAt int64
	Kind       string
	Activity   *ActivityInterval
}

// TimelineEntry is one composed timeline row. EventID is the preserved source id; Interval
// is set only for activity entries.
type TimelineEntry struct {
	EventID    provenance.JournalID
	Task       provenance.TaskID
	RecordedAt int64
	Kind       string
	EntryKind  TimelineEntryKind
	Interval   *ActivityInterval
}

// ComposeTimeline composes sources into the deterministic (RecordedAt, EventID) timeline,
// de-duplicating by EventID (a source event appears once) and preserving each source
// EventID. The input slice is never mutated. Overlapping activity intervals are preserved
// unchanged; they are not merged or split.
func ComposeTimeline(sources []TimelineSource) []TimelineEntry {
	// De-duplicate by EventID, keeping the first occurrence, so a source event that is
	// listed twice within a traversal never produces two timeline rows.
	seen := make(map[provenance.JournalID]struct{}, len(sources))
	entries := make([]TimelineEntry, 0, len(sources))
	for _, s := range sources {
		if _, dup := seen[s.EventID]; dup {
			continue
		}
		seen[s.EventID] = struct{}{}
		entry := TimelineEntry{
			EventID:    s.EventID,
			Task:       s.Task,
			RecordedAt: s.RecordedAt,
			Kind:       s.Kind,
			EntryKind:  TimelineEvent,
		}
		if s.Activity != nil {
			interval := *s.Activity
			entry.EntryKind = TimelineActivity
			entry.Interval = &interval
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].RecordedAt != entries[b].RecordedAt {
			return entries[a].RecordedAt < entries[b].RecordedAt
		}
		// JournalID (source EventID) is the canonical total-order tiebreak.
		return entries[a].EventID < entries[b].EventID
	})
	return entries
}

// AssignmentTransitionKind is the closed kind of an assignment episode transition.
type AssignmentTransitionKind int

const (
	assignmentTransitionInvalid AssignmentTransitionKind = iota
	// AssignmentStarted opens an episode.
	AssignmentStarted
	// AssignmentEnded closes an episode.
	AssignmentEnded
)

// AssignmentTransition is one ordered assignment start/end the projection folds. Ordinal is
// the journal order (JournalID); the projection applies transitions in ascending Ordinal.
type AssignmentTransition struct {
	Assignment  provenance.AssignmentID
	Task        provenance.TaskID
	Role        AssignmentRole
	Occupant    provenance.ActorID
	Predecessor provenance.AssignmentID
	Parent      provenance.AssignmentID
	Kind        AssignmentTransitionKind
	Ordinal     provenance.JournalID
}

// AssignmentEpisode is one projected assignment episode.
type AssignmentEpisode struct {
	ID          provenance.AssignmentID
	Task        provenance.TaskID
	Role        AssignmentRole
	Occupant    provenance.ActorID
	Predecessor provenance.AssignmentID
	Parent      provenance.AssignmentID
	Active      bool
}

// AssignmentProjection is the folded assignment state: every episode by id, and the derived
// current owner per task (the active owner-responsibility occupant).
type AssignmentProjection struct {
	Episodes    map[provenance.AssignmentID]AssignmentEpisode
	OwnerByTask map[provenance.TaskID]provenance.ActorID
}

// OwnerOf returns the current owner of task and whether one is set.
func (p AssignmentProjection) OwnerOf(task provenance.TaskID) (provenance.ActorID, bool) {
	owner, ok := p.OwnerByTask[task]
	return owner, ok
}

// ProjectAssignments folds transitions (applied in ascending Ordinal) into the current
// episodes and derived owner. A Started transition opens/re-activates an episode; an Ended
// transition deactivates it. Task.Owner is derived last-writer-wins from the active
// owner-responsibility episodes, so a responsibility transfer (end predecessor, start
// successor) atomically moves the owner. The input is never mutated.
func ProjectAssignments(transitions []AssignmentTransition) AssignmentProjection {
	ordered := make([]AssignmentTransition, len(transitions))
	copy(ordered, transitions)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].Ordinal < ordered[b].Ordinal })

	proj := AssignmentProjection{
		Episodes:    make(map[provenance.AssignmentID]AssignmentEpisode),
		OwnerByTask: make(map[provenance.TaskID]provenance.ActorID),
	}
	for _, tr := range ordered {
		switch tr.Kind {
		case AssignmentStarted:
			proj.Episodes[tr.Assignment] = AssignmentEpisode{
				ID:          tr.Assignment,
				Task:        tr.Task,
				Role:        tr.Role,
				Occupant:    tr.Occupant,
				Predecessor: tr.Predecessor,
				Parent:      tr.Parent,
				Active:      true,
			}
		case AssignmentEnded:
			if ep, ok := proj.Episodes[tr.Assignment]; ok {
				ep.Active = false
				proj.Episodes[tr.Assignment] = ep
			}
		}
	}
	// Derive Task.Owner from the active owner-responsibility episodes. When a task has
	// several such episodes across time, the highest-Ordinal active one wins; we recompute
	// deterministically by scanning transitions in order.
	activeOwner := make(map[provenance.TaskID]provenance.AssignmentID)
	for _, tr := range ordered {
		if tr.Role != RoleOwnerResponsibility {
			continue
		}
		switch tr.Kind {
		case AssignmentStarted:
			activeOwner[tr.Task] = tr.Assignment
		case AssignmentEnded:
			if activeOwner[tr.Task] == tr.Assignment {
				delete(activeOwner, tr.Task)
			}
		}
	}
	for task, assignment := range activeOwner {
		if ep, ok := proj.Episodes[assignment]; ok && ep.Active {
			proj.OwnerByTask[task] = ep.Occupant
		}
	}
	return proj
}

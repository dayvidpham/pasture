package handlers

import (
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestTaskAndCommentOrderingBreaksTimestampTies(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	a := provenance.Task{
		ID:        provenance.TaskID{Namespace: "a"},
		CreatedAt: now,
		Priority:  provenance.PriorityHigh,
	}
	b := provenance.Task{
		ID:        provenance.TaskID{Namespace: "b"},
		CreatedAt: now,
		Priority:  provenance.PriorityCritical,
	}
	c := provenance.Task{
		ID:        provenance.TaskID{Namespace: "c"},
		CreatedAt: now.Add(-time.Second),
		Priority:  provenance.PriorityHigh,
	}

	ts := []provenance.Task{b, a, c}
	sortTasks(ts, false)
	require.Equal(t, []provenance.Task{c, a, b}, ts)
	sortTasks(ts, true)
	require.Equal(t, []provenance.Task{b, c, a}, ts)

	first := provenance.Comment{
		ID:        provenance.CommentID{Namespace: "a"},
		CreatedAt: now,
	}
	second := provenance.Comment{
		ID:        provenance.CommentID{Namespace: "b"},
		CreatedAt: now,
	}
	early := provenance.Comment{
		ID:        provenance.CommentID{Namespace: "z"},
		CreatedAt: now.Add(-time.Second),
	}

	cs := []provenance.Comment{second, first, early}
	sortComments(cs)
	require.Equal(t, []provenance.Comment{early, first, second}, cs)
}

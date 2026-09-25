package tasks

import (
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// This file holds the shared test fixtures that the retired assignment-recovery
// probes declared. The probe file that owned them is gone; these survive because
// tests in this package still use them.

// feasibilityActor registers one human agent for a fixture and returns its actor
// id.
func feasibilityActor(t *testing.T, tracker *trackerImpl, handle string) provenance.ActorID {
	t.Helper()
	actor, err := tracker.RegisterHumanAgent(handle, "Feasibility Probe", handle+"@example.test")
	if err != nil {
		t.Fatalf("register actor %q: %v", handle, err)
	}
	return actor.ID
}

// feasibilityEpisode is one seeded assignment episode plus the two journal ids
// a read needs: the authority row and the material start event.
type feasibilityEpisode struct {
	task      provenance.TaskID
	occupant  provenance.ActorID
	authority provenance.JournalID
	event     provenance.JournalID
}

const (
	feasibilityAuthoritySlot provenance.ResultSlotID = "authority"
	feasibilityEventSlot     provenance.ResultSlotID = "event"
)

// seedFeasibilityEpisode commits one assignment episode through a single journal
// Apply and returns the two ids from the Apply RESULT. The effect shape mirrors
// the one an assignment command writes: an EffectAssignmentStart carrying a
// caller-named result slot, plus the material FamilyAssignmentStarted task event
// carrying its own.
func seedFeasibilityEpisode(t *testing.T, tracker *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, occupant provenance.ActorID, operation provenance.OperationID) feasibilityEpisode {
	t.Helper()
	_, systemAuthority, found, err := readSystemIdentity(tracker.auditDB)
	require.NoError(t, err)
	require.True(t, found)
	event, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: assignment, Role: RoleOwnerResponsibility, Occupant: occupant})
	require.NoError(t, err)
	event.ResultSlot = feasibilityEventSlot
	result, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &systemAuthority,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectAssignmentStart, ResultSlot: feasibilityAuthoritySlot, TaskID: task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: occupant},
			event,
		},
	})
	require.NoError(t, err)
	episode := feasibilityEpisode{task: task, occupant: occupant}
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case feasibilityAuthoritySlot:
			episode.authority = slot.ProducedJournalID
		case feasibilityEventSlot:
			episode.event = slot.ProducedJournalID
		}
	}
	require.NotZero(t, episode.authority, "the Apply result bound no authority slot: %+v", result.ResultSlots)
	require.NotZero(t, episode.event, "the Apply result bound no material-event slot: %+v", result.ResultSlots)
	return episode
}

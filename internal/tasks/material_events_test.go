package tasks

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"
)

// mkTask builds a valid namespaced task id for material-event tests.
func mkTask(t *testing.T) provenance.TaskID {
	t.Helper()
	return provenance.TaskID{Namespace: "test", UUID: uuid.Must(uuid.NewV7())}
}

func mkActor(t *testing.T) provenance.ActorID {
	t.Helper()
	return provenance.ActorID{Namespace: "test", UUID: uuid.Must(uuid.NewV7())}
}

func mkActivity(t *testing.T) provenance.ActivityID {
	t.Helper()
	return provenance.ActivityID{Namespace: "test", UUID: uuid.Must(uuid.NewV7())}
}

// validEventFor returns a fully-valid material event for every family. It is the
// exhaustiveness driver: if a new family is added to materialEventFamilies but not
// handled here, TestEveryMaterialEventFamilyMaps fails on the unmapped default.
func validEventFor(t *testing.T, f MaterialEventFamily) MaterialEvent {
	t.Helper()
	task := mkTask(t)
	switch f {
	case FamilyAssignmentStarted:
		return AssignmentStartedEvent{Task: task, Assignment: "A1", Role: RoleOwnerResponsibility, Occupant: mkActor(t)}
	case FamilyAssignmentCompleted:
		return AssignmentCompletedEvent{Task: task, Assignment: "A1", Activity: mkActivity(t), Occupant: mkActor(t)}
	case FamilyReviewRecorded:
		return ReviewRecordedEvent{ReviewedTask: task, AxisTask: mkTask(t), Kind: SubjectImplementation, Verdict: VerdictAccept}
	case FamilyUATRecorded:
		return UATRecordedEvent{Subject: task, Kind: SubjectPlan, Resolution: VerdictRevise}
	case FamilySkillRun:
		return SkillRunEvent{Task: task, Skill: "pasture.worker", Actor: mkActor(t), Outcome: SkillSucceeded}
	case FamilyGitRemoteRefVerified:
		return GitRemoteRefVerifiedEvent{
			Task:       task,
			Repository: "dayvidpham/pasture",
			Ref:        "refs/heads/main",
			CommitOID:  provenance.GitOID("0123456789abcdef0123456789abcdef01234567"),
		}
	case FamilyTaskClosed:
		return TaskClosedEvent{Task: task, Reason: "reviewed and merged"}
	default:
		t.Fatalf("validEventFor: unhandled family %v (%d) — add it to the exhaustive test switch", f, int(f))
		return nil
	}
}

// TestEveryMaterialEventFamilyMaps proves the mapping is exhaustive: every family in
// the canonical list maps to a non-empty effect whose kind is the family's fixed kind
// and whose source task and payload/context set are populated.
func TestEveryMaterialEventFamilyMaps(t *testing.T) {
	for _, f := range materialEventFamilies {
		ev := validEventFor(t, f)
		if ev.Family() != f {
			t.Fatalf("family %v: validEventFor returned event with family %v", f, ev.Family())
		}
		eff, err := MapMaterialEvent(ev)
		if err != nil {
			t.Fatalf("family %v: MapMaterialEvent failed: %v", f, err)
		}
		if eff.Sort != provenance.EffectTaskEvent {
			t.Errorf("family %v: effect sort = %v, want EffectTaskEvent", f, eff.Sort)
		}
		if eff.EventKind != f.EventKind() {
			t.Errorf("family %v: effect kind = %q, want fixed kind %q", f, eff.EventKind, f.EventKind())
		}
		if eff.TaskID != ev.SourceTask() {
			t.Errorf("family %v: effect task = %v, want source task %v", f, eff.TaskID, ev.SourceTask())
		}
		if len(eff.Payload) == 0 {
			t.Errorf("family %v: effect payload is empty", f)
		}
		if !json.Valid(eff.Payload) {
			t.Errorf("family %v: effect payload is not valid JSON: %s", f, eff.Payload)
		}
		if len(eff.Contexts) == 0 {
			t.Errorf("family %v: effect has no contexts", f)
		}
		// A material event is non-lifecycle: its kind must never be a status-changing
		// transition kind, so folding it can never move task status.
		if provenance.IsTransitionLifecycleKind(eff.EventKind) {
			t.Errorf("family %v: kind %q is a lifecycle transition kind; material events must be non-lifecycle", f, eff.EventKind)
		}
	}
}

// TestEveryFamilyHasDistinctLegalFixedKind proves the fixed-kind discipline: each
// family's kind is legal per the journal grammar, versioned, and unique.
func TestEveryFamilyHasDistinctLegalFixedKind(t *testing.T) {
	seen := map[provenance.EventKind]MaterialEventFamily{}
	for _, f := range materialEventFamilies {
		kind := f.EventKind()
		if kind == "" {
			t.Errorf("family %d has empty fixed kind", int(f))
			continue
		}
		if err := provenance.ValidateEventKind(kind); err != nil {
			t.Errorf("family %v fixed kind %q is not a legal journal event kind: %v", f, kind, err)
		}
		if prev, dup := seen[kind]; dup {
			t.Errorf("families %v and %v share fixed kind %q", prev, f, kind)
		}
		seen[kind] = f
	}
	// The invalid sentinel must map to no kind.
	if materialEventInvalid.EventKind() != "" {
		t.Errorf("materialEventInvalid resolves to kind %q, want empty", materialEventInvalid.EventKind())
	}
}

// TestMapMaterialEventCanonicalPayloadIsDeterministic proves the payload is a pure
// function of the value: two independent maps of the same event yield byte-identical
// payloads (golden-comparable canonical encoding).
func TestMapMaterialEventCanonicalPayloadIsDeterministic(t *testing.T) {
	task := mkTask(t)
	actor := mkActor(t)
	for i := 0; i < 2; i++ {
		ev := SkillRunEvent{Task: task, Skill: "pasture.worker", Actor: actor, Outcome: SkillFailed}
		a, err := MapMaterialEvent(ev)
		if err != nil {
			t.Fatalf("map[%d]: %v", i, err)
		}
		b, err := MapMaterialEvent(ev)
		if err != nil {
			t.Fatalf("remap[%d]: %v", i, err)
		}
		if string(a.Payload) != string(b.Payload) {
			t.Fatalf("payload not deterministic: %s vs %s", a.Payload, b.Payload)
		}
	}
}

// TestMapMaterialEventRejectsInvalid proves validation runs before any journal
// encoding: a zero task, unknown enum, malformed git oid, and a nil event are all
// rejected with no effect produced.
func TestMapMaterialEventRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		ev   MaterialEvent
	}{
		{"nil-event", nil},
		{"zero-task", TaskClosedEvent{}},
		{"unknown-role", AssignmentStartedEvent{Task: mkTask(t), Assignment: "A1", Role: AssignmentRole(99), Occupant: mkActor(t)}},
		{"zero-occupant", AssignmentStartedEvent{Task: mkTask(t), Assignment: "A1", Role: RoleOwnerResponsibility}},
		{"empty-assignment", AssignmentCompletedEvent{Task: mkTask(t), Assignment: "", Occupant: mkActor(t)}},
		{"bad-verdict", ReviewRecordedEvent{ReviewedTask: mkTask(t), AxisTask: mkTask(t), Kind: SubjectPlan, Verdict: Verdict(0)}},
		{"empty-skill", SkillRunEvent{Task: mkTask(t), Skill: "", Actor: mkActor(t), Outcome: SkillSucceeded}},
		{"bad-git-oid", GitRemoteRefVerifiedEvent{Task: mkTask(t), Repository: "r", Ref: "refs/heads/main", CommitOID: "not-a-hash"}},
		{"empty-repo", GitRemoteRefVerifiedEvent{Task: mkTask(t), Repository: "", Ref: "refs/heads/main", CommitOID: provenance.GitOID("0123456789abcdef0123456789abcdef01234567")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff, err := MapMaterialEvent(tc.ev)
			if err == nil {
				t.Fatalf("expected rejection, got effect %+v", eff)
			}
			if eff.Sort != 0 || eff.EventKind != "" {
				t.Fatalf("rejected event still produced an effect: %+v", eff)
			}
		})
	}
}

// TestUnknownFamilyRejected proves a family value outside the closed set never maps.
func TestUnknownFamilyRejected(t *testing.T) {
	if k := MaterialEventFamily(12345).EventKind(); k != "" {
		t.Fatalf("unknown family resolved to kind %q, want empty", k)
	}
}

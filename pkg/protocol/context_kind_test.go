package protocol_test

import (
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// Membership and arity tests for the ContextKind enum.
//
// PROPOSAL-2 §7.5: ContextKind has exactly 8 values (None + 7 concrete).
// Scenario 10 (URE C1 binding) asserts ResearcherNoteContext is NOT a member.

func TestContextKind_AllContextKinds_HasExactly8Values(t *testing.T) {
	t.Parallel()

	const expected = 8
	if got := len(protocol.AllContextKinds); got != expected {
		t.Fatalf("AllContextKinds has %d values, want %d (PROPOSAL-2 §7.5: None + Epoch + Slice + Review + Followup + Git + Skill + Session)", got, expected)
	}
}

func TestContextKind_AllContextKinds_ContainsExpectedValues(t *testing.T) {
	t.Parallel()

	want := map[protocol.ContextKind]bool{
		protocol.ContextNone:     false,
		protocol.ContextEpoch:    false,
		protocol.ContextSlice:    false,
		protocol.ContextReview:   false,
		protocol.ContextFollowup: false,
		protocol.ContextGit:      false,
		protocol.ContextSkill:    false,
		protocol.ContextSession:  false,
	}
	for _, k := range protocol.AllContextKinds {
		if _, ok := want[k]; !ok {
			t.Errorf("AllContextKinds contains unexpected value %q", k)
			continue
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("AllContextKinds missing expected value %q", k)
		}
	}
}

// TestContextKind_ResearcherNoteContext_IsExcluded is BDD Scenario 10's
// enum-membership assertion. Per URE clarification C1, researcher's notes are
// out of scope; any code-review attempt to add ResearcherNoteContext to the
// enum must be rejected with a reference to C1.
func TestContextKind_ResearcherNoteContext_IsExcluded(t *testing.T) {
	t.Parallel()

	for _, k := range protocol.AllContextKinds {
		if k.String() == "ResearcherNoteContext" {
			t.Fatalf("ContextKind 'ResearcherNoteContext' is in AllContextKinds but URE C1 (Scenario 10) excludes it")
		}
	}
	if protocol.ContextKind("ResearcherNoteContext").IsValid() {
		t.Errorf(`ContextKind("ResearcherNoteContext").IsValid() = true, want false (URE C1 binding, Scenario 10)`)
	}
}

func TestContextKind_IsValid_AcceptsAllKnownValues(t *testing.T) {
	t.Parallel()

	for _, k := range protocol.AllContextKinds {
		if !k.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", k)
		}
	}
}

func TestContextKind_IsValid_RejectsUnknownValues(t *testing.T) {
	t.Parallel()

	cases := []protocol.ContextKind{
		"",
		"Unknown",
		"epochcontext", // case-sensitive
		"ResearcherNoteContext",
		"Note",
	}
	for _, c := range cases {
		if c.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", c)
		}
	}
}

func TestContextKind_String_ReturnsWireFormat(t *testing.T) {
	t.Parallel()

	cases := map[protocol.ContextKind]string{
		protocol.ContextNone:     "None",
		protocol.ContextEpoch:    "EpochContext",
		protocol.ContextSlice:    "SliceContext",
		protocol.ContextReview:   "ReviewContext",
		protocol.ContextFollowup: "FollowupContext",
		protocol.ContextGit:      "GitContext",
		protocol.ContextSkill:    "SkillContext",
		protocol.ContextSession:  "SessionContext",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("ContextKind(%v).String() = %q, want %q", k, got, want)
		}
	}
}

func TestContext_StructFields(t *testing.T) {
	t.Parallel()

	// Smoke test that the Context struct carries (Kind, ContextID).
	c := protocol.Context{
		Kind:      protocol.ContextEpoch,
		ContextID: "aura-plugins--01968a3c-1234-7000-8000-000000000000",
	}
	if c.Kind != protocol.ContextEpoch {
		t.Errorf("Context.Kind = %q, want %q", c.Kind, protocol.ContextEpoch)
	}
	if c.ContextID == "" {
		t.Errorf("Context.ContextID is empty")
	}
}

package provadapter

import (
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestDefaultLLMActorDescriptor(t *testing.T) {
	t.Parallel()
	id := DefaultLLMActorID()
	if id.String() != "pasture-system/00000000-0000-0000-0000-000000000001" || !IsDefaultLLMActor(id) {
		t.Fatalf("unexpected default LLM identity: %v", id)
	}
	entry := DefaultLLMActorEntry()
	if entry.ActorID != id || entry.Namespace != PastureSystemNamespace || entry.ActorKind != provenance.AgentKindSoftware || entry.Name != "pasture/default-llm" || entry.Metadata != `{"classification":"generic-llm","purpose":"execution-attribution","version":1}` {
		t.Fatalf("unexpected descriptor: %+v", entry)
	}
	reg := DefaultLLMActorRegistration()
	if reg.Entry != entry || !reg.Claim.Equal(PastureSystemClaim()) || reg.AgentName != entry.Name || reg.Version != "1" || reg.Source != "pasture" {
		t.Fatalf("unexpected registration: %+v", reg)
	}
	reg.Entry.Metadata = `{}`
	entry.Name = "changed"
	if DefaultLLMActorRegistration().Entry != DefaultLLMActorEntry() || DefaultLLMActorEntry().Metadata == reg.Entry.Metadata || DefaultLLMActorEntry().Name == entry.Name {
		t.Fatal("caller mutation changed the fixed descriptor")
	}
	if PastureSystemDefaultActorID().String() != "pasture-system/00000000-0000-0000-0000-000000000000" || PastureSystemDefaultEntry().Metadata != "" {
		t.Fatal("system committer descriptor changed")
	}
}

func TestDefaultLLMActorPredicateRejectsLookalikes(t *testing.T) {
	t.Parallel()
	if IsDefaultLLMActor(PastureSystemDefaultActorID()) || IsDefaultLLMActor(provenance.ActorID{}) {
		t.Fatal("system committer or absent identity classified as fallback")
	}
	lookalike := DefaultLLMActorID()
	lookalike.Namespace = "pasture/default-llm"
	if IsDefaultLLMActor(lookalike) {
		t.Fatal("name-like namespace classified as fallback")
	}
	lookalike = DefaultLLMActorID()
	lookalike.UUID[15] = 2
	if IsDefaultLLMActor(lookalike) {
		t.Fatal("neighboring reserved identity classified as fallback")
	}
}

func TestDefaultLLMActorFixedRegistration(t *testing.T) {
	t.Parallel()
	tr := openMemoryTracker(t)
	if _, err := ActivatePastureSystem(tr); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.SoftwareAgent(DefaultLLMActorID()); err == nil {
		t.Fatal("bootstrap unexpectedly activated fallback")
	}
	first, err := tr.RegisterFixedSoftwareAgent(DefaultLLMActorRegistration())
	if err != nil {
		t.Fatal(err)
	}
	second, err := tr.RegisterFixedSoftwareAgent(DefaultLLMActorRegistration())
	if err != nil || first != second || first.ID != DefaultLLMActorID() || first.Name != "pasture/default-llm" {
		t.Fatalf("fixed registration not idempotent: %+v %+v %v", first, second, err)
	}
	drift := DefaultLLMActorRegistration()
	drift.Entry.Metadata = `{"classification":"authoritative"}`
	if _, err := tr.RegisterFixedSoftwareAgent(drift); err == nil {
		t.Fatal("conflicting manifest accepted")
	}
	if _, err := tr.RegisterFixedSoftwareAgent(DefaultLLMActorRegistration()); err != nil {
		t.Fatalf("conflict damaged original descriptor: %v", err)
	}
	assertDefaultSoftwareAgent(t, tr)
}

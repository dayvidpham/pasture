package acceptance

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestExpandMutationsDeterministicBoundedAndNonVacuous(t *testing.T) {
	t.Parallel()
	corpus := validCorpus(t)
	first, err := ExpandMutations(corpus)
	if err != nil {
		t.Fatalf("ExpandMutations(first): %v", err)
	}
	second, err := ExpandMutations(corpus)
	if err != nil {
		t.Fatalf("ExpandMutations(second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expansion is nondeterministic\nfirst=%#v\nsecond=%#v", first, second)
	}
	if len(first.Mutants) != 1 || first.Mutants[0].Case.ID != "command-pass__remove-actor__1" || first.Mutants[0].Case.Class != MustFail {
		t.Fatalf("mutants = %#v", first.Mutants)
	}
	if first.Accounting[0].Selected != 1 {
		t.Fatalf("accounting = %#v", first.Accounting)
	}
}

func TestExpandMutationsRejectsDuplicatesNoOpsAndOverflow(t *testing.T) {
	t.Parallel()
	t.Run("duplicate-source", func(t *testing.T) {
		corpus := validCorpus(t)
		duplicate := cloneCase(corpus.Cases[1])
		duplicate.ID = "command-fail-copy"
		corpus.Cases = append(corpus.Cases, duplicate)
		if _, err := ExpandMutations(corpus); err == nil || !strings.Contains(err.Error(), "canonical duplicates") {
			t.Fatalf("duplicate error = %v", err)
		}
	})
	t.Run("no-op", func(t *testing.T) {
		corpus := validCorpus(t)
		corpus.Operators[0].ID = MutUnknownActor
		corpus.Cases[0].Mutations = []MutationOperatorID{MutUnknownActor}
		corpus.Cases[0].Target.Command.Argv[4] = mutationActorID.String()
		if _, err := ExpandMutations(corpus); err == nil || !strings.Contains(err.Error(), "produced a no-op") {
			t.Fatalf("no-op error = %v", err)
		}
	})
	t.Run("overflow", func(t *testing.T) {
		corpus := validCorpus(t)
		second := cloneCase(corpus.Cases[0])
		second.ID = "command-pass-second"
		second.Target.Command.Argv = append(second.Target.Command.Argv, "second")
		corpus.Cases = append(corpus.Cases, second)
		corpus.MaxGenerated = 1
		if _, err := ExpandMutations(corpus); err == nil || !strings.Contains(err.Error(), "exceeds corpus maxGenerated") {
			t.Fatalf("overflow error = %v", err)
		}
	})
}

func TestEvaluateMutationsRequiresDeclaredOracleKills(t *testing.T) {
	t.Parallel()
	expansion, err := ExpandMutations(validCorpus(t))
	if err != nil {
		t.Fatal(err)
	}
	id := expansion.Mutants[0].Case.ID
	report, err := EvaluateMutations(expansion, []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "typed validation", ReproductionCommand: []string{"pasture", "epoch", "land"}}})
	if err != nil {
		t.Fatalf("EvaluateMutations(killed): %v", err)
	}
	if report.Operators[0].Executed != 1 || report.Operators[0].Killed != 1 || report.Operators[0].Survived != 0 {
		t.Fatalf("report accounting = %#v", report.Operators)
	}

	if _, err := EvaluateMutations(expansion, nil); err == nil || !strings.Contains(err.Error(), "was not executed") || !strings.Contains(err.Error(), "killed zero") {
		t.Fatalf("missing execution error = %v", err)
	}
	survived, err := EvaluateMutations(expansion, []Observation{{MutantID: id, ObservedOracle: OracleAuthority, ObservedResult: "wrong failure", ReproductionCommand: []string{"pasture", "epoch", "land"}}})
	if err == nil || !strings.Contains(err.Error(), "survived") || survived.Operators[0].Survived != 1 {
		t.Fatalf("survivor report=%#v error=%v", survived, err)
	}
}

func TestEvaluateMutationsRejectsMalformedObservationsBeforeAccounting(t *testing.T) {
	t.Parallel()
	expansion, err := ExpandMutations(validCorpus(t))
	if err != nil {
		t.Fatal(err)
	}
	id := expansion.Mutants[0].Case.ID
	valid := Observation{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "typed validation", ReproductionCommand: []string{"pasture", "epoch", "land"}}
	tests := []struct {
		name         string
		observations []Observation
		want         string
	}{
		{"duplicate-id", []Observation{valid, valid}, "observed twice"},
		{"unknown-id", []Observation{{MutantID: "unknown", ObservedOracle: OracleValidation, ObservedResult: "result", ReproductionCommand: []string{"pasture"}}}, "unknown mutant"},
		{"invalid-oracle", []Observation{{MutantID: id, ObservedOracle: "other", ObservedResult: "result", ReproductionCommand: []string{"pasture"}}}, "invalid observed oracle"},
		{"empty-result", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ReproductionCommand: []string{"pasture"}}}, "observedResult must be non-empty"},
		{"oversized-result", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: strings.Repeat("x", MaxObservedResultBytes+1), ReproductionCommand: []string{"pasture"}}}, "observedResult must be non-empty"},
		{"empty-reproduction", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "result"}}, "reproductionCommand must contain"},
		{"empty-reproduction-arg", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "result", ReproductionCommand: []string{""}}}, "reproductionCommand[0]"},
		{"oversized-reproduction", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "result", ReproductionCommand: make([]string, MaxReproductionArgs+1)}}, "reproductionCommand must contain"},
		{"oversized-reproduction-arg", []Observation{{MutantID: id, ObservedOracle: OracleValidation, ObservedResult: "result", ReproductionCommand: []string{strings.Repeat("x", MaxReproductionArgBytes+1)}}}, "reproductionCommand[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if report, err := EvaluateMutations(expansion, test.observations); err == nil || !strings.Contains(err.Error(), test.want) || len(report.Operators) != 0 {
				t.Fatalf("EvaluateMutations report=%#v error=%v, want pre-accounting error %q", report, err, test.want)
			}
		})
	}
}

func TestEvaluateMutationsRejectsMalformedExpansion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Expansion)
		want string
	}{
		{"empty-sources", func(e *Expansion) { e.Sources = nil }, "expansion is vacuous"},
		{"empty-mutants", func(e *Expansion) { e.Mutants = nil }, "expansion is vacuous"},
		{"empty-accounting", func(e *Expansion) { e.Accounting = nil }, "expansion is vacuous"},
		{"prepopulated-runtime", func(e *Expansion) { e.Accounting[0].Executed = 1 }, "pre-populates runtime counters"},
		{"negative-generation", func(e *Expansion) { e.Accounting[0].Incompatible = -1 }, "negative generation counters"},
		{"invalid-generation", func(e *Expansion) { e.Accounting[0].Duplicate = 1 }, "invalid generation counters"},
		{"overflow-generation", func(e *Expansion) { e.Accounting[0].Incompatible = math.MaxInt }, "invalid generation counters"},
		{"selected-mismatch", func(e *Expansion) { e.Accounting[0].Selected++ }, "does not exactly correspond"},
		{"extra-operator-accounting", func(e *Expansion) {
			e.Accounting = append(e.Accounting, OperatorAccounting{OperatorID: MutUnknownActor, Selected: 1})
		}, "does not exactly correspond"},
		{"missing-operator-accounting", func(e *Expansion) { e.Mutants[0].OperatorID = MutUnknownActor }, "does not exactly correspond"},
		{"unknown-source", func(e *Expansion) { e.Mutants[0].SourceID = "unknown-source" }, "invalid operator/oracle"},
		{"oracle-mismatch", func(e *Expansion) { e.Mutants[0].Oracle = OracleAuthority }, "does not exactly match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expansion, err := ExpandMutations(validCorpus(t))
			if err != nil {
				t.Fatal(err)
			}
			test.edit(&expansion)
			if report, err := EvaluateMutations(expansion, nil); err == nil || !strings.Contains(err.Error(), test.want) || len(report.Operators) != 0 {
				t.Fatalf("EvaluateMutations malformed expansion report=%#v error=%v, want %q", report, err, test.want)
			}
		})
	}
}

func TestEvaluateMutationsExpansionCardinalityBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		limit int
		set   func(*Expansion, int)
		want  string
	}{
		{"sources", MaxCorpusCases, func(e *Expansion, size int) { e.Sources = make([]Case, size) }, "MaxCorpusCases"},
		{"mutants", MaxGeneratedCases, func(e *Expansion, size int) { e.Mutants = make([]Mutant, size) }, "MaxGeneratedCases"},
		{"accounting", MaxCorpusOperators, func(e *Expansion, size int) { e.Accounting = make([]OperatorAccounting, size) }, "MaxCorpusOperators"},
	}
	for _, test := range tests {
		t.Run(test.name+"-exact-bound", func(t *testing.T) {
			expansion, err := ExpandMutations(validCorpus(t))
			if err != nil {
				t.Fatal(err)
			}
			test.set(&expansion, test.limit)
			_, err = EvaluateMutations(expansion, nil)
			if err == nil {
				t.Fatal("zero-value exact-bound elements unexpectedly formed a valid expansion")
			}
			if strings.Contains(err.Error(), "exceeding "+test.want) {
				t.Fatalf("exact bound was rejected as oversized: %v", err)
			}
		})
		t.Run(test.name+"-above-bound", func(t *testing.T) {
			expansion, err := ExpandMutations(validCorpus(t))
			if err != nil {
				t.Fatal(err)
			}
			test.set(&expansion, test.limit+1)
			if report, err := EvaluateMutations(expansion, nil); err == nil || !strings.Contains(err.Error(), test.want) || len(report.Operators) != 0 {
				t.Fatalf("above-bound report=%#v error=%v, want actionable %s cardinality error", report, err, test.want)
			}
		})
	}
}

func TestEveryVersionedOperatorProducesOneDistinctMutant(t *testing.T) {
	t.Parallel()
	commandOperators := []MutationOperatorID{
		MutRemoveActor, MutUnknownActor, MutWrongActorKind, MutEndedAssignment,
		MutWrongAssignmentRole, MutWrongSubject, MutChangedCommand,
		MutChangedEvidence, MutUnknownJSONField, MutTrailingJSON,
	}
	all := append(commandOperators, MutNativeEventMismatch)
	for _, operatorID := range all {
		operatorID := operatorID
		t.Run(string(operatorID), func(t *testing.T) {
			t.Parallel()
			corpus := validCorpus(t)
			row := &corpus.Cases[0]
			row.Mutations = []MutationOperatorID{operatorID}
			row.Target.Command.Argv = append(row.Target.Command.Argv, "--subject", "subject-original")
			row.Setup.Assignments = []AssignmentSetup{{
				ID:    "assignment-original",
				Actor: row.Setup.Actors[0].ID,
				Role:  tasks.RoleOwnerResponsibility,
				Task:  mustTaskID(t, "acceptance--00000000-0000-7000-8000-000000000020"),
				Epoch: mustTaskID(t, "acceptance--00000000-0000-7000-8000-000000000021"),
				State: provenance.TransitionStarted,
			}}
			inline := `{"evidence":true}`
			row.Setup.Evidence = []EvidenceSetup{{ID: 1, Kind: "acceptance.evidence", Subject: "subject-original", Value: DataValue{Inline: &inline}}}
			kind := TargetProductionCommand
			if operatorID == MutNativeEventMismatch {
				kind = TargetNativeEvent
				input := `{"event":true}`
				output := "{}"
				row.Target = Target{Kind: kind, NativeEvent: &NativeEvent{Harness: HarnessOpenCode, Contract: "opencode/v1", Event: "session.created", InputJSON: DataValue{Inline: &input}}}
				row.Expect.OutputMutation = &DataValue{Inline: &output}
			}
			corpus.Operators = []MutationOperator{{ID: operatorID, Compatible: []TargetKind{kind}, Oracle: OracleValidation, MaxPerCase: 1}}
			expansion, err := ExpandMutations(corpus)
			if err != nil {
				t.Fatalf("ExpandMutations(%s): %v", operatorID, err)
			}
			if len(expansion.Mutants) != 1 || expansion.Mutants[0].OperatorID != operatorID {
				t.Fatalf("operator %s expansion = %#v", operatorID, expansion)
			}
		})
	}
}

func TestExpandMutationsRejectsOperatorsThatCrossTypedMaximums(t *testing.T) {
	t.Parallel()
	tests := []struct {
		operator MutationOperatorID
		prepare  func(*Case)
	}{
		{MutWrongActorKind, func(row *Case) {
			for i := len(row.Setup.Actors); i < MaxSetupRecords; i++ {
				row.Setup.Actors = append(row.Setup.Actors, ActorSetup{ID: provenance.ActorID{Namespace: "acceptance", UUID: uuid.NewSHA1(uuid.Nil, []byte(fmt.Sprintf("actor-%d", i)))}, Kind: provenance.AgentKindSoftware})
			}
		}},
		{MutChangedCommand, func(row *Case) {
			for len(row.Target.Command.Argv) < MaxArgvEntries {
				row.Target.Command.Argv = append(row.Target.Command.Argv, "bounded")
			}
		}},
		{MutTrailingJSON, func(row *Case) {
			value := strings.Repeat("x", MaxDataValueBytes)
			row.Target.Command.Stdin = DataValue{Inline: &value}
		}},
		{MutNativeEventMismatch, func(row *Case) {
			input := "{}"
			output := "{}"
			row.Target = Target{Kind: TargetNativeEvent, NativeEvent: &NativeEvent{Harness: HarnessOpenCode, Contract: "opencode/v1", Event: strings.Repeat("e", MaxDataValueBytes), InputJSON: DataValue{Inline: &input}}}
			row.Expect.OutputMutation = &DataValue{Inline: &output}
		}},
	}
	for _, test := range tests {
		t.Run(string(test.operator), func(t *testing.T) {
			corpus := validCorpus(t)
			row := &corpus.Cases[0]
			test.prepare(row)
			row.Mutations = []MutationOperatorID{test.operator}
			corpus.Operators = []MutationOperator{{ID: test.operator, Compatible: []TargetKind{row.Target.Kind}, Oracle: OracleValidation, MaxPerCase: 1}}
			if _, err := ExpandMutations(corpus); err == nil || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), string(test.operator)) || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("maximum-bound mutation error = %v", err)
			}
		})
	}
}

func mustTaskID(t *testing.T, raw string) provenance.TaskID {
	t.Helper()
	id, err := provenance.ParseTaskID(raw)
	if err != nil {
		t.Fatalf("ParseTaskID(%q): %v", raw, err)
	}
	return id
}

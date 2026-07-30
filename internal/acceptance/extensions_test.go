package acceptance

import "testing"

func TestMilestoneCoverageDefersThenGates(t *testing.T) {
	t.Parallel()
	spec := CoverageSpec{Milestone: MilestoneM1, Dimensions: []CoverageDimension{{ID: "cursor", Milestone: MilestoneM3, Values: []CoverageValue{"tampered", "lineage"}}}}
	results, err := EvaluateCoverage(spec, MilestoneM1, []CoverageAssignment{{Dimension: "cursor", Value: "tampered"}})
	if err != nil || results[0].Gated {
		t.Fatalf("M1 results=%v err=%v", results, err)
	}
	results, err = EvaluateCoverage(spec, MilestoneM3, []CoverageAssignment{{Dimension: "cursor", Value: "tampered"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireCovered(results); err == nil {
		t.Fatal("M3 incomplete coverage passed")
	}
}

func TestSourceMutationEvaluationRejectsSurvivorAndMissingExecution(t *testing.T) {
	t.Parallel()
	ops := []SourceMutationOperator{{ID: "remove-reader-guard", Guard: "TestBoundedReaderGuard", Rationale: "prove direct SQL is rejected"}}
	if err := EvaluateSourceMutations(ops, nil); err == nil {
		t.Fatal("unexecuted operator passed")
	}
	if err := EvaluateSourceMutations(ops, []SourceMutationResult{{OperatorID: ops[0].ID, Guard: ops[0].Guard, Observed: "guard stayed green"}}); err == nil {
		t.Fatal("surviving operator passed")
	}
	if err := EvaluateSourceMutations(ops, []SourceMutationResult{{OperatorID: ops[0].ID, Guard: ops[0].Guard, Observed: "guard turned red", Killed: true}}); err != nil {
		t.Fatalf("killed operator failed: %v", err)
	}
}

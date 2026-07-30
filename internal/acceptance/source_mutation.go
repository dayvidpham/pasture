package acceptance

import (
	"fmt"
	"strings"
)

type SourceMutationOperatorID string
type SourceMutationOperator struct {
	ID        SourceMutationOperatorID
	Guard     string
	Rationale string
}
type SourceMutationResult struct {
	OperatorID SourceMutationOperatorID
	Guard      string
	Killed     bool
	Observed   string
}

func EvaluateSourceMutations(operators []SourceMutationOperator, results []SourceMutationResult) error {
	if len(operators) == 0 || len(operators) > MaxCorpusOperators {
		return fmt.Errorf("source mutation evaluation requires 1..%d operators", MaxCorpusOperators)
	}
	declared := map[SourceMutationOperatorID]SourceMutationOperator{}
	for _, operator := range operators {
		if operator.ID == "" || strings.TrimSpace(operator.Guard) == "" || strings.TrimSpace(operator.Rationale) == "" {
			return fmt.Errorf("source mutation operators require id, exact guard, and rationale")
		}
		if _, ok := declared[operator.ID]; ok {
			return fmt.Errorf("source mutation operator %q is duplicated", operator.ID)
		}
		declared[operator.ID] = operator
	}
	seen := map[SourceMutationOperatorID]bool{}
	for _, result := range results {
		operator, ok := declared[result.OperatorID]
		if !ok {
			return fmt.Errorf("source mutation result names undeclared operator %q", result.OperatorID)
		}
		if seen[result.OperatorID] {
			return fmt.Errorf("source mutation operator %q executed more than once", result.OperatorID)
		}
		seen[result.OperatorID] = true
		if result.Guard != operator.Guard || strings.TrimSpace(result.Observed) == "" {
			return fmt.Errorf("source mutation operator %q did not report its exact guard and observation", result.OperatorID)
		}
		if !result.Killed {
			return fmt.Errorf("source mutation operator %q survived guard %q: %s", result.OperatorID, result.Guard, result.Observed)
		}
	}
	for id := range declared {
		if !seen[id] {
			return fmt.Errorf("source mutation operator %q was not executed", id)
		}
	}
	return nil
}

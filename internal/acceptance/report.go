package acceptance

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type OperatorAccounting struct {
	OperatorID   MutationOperatorID `json:"operatorId"`
	Selected     int                `json:"selected"`
	Executed     int                `json:"executed"`
	Killed       int                `json:"killed"`
	Survived     int                `json:"survived"`
	Incompatible int                `json:"incompatible"`
	Duplicate    int                `json:"duplicate"`
}

type Observation struct {
	MutantID            string     `json:"mutantId"`
	ObservedOracle      OracleKind `json:"observedOracle"`
	ObservedResult      string     `json:"observedResult"`
	ReproductionCommand []string   `json:"reproductionCommand"`
}

type MutantResult struct {
	MutantID            string             `json:"mutantId"`
	SourceID            string             `json:"sourceId"`
	OperatorID          MutationOperatorID `json:"operatorId"`
	ExpectedOracle      OracleKind         `json:"expectedOracle"`
	ObservedOracle      OracleKind         `json:"observedOracle"`
	ObservedResult      string             `json:"observedResult"`
	ReproductionCommand []string           `json:"reproductionCommand"`
	Killed              bool               `json:"killed"`
}

type Report struct {
	Schema    string               `json:"schema"`
	Operators []OperatorAccounting `json:"operators"`
	Mutants   []MutantResult       `json:"mutants"`
}

const ReportSchemaVersion = "pasture.acceptance-report/v1"

const (
	MaxObservedResultBytes  = 1 << 20
	MaxReproductionArgs     = MaxArgvEntries
	MaxReproductionArgBytes = 64 << 10
)

func EvaluateMutations(expansion Expansion, observations []Observation) (Report, error) {
	if len(expansion.Sources) > MaxCorpusCases {
		return Report{}, fmt.Errorf("acceptance report: expansion has %d sources, exceeding MaxCorpusCases=%d; reduce the externally constructed source corpus before evaluation", len(expansion.Sources), MaxCorpusCases)
	}
	if len(expansion.Mutants) > MaxGeneratedCases {
		return Report{}, fmt.Errorf("acceptance report: expansion has %d mutants, exceeding MaxGeneratedCases=%d; enforce the corpus generation bound before evaluation", len(expansion.Mutants), MaxGeneratedCases)
	}
	if len(expansion.Accounting) > MaxCorpusOperators {
		return Report{}, fmt.Errorf("acceptance report: expansion has %d accounting rows, exceeding MaxCorpusOperators=%d; provide at most one bounded row per declared operator", len(expansion.Accounting), MaxCorpusOperators)
	}
	if len(expansion.Sources) == 0 || len(expansion.Mutants) == 0 || len(expansion.Accounting) == 0 {
		return Report{}, errors.New("acceptance report: expansion is vacuous; non-empty sources, mutants, and operator accounting are required")
	}
	if len(observations) > MaxGeneratedCases {
		return Report{}, fmt.Errorf("acceptance report: received %d observations, exceeding the %d-result bound", len(observations), MaxGeneratedCases)
	}
	sourceIDs := make(map[string]bool, len(expansion.Sources))
	for i, source := range expansion.Sources {
		if source.ID == "" || sourceIDs[source.ID] {
			return Report{}, fmt.Errorf("acceptance report: expansion source[%d] has an empty or duplicate id %q", i, source.ID)
		}
		sourceIDs[source.ID] = true
	}
	mutants := make(map[string]Mutant, len(expansion.Mutants))
	mutantsPerOperator := make(map[MutationOperatorID]int, len(expansion.Accounting))
	for _, mutant := range expansion.Mutants {
		if mutant.Case.ID == "" || mutant.SourceID == "" || !sourceIDs[mutant.SourceID] || !mutant.OperatorID.IsValid() || !mutant.Oracle.IsValid() {
			return Report{}, fmt.Errorf("acceptance report: expansion contains a mutant with an empty id or invalid operator/oracle: %#v", mutant)
		}
		if mutant.Case.Expect.Oracle != mutant.Oracle {
			return Report{}, fmt.Errorf("acceptance report: mutant %q case oracle %q does not exactly match declared mutant oracle %q", mutant.Case.ID, mutant.Case.Expect.Oracle, mutant.Oracle)
		}
		if err := validateCompletedMutant(mutant.Case); err != nil {
			return Report{}, fmt.Errorf("acceptance report: mutant %q violates completed case invariants: %w", mutant.Case.ID, err)
		}
		if _, exists := mutants[mutant.Case.ID]; exists {
			return Report{}, fmt.Errorf("acceptance report: expansion contains duplicate mutant id %q", mutant.Case.ID)
		}
		mutants[mutant.Case.ID] = mutant
		mutantsPerOperator[mutant.OperatorID]++
	}
	stats := make(map[MutationOperatorID]*OperatorAccounting, len(expansion.Accounting))
	for _, initial := range expansion.Accounting {
		if !initial.OperatorID.IsValid() {
			return Report{}, fmt.Errorf("acceptance report: expansion accounting has invalid operator id %q", initial.OperatorID)
		}
		if _, exists := stats[initial.OperatorID]; exists {
			return Report{}, fmt.Errorf("acceptance report: expansion accounting repeats operator id %q", initial.OperatorID)
		}
		if initial.Selected < 0 || initial.Incompatible < 0 || initial.Duplicate < 0 {
			return Report{}, fmt.Errorf("acceptance report: expansion accounting for operator %q has negative generation counters", initial.OperatorID)
		}
		if initial.Duplicate != 0 || initial.Selected > len(expansion.Sources) || initial.Incompatible > len(expansion.Sources)-initial.Selected {
			return Report{}, fmt.Errorf("acceptance report: expansion accounting for operator %q has invalid generation counters; a completed expansion has zero duplicates and at most one selected/incompatible result per source", initial.OperatorID)
		}
		if initial.Executed != 0 || initial.Killed != 0 || initial.Survived != 0 {
			return Report{}, fmt.Errorf("acceptance report: expansion accounting for operator %q pre-populates runtime counters; executed, killed, and survived must start at zero", initial.OperatorID)
		}
		if want := mutantsPerOperator[initial.OperatorID]; want == 0 || initial.Selected != want {
			return Report{}, fmt.Errorf("acceptance report: operator %q accounting does not exactly correspond to generated mutants; selected=%d mutants=%d", initial.OperatorID, initial.Selected, want)
		}
		copy := initial
		stats[initial.OperatorID] = &copy
	}
	for _, mutant := range expansion.Mutants {
		if _, exists := stats[mutant.OperatorID]; !exists {
			return Report{}, fmt.Errorf("acceptance report: mutant %q references operator %q with no accounting row", mutant.Case.ID, mutant.OperatorID)
		}
	}
	if len(stats) != len(mutantsPerOperator) {
		return Report{}, fmt.Errorf("acceptance report: operator accounting set does not exactly match mutant operators; accounting=%d mutantOperators=%d", len(stats), len(mutantsPerOperator))
	}
	seen := make(map[string]bool, len(observations))
	for i, observation := range observations {
		if observation.MutantID == "" {
			return Report{}, fmt.Errorf("acceptance report: observation[%d] has an empty mutant id", i)
		}
		if seen[observation.MutantID] {
			return Report{}, fmt.Errorf("acceptance report: mutant %q was observed twice; execution accounting must be exactly once", observation.MutantID)
		}
		seen[observation.MutantID] = true
		if _, exists := mutants[observation.MutantID]; !exists {
			return Report{}, fmt.Errorf("acceptance report: observation names unknown mutant %q", observation.MutantID)
		}
		if !observation.ObservedOracle.IsValid() {
			return Report{}, fmt.Errorf("acceptance report: mutant %q has invalid observed oracle %q", observation.MutantID, observation.ObservedOracle)
		}
		if strings.TrimSpace(observation.ObservedResult) == "" || len(observation.ObservedResult) > MaxObservedResultBytes {
			return Report{}, fmt.Errorf("acceptance report: mutant %q observedResult must be non-empty and at most %d bytes", observation.MutantID, MaxObservedResultBytes)
		}
		if len(observation.ReproductionCommand) == 0 || len(observation.ReproductionCommand) > MaxReproductionArgs {
			return Report{}, fmt.Errorf("acceptance report: mutant %q reproductionCommand must contain 1..%d arguments", observation.MutantID, MaxReproductionArgs)
		}
		for argIndex, arg := range observation.ReproductionCommand {
			if arg == "" || len(arg) > MaxReproductionArgBytes {
				return Report{}, fmt.Errorf("acceptance report: mutant %q reproductionCommand[%d] must be non-empty and at most %d bytes", observation.MutantID, argIndex, MaxReproductionArgBytes)
			}
		}
	}
	seen = make(map[string]bool, len(observations))
	report := Report{Schema: ReportSchemaVersion}
	var failures []string
	for _, observation := range observations {
		seen[observation.MutantID] = true
		mutant := mutants[observation.MutantID]
		accounting := stats[mutant.OperatorID]
		accounting.Executed++
		killed := observation.ObservedOracle == mutant.Oracle
		if killed {
			accounting.Killed++
		} else {
			accounting.Survived++
			failures = append(failures, fmt.Sprintf("mutant %s from %s via %s survived: observed %s (%s), expected %s; reproduce: %s",
				mutant.Case.ID, mutant.SourceID, mutant.OperatorID, observation.ObservedOracle,
				observation.ObservedResult, mutant.Oracle, strings.Join(observation.ReproductionCommand, " ")))
		}
		report.Mutants = append(report.Mutants, MutantResult{MutantID: mutant.Case.ID, SourceID: mutant.SourceID, OperatorID: mutant.OperatorID, ExpectedOracle: mutant.Oracle, ObservedOracle: observation.ObservedOracle, ObservedResult: observation.ObservedResult, ReproductionCommand: slices.Clone(observation.ReproductionCommand), Killed: killed})
	}
	for id, mutant := range mutants {
		if !seen[id] {
			failures = append(failures, fmt.Sprintf("mutant %s from %s via %s was not executed; expected oracle %s", id, mutant.SourceID, mutant.OperatorID, mutant.Oracle))
		}
	}
	for id, accounting := range stats {
		if accounting.Executed == 0 {
			failures = append(failures, fmt.Sprintf("operator %s executed zero mutants", id))
		}
		if accounting.Killed == 0 {
			failures = append(failures, fmt.Sprintf("operator %s killed zero mutants by its declared oracle", id))
		}
		report.Operators = append(report.Operators, *accounting)
	}
	slices.SortFunc(report.Operators, func(a, b OperatorAccounting) int { return strings.Compare(string(a.OperatorID), string(b.OperatorID)) })
	slices.SortFunc(report.Mutants, func(a, b MutantResult) int { return strings.Compare(a.MutantID, b.MutantID) })
	if len(failures) != 0 {
		slices.Sort(failures)
		return report, errors.New("acceptance report: mutation contract failed: " + strings.Join(failures, "; "))
	}
	return report, nil
}

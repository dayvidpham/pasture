package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/tasks"
)

const mutantOrdinal = 1

var mutationActorID = mustStaticActorID("acceptance-mutant--00000000-0000-7000-8000-000000000001")

type Mutant struct {
	Case       Case
	SourceID   string
	OperatorID MutationOperatorID
	Oracle     OracleKind
}

type Expansion struct {
	Sources    []Case
	Mutants    []Mutant
	Accounting []OperatorAccounting
}

func ExpandMutations(corpus Corpus) (Expansion, error) {
	if err := ValidateCorpus(corpus); err != nil {
		return Expansion{}, err
	}
	sources := slices.Clone(corpus.Cases)
	slices.SortFunc(sources, func(a, b Case) int { return strings.Compare(a.ID, b.ID) })
	operators := slices.Clone(corpus.Operators)
	slices.SortFunc(operators, func(a, b MutationOperator) int { return strings.Compare(string(a.ID), string(b.ID)) })
	opByID := make(map[MutationOperatorID]MutationOperator, len(operators))
	accounting := make(map[MutationOperatorID]*OperatorAccounting, len(operators))
	generated := make(map[MutationOperatorID]int, len(operators))
	for _, operator := range operators {
		opByID[operator.ID] = operator
		accounting[operator.ID] = &OperatorAccounting{OperatorID: operator.ID}
	}

	seenCases := make(map[string]string, len(sources)+corpus.MaxGenerated)
	for _, source := range sources {
		digest, err := canonicalCaseDigest(source)
		if err != nil {
			return Expansion{}, fmt.Errorf("acceptance mutation: canonicalize source %q: %w", source.ID, err)
		}
		if prior, exists := seenCases[digest]; exists {
			return Expansion{}, fmt.Errorf("acceptance mutation: source cases %q and %q are canonical duplicates; remove one or make their behavior distinct", prior, source.ID)
		}
		seenCases[digest] = source.ID
	}

	var mutants []Mutant
	for _, source := range sources {
		requested := slices.Clone(source.Mutations)
		slices.SortFunc(requested, func(a, b MutationOperatorID) int { return strings.Compare(string(a), string(b)) })
		for _, operatorID := range requested {
			operator := opByID[operatorID]
			stats := accounting[operatorID]
			if !targetCompatible(operator, source.Target.Kind) {
				stats.Incompatible++
				continue
			}
			candidate, compatible, err := applyMutation(source, operator)
			if err != nil {
				return Expansion{}, fmt.Errorf("acceptance mutation: source %q operator %q failed before execution: %w", source.ID, operator.ID, err)
			}
			if !compatible {
				stats.Incompatible++
				continue
			}
			stats.Selected++
			candidate.ID = fmt.Sprintf("%s__%s__%d", source.ID, operator.ID, mutantOrdinal)
			candidate.Class = MustFail
			candidate.Expect.Oracle = operator.Oracle
			candidate.Mutations = []MutationOperatorID{}
			if err := validateCompletedMutant(candidate); err != nil {
				return Expansion{}, fmt.Errorf("acceptance mutation: completed mutant from source %q operator %q is invalid and was rejected before canonicalization: %w", source.ID, operator.ID, err)
			}
			before, err := canonicalMutationInput(source)
			if err != nil {
				return Expansion{}, err
			}
			after, err := canonicalMutationInput(candidate)
			if err != nil {
				return Expansion{}, err
			}
			if before == after {
				return Expansion{}, fmt.Errorf("acceptance mutation: operator %q produced a no-op for source %q; every mutant must change canonical target or setup data", operator.ID, source.ID)
			}
			digest, err := canonicalCaseDigest(candidate)
			if err != nil {
				return Expansion{}, err
			}
			if prior, exists := seenCases[digest]; exists {
				stats.Duplicate++
				return Expansion{}, fmt.Errorf("acceptance mutation: generated mutant %q duplicates %q canonically; expansion fails instead of silently dropping coverage", candidate.ID, prior)
			}
			if len(mutants) == corpus.MaxGenerated {
				return Expansion{}, fmt.Errorf("acceptance mutation: expansion exceeds corpus maxGenerated=%d at source %q operator %q; raise the reviewed bound or reduce mutations", corpus.MaxGenerated, source.ID, operator.ID)
			}
			seenCases[digest] = candidate.ID
			mutants = append(mutants, Mutant{Case: candidate, SourceID: source.ID, OperatorID: operator.ID, Oracle: operator.Oracle})
			generated[operator.ID]++
		}
	}
	slices.SortFunc(mutants, func(a, b Mutant) int { return strings.Compare(a.Case.ID, b.Case.ID) })

	resultStats := make([]OperatorAccounting, 0, len(operators))
	for _, operator := range operators {
		stats := *accounting[operator.ID]
		if stats.Selected == 0 {
			return Expansion{}, fmt.Errorf("acceptance mutation: enabled operator %q selected zero compatible source rows; add a compatible case that requests it", operator.ID)
		}
		if generated[operator.ID] == 0 {
			return Expansion{}, fmt.Errorf("acceptance mutation: enabled operator %q generated zero executable mutants; add a source row containing the fields this operator transforms", operator.ID)
		}
		resultStats = append(resultStats, stats)
	}
	if len(mutants) == 0 {
		return Expansion{}, errors.New("acceptance mutation: expansion generated zero mutants; mutation metadata cannot be decorative")
	}
	return Expansion{Sources: sources, Mutants: mutants, Accounting: resultStats}, nil
}

func validateCompletedMutant(candidate Case) error {
	if candidate.ID == "" || len(candidate.ID) > 256 || !corpusIDPattern.MatchString(candidate.ID[:min(len(candidate.ID), 128)]) {
		return fmt.Errorf("mutant id %q is empty, too long, or contains unsupported characters", candidate.ID)
	}
	for _, r := range candidate.ID {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '.' && r != '_' && r != '-' {
			return fmt.Errorf("mutant id %q contains unsupported character %q", candidate.ID, r)
		}
	}
	if candidate.Class != MustFail {
		return fmt.Errorf("mutant %q class is %q; completed mutants must be must-fail", candidate.ID, candidate.Class)
	}
	if candidate.Mutations == nil || len(candidate.Mutations) != 0 {
		return fmt.Errorf("mutant %q must carry an explicit empty mutations list", candidate.ID)
	}
	if err := validateTarget(candidate.ID, candidate.Target); err != nil {
		return err
	}
	if err := validateSetup(candidate); err != nil {
		return err
	}
	if err := validatePersistedDelta(candidate); err != nil {
		return err
	}
	if err := validateExpectation(candidate.ID, candidate.Target, candidate.Expect); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.Provenance.Requirement) == "" || strings.TrimSpace(candidate.Provenance.Record) == "" || strings.TrimSpace(candidate.Provenance.Rationale) == "" {
		return fmt.Errorf("mutant %q provenance requirement, record, and rationale must be non-empty", candidate.ID)
	}
	return nil
}

func targetCompatible(operator MutationOperator, kind TargetKind) bool {
	return slices.Contains(operator.Compatible, kind)
}

func applyMutation(source Case, operator MutationOperator) (Case, bool, error) {
	candidate := cloneCase(source)
	switch operator.ID {
	case MutRemoveActor:
		return mutateArg(&candidate, "--actor", "", true)
	case MutUnknownActor:
		return mutateArg(&candidate, "--actor", mutationActorID.String(), false)
	case MutWrongActorKind:
		changed, compatible, err := mutateArg(&candidate, "--actor", mutationActorID.String(), false)
		if err != nil || !compatible {
			return changed, compatible, err
		}
		changed.Setup.Actors = append(changed.Setup.Actors, ActorSetup{ID: mutationActorID, Kind: provenance.AgentKindSoftware})
		return changed, true, nil
	case MutEndedAssignment:
		for i := range candidate.Setup.Assignments {
			if candidate.Setup.Assignments[i].State == provenance.TransitionStarted {
				candidate.Setup.Assignments[i].State = provenance.TransitionEnded
				return candidate, true, nil
			}
		}
		return Case{}, false, nil
	case MutWrongAssignmentRole:
		if len(candidate.Setup.Assignments) == 0 {
			return Case{}, false, nil
		}
		if candidate.Setup.Assignments[0].Role == tasks.RoleAxisReviewer {
			candidate.Setup.Assignments[0].Role = tasks.RoleGoverningSupervisor
		} else {
			candidate.Setup.Assignments[0].Role = tasks.RoleAxisReviewer
		}
		return candidate, true, nil
	case MutWrongSubject:
		for _, flag := range []string{"--subject", "--proposal", "--candidate", "--epoch"} {
			if changed, compatible, err := mutateArg(&candidate, flag, "acceptance-mutated-subject", false); compatible || err != nil {
				return changed, compatible, err
			}
		}
		return Case{}, false, nil
	case MutChangedCommand:
		if candidate.Target.Command == nil {
			return Case{}, false, nil
		}
		candidate.Target.Command.Argv = append(candidate.Target.Command.Argv, "--acceptance-mutated-command")
		return candidate, true, nil
	case MutChangedEvidence:
		if len(candidate.Setup.Evidence) == 0 {
			return Case{}, false, nil
		}
		changed := "acceptance-mutated-evidence"
		candidate.Setup.Evidence[0].Value = DataValue{Inline: &changed}
		return candidate, true, nil
	case MutUnknownJSONField:
		value := inputDataValue(&candidate)
		if value == nil || value.Inline == nil {
			return Case{}, false, nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*value.Inline), &object); err != nil || object == nil {
			return Case{}, false, nil
		}
		object["acceptanceUnknownField"] = json.RawMessage("true")
		encoded, err := json.Marshal(object)
		if err != nil {
			return Case{}, false, fmt.Errorf("encode unknown-field mutation: %w", err)
		}
		text := string(encoded)
		value.Inline = &text
		return candidate, true, nil
	case MutTrailingJSON:
		value := inputDataValue(&candidate)
		if value == nil || value.Inline == nil {
			return Case{}, false, nil
		}
		text := *value.Inline + "\n{}"
		value.Inline = &text
		return candidate, true, nil
	case MutNativeEventMismatch:
		if candidate.Target.NativeEvent == nil {
			return Case{}, false, nil
		}
		candidate.Target.NativeEvent.Event += ".acceptance-mismatch"
		return candidate, true, nil
	default:
		return Case{}, false, fmt.Errorf("operator %q has no static implementation", operator.ID)
	}
}

func mutateArg(candidate *Case, flag, replacement string, remove bool) (Case, bool, error) {
	if candidate.Target.Command == nil {
		return Case{}, false, nil
	}
	argv := candidate.Target.Command.Argv
	for i := 0; i < len(argv); i++ {
		if argv[i] != flag {
			continue
		}
		if i+1 >= len(argv) {
			return Case{}, false, fmt.Errorf("flag %q has no value in argv", flag)
		}
		if remove {
			candidate.Target.Command.Argv = append(slices.Clone(argv[:i]), argv[i+2:]...)
		} else {
			candidate.Target.Command.Argv[i+1] = replacement
		}
		return *candidate, true, nil
	}
	return Case{}, false, nil
}

func inputDataValue(candidate *Case) *DataValue {
	if candidate.Target.Command != nil {
		return &candidate.Target.Command.Stdin
	}
	if candidate.Target.NativeEvent != nil {
		return &candidate.Target.NativeEvent.InputJSON
	}
	return nil
}

func cloneCase(source Case) Case {
	clone := source
	clone.Mutations = slices.Clone(source.Mutations)
	clone.Setup.Actors = slices.Clone(source.Setup.Actors)
	clone.Setup.Assignments = slices.Clone(source.Setup.Assignments)
	clone.Setup.Tasks = slices.Clone(source.Setup.Tasks)
	clone.Setup.Evidence = slices.Clone(source.Setup.Evidence)
	for i := range clone.Setup.Evidence {
		clone.Setup.Evidence[i].Value = cloneDataValue(source.Setup.Evidence[i].Value)
	}
	clone.Setup.PreState = cloneDataValue(source.Setup.PreState)
	if source.Target.Command != nil {
		command := *source.Target.Command
		command.Argv = slices.Clone(source.Target.Command.Argv)
		command.Stdin = cloneDataValue(source.Target.Command.Stdin)
		clone.Target.Command = &command
	}
	if source.Target.NativeEvent != nil {
		event := *source.Target.NativeEvent
		event.InputJSON = cloneDataValue(source.Target.NativeEvent.InputJSON)
		clone.Target.NativeEvent = &event
	}
	clone.Expect.ExitCode = cloneInt(source.Expect.ExitCode)
	clone.Expect.StdoutJSON = cloneDataValuePointer(source.Expect.StdoutJSON)
	clone.Expect.Stderr = cloneDataValuePointer(source.Expect.Stderr)
	clone.Expect.OutputMutation = cloneDataValuePointer(source.Expect.OutputMutation)
	clone.Delta.Graph = cloneExactDelta(source.Delta.Graph)
	clone.Delta.Assignments = cloneExactDelta(source.Delta.Assignments)
	clone.Delta.Decisions = cloneExactDelta(source.Delta.Decisions)
	clone.Delta.Evidence = cloneExactDelta(source.Delta.Evidence)
	clone.Delta.Activities = cloneExactDelta(source.Delta.Activities)
	clone.Delta.Events = cloneExactDelta(source.Delta.Events)
	clone.Delta.Journal = cloneExactDelta(source.Delta.Journal)
	clone.Delta.Projection = cloneExactDelta(source.Delta.Projection)
	return clone
}

func cloneDataValue(source DataValue) DataValue {
	clone := source
	if source.Inline != nil {
		text := *source.Inline
		clone.Inline = &text
	}
	return clone
}

func cloneDataValuePointer(source *DataValue) *DataValue {
	if source == nil {
		return nil
	}
	clone := cloneDataValue(*source)
	return &clone
}

func cloneInt(source *int) *int {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func cloneExactDelta(source ExactDelta) ExactDelta {
	clone := source
	clone.Added = slices.Clone(source.Added)
	clone.Changed = slices.Clone(source.Changed)
	clone.Removed = slices.Clone(source.Removed)
	return clone
}

func canonicalMutationInput(row Case) (string, error) {
	return digestJSON(struct {
		Target Target
		Setup  Setup
	}{row.Target, row.Setup})
}

func canonicalCaseDigest(row Case) (string, error) {
	return digestJSON(struct {
		Target Target
		Setup  Setup
		Expect Expectation
		Delta  PersistedDelta
	}{row.Target, row.Setup, row.Expect, row.Delta})
}

func digestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func mustStaticActorID(raw string) provenance.ActorID {
	id, err := provenance.ParseActorID(raw)
	if err != nil {
		panic(err)
	}
	return id
}

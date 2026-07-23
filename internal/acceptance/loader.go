package acceptance

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dayvidpham/provenance"
	"gopkg.in/yaml.v3"

	"github.com/dayvidpham/pasture/internal/tasks"
)

var corpusIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type corpusWire struct {
	Schema       *string                `yaml:"schema"`
	ID           *string                `yaml:"id"`
	MaxGenerated *int                   `yaml:"maxGenerated"`
	Cases        []caseWire             `yaml:"cases"`
	Operators    []mutationOperatorWire `yaml:"operators"`
}

type caseWire struct {
	ID         *string               `yaml:"id"`
	Class      *CaseClass            `yaml:"class"`
	Target     *targetWire           `yaml:"target"`
	Setup      *setupWire            `yaml:"setup"`
	Expect     *expectationWire      `yaml:"expect"`
	Delta      *persistedDeltaWire   `yaml:"delta"`
	Provenance *provenanceSourceWire `yaml:"provenance"`
	Mutations  []MutationOperatorID  `yaml:"mutations"`
}

type targetWire struct {
	Kind        *TargetKind            `yaml:"kind"`
	Command     *productionCommandWire `yaml:"command"`
	NativeEvent *nativeEventWire       `yaml:"nativeEvent"`
}

type productionCommandWire struct {
	Argv  []string       `yaml:"argv"`
	Stdin *dataValueWire `yaml:"stdin"`
}

type nativeEventWire struct {
	Harness   *HarnessKind   `yaml:"harness"`
	Contract  *string        `yaml:"contract"`
	Event     *string        `yaml:"event"`
	InputJSON *dataValueWire `yaml:"inputJSON"`
}

type dataValueWire struct {
	Inline  *string `yaml:"inline"`
	Fixture *string `yaml:"fixture"`
}

type setupWire struct {
	Fixture     *string               `yaml:"fixture"`
	PreState    *dataValueWire        `yaml:"preState"`
	Actors      []actorSetupWire      `yaml:"actors"`
	Assignments []assignmentSetupWire `yaml:"assignments"`
	Tasks       []taskSetupWire       `yaml:"tasks"`
	Evidence    []evidenceSetupWire   `yaml:"evidence"`
}

type actorSetupWire struct {
	ID   *string               `yaml:"id"`
	Kind *provenance.AgentKind `yaml:"kind"`
}

type assignmentSetupWire struct {
	ID    *string `yaml:"id"`
	Actor *string `yaml:"actor"`
	Role  *string `yaml:"role"`
	Task  *string `yaml:"task"`
	Epoch *string `yaml:"epoch"`
	State *string `yaml:"state"`
}

type taskSetupWire struct {
	ID     *string              `yaml:"id"`
	Kind   *provenance.TaskType `yaml:"kind"`
	Status *string              `yaml:"status"`
}

type evidenceSetupWire struct {
	ID      *int64         `yaml:"id"`
	Kind    *string        `yaml:"kind"`
	Subject *string        `yaml:"subject"`
	Value   *dataValueWire `yaml:"value"`
}

type expectationWire struct {
	Oracle         *OracleKind    `yaml:"oracle"`
	ExitCode       *int           `yaml:"exitCode"`
	StdoutJSON     *dataValueWire `yaml:"stdoutJSON"`
	Stderr         *dataValueWire `yaml:"stderr"`
	OutputMutation *dataValueWire `yaml:"outputMutation"`
}

type persistedDeltaWire struct {
	Graph       *exactDeltaWire `yaml:"graph"`
	Assignments *exactDeltaWire `yaml:"assignments"`
	Decisions   *exactDeltaWire `yaml:"decisions"`
	Evidence    *exactDeltaWire `yaml:"evidence"`
	Activities  *exactDeltaWire `yaml:"activities"`
	Events      *exactDeltaWire `yaml:"events"`
	Journal     *exactDeltaWire `yaml:"journal"`
	Projection  *exactDeltaWire `yaml:"projection"`
}

type exactDeltaWire struct {
	Added      []string `yaml:"added"`
	Changed    []string `yaml:"changed"`
	Removed    []string `yaml:"removed"`
	RowCount   *int     `yaml:"rowCount"`
	ByteDigest *string  `yaml:"byteDigest"`
}

type provenanceSourceWire struct {
	Requirement *string `yaml:"requirement"`
	Record      *string `yaml:"record"`
	Rationale   *string `yaml:"rationale"`
}

type mutationOperatorWire struct {
	ID         *MutationOperatorID `yaml:"id"`
	Compatible []TargetKind        `yaml:"compatible"`
	Oracle     *OracleKind         `yaml:"oracle"`
	MaxPerCase *int                `yaml:"maxPerCase"`
}

func LoadCorpus(path string) (Corpus, error) {
	file, err := os.Open(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("acceptance corpus: could not read %q while loading the corpus; no cases ran; fix the path or file permissions: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxCorpusBytes+1))
	if err != nil {
		return Corpus{}, fmt.Errorf("acceptance corpus: could not read bounded input from %q; no cases ran; verify the file is stable and readable: %w", path, err)
	}
	if len(data) > MaxCorpusBytes {
		return Corpus{}, fmt.Errorf("acceptance corpus: %q exceeds the %d-byte input bound; split the reviewed corpus into smaller files", path, MaxCorpusBytes)
	}
	corpus, err := DecodeCorpus(data)
	if err != nil {
		return Corpus{}, fmt.Errorf("acceptance corpus: could not load %q; the corpus was rejected before execution; fix the named schema error: %w", path, err)
	}
	return corpus, nil
}

func DecodeCorpus(data []byte) (Corpus, error) {
	if len(data) == 0 {
		return Corpus{}, errors.New("acceptance corpus: input is empty; provide one pasture.acceptance-corpus/v1 YAML document")
	}
	if len(data) > MaxCorpusBytes {
		return Corpus{}, fmt.Errorf("acceptance corpus: input has %d bytes, exceeding the %d-byte bound; split the reviewed corpus", len(data), MaxCorpusBytes)
	}
	var node yaml.Node
	nodeDecoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := nodeDecoder.Decode(&node); err != nil {
		return Corpus{}, fmt.Errorf("decode YAML document: %w", err)
	}
	if err := rejectExecutableYAML(&node); err != nil {
		return Corpus{}, err
	}
	var trailing yaml.Node
	if err := nodeDecoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Corpus{}, errors.New("acceptance corpus: multiple YAML documents are forbidden; keep the corpus in one data-only document")
		}
		return Corpus{}, fmt.Errorf("decode trailing YAML: %w", err)
	}

	var wire corpusWire
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&wire); err != nil {
		return Corpus{}, fmt.Errorf("strict schema decode failed (unknown and duplicate fields are forbidden): %w", err)
	}
	corpus, err := wire.toCorpus()
	if err != nil {
		return Corpus{}, err
	}
	if err := ValidateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func rejectExecutableYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("acceptance corpus: YAML aliases and anchors are forbidden; fixtures must contain explicit immutable data")
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("acceptance corpus: explicit YAML tag %q is forbidden; rely on implicit scalar resolution and keep fixtures data-only", node.Tag)
	}
	for _, child := range node.Content {
		if err := rejectExecutableYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func (w corpusWire) toCorpus() (Corpus, error) {
	if w.Schema == nil || w.ID == nil || w.MaxGenerated == nil || w.Cases == nil || w.Operators == nil {
		return Corpus{}, errors.New("acceptance corpus: schema, id, maxGenerated, cases, and operators are all required; use explicit empty lists where appropriate")
	}
	c := Corpus{Schema: *w.Schema, ID: *w.ID, MaxGenerated: *w.MaxGenerated}
	for i, op := range w.Operators {
		converted, err := op.convert()
		if err != nil {
			return Corpus{}, fmt.Errorf("acceptance corpus: operators[%d]: %w", i, err)
		}
		c.Operators = append(c.Operators, converted)
	}
	for i, row := range w.Cases {
		converted, err := row.convert()
		if err != nil {
			return Corpus{}, fmt.Errorf("acceptance corpus: cases[%d]: %w", i, err)
		}
		c.Cases = append(c.Cases, converted)
	}
	return c, nil
}

func (w mutationOperatorWire) convert() (MutationOperator, error) {
	if w.ID == nil || w.Oracle == nil || w.MaxPerCase == nil || w.Compatible == nil {
		return MutationOperator{}, errors.New("id, compatible, oracle, and maxPerCase are required")
	}
	return MutationOperator{ID: *w.ID, Compatible: w.Compatible, Oracle: *w.Oracle, MaxPerCase: *w.MaxPerCase}, nil
}

func (w caseWire) convert() (Case, error) {
	if w.ID == nil || w.Class == nil || w.Target == nil || w.Setup == nil || w.Expect == nil || w.Delta == nil || w.Provenance == nil || w.Mutations == nil {
		return Case{}, errors.New("id, class, target, setup, expect, delta, provenance, and mutations are required")
	}
	target, err := w.Target.convert()
	if err != nil {
		return Case{}, fmt.Errorf("target: %w", err)
	}
	setup, err := w.Setup.convert()
	if err != nil {
		return Case{}, fmt.Errorf("setup: %w", err)
	}
	expect, err := w.Expect.convert()
	if err != nil {
		return Case{}, fmt.Errorf("expect: %w", err)
	}
	delta, err := w.Delta.convert()
	if err != nil {
		return Case{}, fmt.Errorf("delta: %w", err)
	}
	prov, err := w.Provenance.convert()
	if err != nil {
		return Case{}, fmt.Errorf("provenance: %w", err)
	}
	return Case{ID: *w.ID, Class: *w.Class, Target: target, Setup: setup, Expect: expect, Delta: delta, Provenance: prov, Mutations: w.Mutations}, nil
}

func (w targetWire) convert() (Target, error) {
	if w.Kind == nil {
		return Target{}, errors.New("kind is required")
	}
	if (*w.Kind == TargetProductionCommand) != (w.Command != nil) || (*w.Kind == TargetNativeEvent) != (w.NativeEvent != nil) {
		return Target{}, errors.New("kind must select exactly one matching command or nativeEvent variant")
	}
	result := Target{Kind: *w.Kind}
	if w.Command != nil {
		if w.Command.Argv == nil || w.Command.Stdin == nil {
			return Target{}, errors.New("command argv and stdin are required")
		}
		stdin, err := w.Command.Stdin.convert("command stdin")
		if err != nil {
			return Target{}, err
		}
		result.Command = &ProductionCommand{Argv: w.Command.Argv, Stdin: stdin}
	} else {
		n := w.NativeEvent
		if n.Harness == nil || n.Contract == nil || n.Event == nil || n.InputJSON == nil {
			return Target{}, errors.New("nativeEvent harness, contract, event, and inputJSON are required")
		}
		input, err := n.InputJSON.convert("native event inputJSON")
		if err != nil {
			return Target{}, err
		}
		result.NativeEvent = &NativeEvent{Harness: *n.Harness, Contract: *n.Contract, Event: *n.Event, InputJSON: input}
	}
	return result, nil
}

func (w dataValueWire) convert(where string) (DataValue, error) {
	if (w.Inline == nil) == (w.Fixture == nil) {
		return DataValue{}, fmt.Errorf("%s must contain exactly one of inline or fixture (inline may explicitly be empty)", where)
	}
	if w.Inline != nil && len(*w.Inline) > MaxDataValueBytes {
		return DataValue{}, fmt.Errorf("%s inline bytes exceed %d-byte bound", where, MaxDataValueBytes)
	}
	if w.Fixture != nil && strings.TrimSpace(*w.Fixture) == "" {
		return DataValue{}, fmt.Errorf("%s fixture path is empty", where)
	}
	if w.Fixture != nil {
		if err := validateFixturePath(*w.Fixture); err != nil {
			return DataValue{}, fmt.Errorf("%s: %w", where, err)
		}
	}
	v := DataValue{Inline: w.Inline}
	if w.Fixture != nil {
		v.Fixture = *w.Fixture
	}
	return v, nil
}

func (w setupWire) convert() (Setup, error) {
	if w.Fixture == nil || w.PreState == nil || w.Actors == nil || w.Assignments == nil || w.Tasks == nil || w.Evidence == nil {
		return Setup{}, errors.New("fixture, preState, actors, assignments, tasks, and evidence are required")
	}
	pre, err := w.PreState.convert("setup preState")
	if err != nil {
		return Setup{}, err
	}
	s := Setup{Fixture: *w.Fixture, PreState: pre}
	for i, a := range w.Actors {
		if a.ID == nil || a.Kind == nil {
			return Setup{}, fmt.Errorf("actors[%d] requires id and kind", i)
		}
		id, err := provenance.ParseActorID(*a.ID)
		if err != nil {
			return Setup{}, fmt.Errorf("actors[%d].id %q is not a Provenance ActorID: %w", i, *a.ID, err)
		}
		s.Actors = append(s.Actors, ActorSetup{ID: id, Kind: *a.Kind})
	}
	for i, a := range w.Assignments {
		if a.ID == nil || a.Actor == nil || a.Role == nil || a.Task == nil || a.Epoch == nil || a.State == nil {
			return Setup{}, fmt.Errorf("assignments[%d] requires id, actor, role, task, epoch, and state", i)
		}
		actor, err := provenance.ParseActorID(*a.Actor)
		if err != nil {
			return Setup{}, fmt.Errorf("assignments[%d].actor: %w", i, err)
		}
		task, err := provenance.ParseTaskID(*a.Task)
		if err != nil {
			return Setup{}, fmt.Errorf("assignments[%d].task: %w", i, err)
		}
		epoch, err := provenance.ParseTaskID(*a.Epoch)
		if err != nil {
			return Setup{}, fmt.Errorf("assignments[%d].epoch: %w", i, err)
		}
		state, err := parseAssignmentState(*a.State)
		if err != nil {
			return Setup{}, fmt.Errorf("assignments[%d].state: %w", i, err)
		}
		if strings.TrimSpace(*a.ID) == "" || strings.TrimSpace(*a.Role) == "" {
			return Setup{}, fmt.Errorf("assignments[%d] id and role must be non-empty", i)
		}
		role, err := parseAssignmentRole(*a.Role)
		if err != nil {
			return Setup{}, fmt.Errorf("assignments[%d].role: %w", i, err)
		}
		s.Assignments = append(s.Assignments, AssignmentSetup{ID: provenance.AssignmentID(*a.ID), Actor: actor, Role: role, Task: task, Epoch: epoch, State: state})
	}
	for i, task := range w.Tasks {
		if task.ID == nil || task.Kind == nil || task.Status == nil {
			return Setup{}, fmt.Errorf("tasks[%d] requires id, kind, and status", i)
		}
		id, err := provenance.ParseTaskID(*task.ID)
		if err != nil {
			return Setup{}, fmt.Errorf("tasks[%d].id: %w", i, err)
		}
		status, err := parseTaskStatus(*task.Status)
		if err != nil {
			return Setup{}, fmt.Errorf("tasks[%d].status: %w", i, err)
		}
		s.Tasks = append(s.Tasks, TaskSetup{ID: id, Kind: *task.Kind, Status: status})
	}
	for i, e := range w.Evidence {
		if e.ID == nil || e.Kind == nil || e.Subject == nil || e.Value == nil {
			return Setup{}, fmt.Errorf("evidence[%d] requires id, kind, subject, and value", i)
		}
		if *e.ID <= 0 || strings.TrimSpace(*e.Kind) == "" || strings.TrimSpace(*e.Subject) == "" {
			return Setup{}, fmt.Errorf("evidence[%d] id must be positive and kind/subject non-empty", i)
		}
		if err := provenance.ValidateEventKind(provenance.EventKind(*e.Kind)); err != nil {
			return Setup{}, fmt.Errorf("evidence[%d].kind %q does not satisfy the production namespaced-kind grammar: %w", i, *e.Kind, err)
		}
		value, err := e.Value.convert(fmt.Sprintf("evidence[%d].value", i))
		if err != nil {
			return Setup{}, err
		}
		s.Evidence = append(s.Evidence, EvidenceSetup{ID: provenance.JournalID(*e.ID), Kind: provenance.EvidenceKind(*e.Kind), Subject: *e.Subject, Value: value})
	}
	return s, nil
}

func parseAssignmentState(raw string) (provenance.AssignmentTransition, error) {
	switch raw {
	case "started":
		return provenance.TransitionStarted, nil
	case "ended":
		return provenance.TransitionEnded, nil
	default:
		return 0, fmt.Errorf("unknown assignment state %q; use started or ended", raw)
	}
}

func parseAssignmentRole(raw string) (tasks.AssignmentRole, error) {
	switch raw {
	case "owner-responsibility":
		return tasks.RoleOwnerResponsibility, nil
	case "governing-supervisor":
		return tasks.RoleGoverningSupervisor, nil
	case "axis-reviewer":
		return tasks.RoleAxisReviewer, nil
	default:
		return 0, fmt.Errorf("unknown assignment role %q; use owner-responsibility, governing-supervisor, or axis-reviewer", raw)
	}
}

func parseTaskStatus(raw string) (provenance.TaskStatus, error) {
	switch raw {
	case "open":
		return provenance.TaskStatusOpen, nil
	case "in_progress":
		return provenance.TaskStatusInProgress, nil
	case "closed":
		return provenance.TaskStatusClosed, nil
	default:
		return 0, fmt.Errorf("unknown task status %q; use open, in_progress, or closed", raw)
	}
}

func (w expectationWire) convert() (Expectation, error) {
	if w.Oracle == nil {
		return Expectation{}, errors.New("oracle is required")
	}
	e := Expectation{Oracle: *w.Oracle, ExitCode: w.ExitCode}
	var err error
	if w.StdoutJSON != nil {
		v, x := w.StdoutJSON.convert("expect stdoutJSON")
		err = x
		e.StdoutJSON = &v
	}
	if err != nil {
		return Expectation{}, err
	}
	if w.Stderr != nil {
		v, x := w.Stderr.convert("expect stderr")
		err = x
		e.Stderr = &v
	}
	if err != nil {
		return Expectation{}, err
	}
	if w.OutputMutation != nil {
		v, x := w.OutputMutation.convert("expect outputMutation")
		err = x
		e.OutputMutation = &v
	}
	return e, err
}

func (w persistedDeltaWire) convert() (PersistedDelta, error) {
	items := []*exactDeltaWire{w.Graph, w.Assignments, w.Decisions, w.Evidence, w.Activities, w.Events, w.Journal, w.Projection}
	for i, item := range items {
		if item == nil {
			return PersistedDelta{}, fmt.Errorf("all eight exact delta sections are required; section %d is missing", i)
		}
	}
	converted := make([]ExactDelta, 8)
	for i, item := range items {
		d, err := item.convert()
		if err != nil {
			return PersistedDelta{}, fmt.Errorf("section %d: %w", i, err)
		}
		converted[i] = d
	}
	return PersistedDelta{Graph: converted[0], Assignments: converted[1], Decisions: converted[2], Evidence: converted[3], Activities: converted[4], Events: converted[5], Journal: converted[6], Projection: converted[7]}, nil
}

func (w exactDeltaWire) convert() (ExactDelta, error) {
	if w.Added == nil || w.Changed == nil || w.Removed == nil || w.RowCount == nil || w.ByteDigest == nil {
		return ExactDelta{}, errors.New("added, changed, removed, rowCount, and byteDigest are required; use explicit empty lists")
	}
	if *w.RowCount < 0 {
		return ExactDelta{}, errors.New("rowCount cannot be negative")
	}
	if !validDigest(*w.ByteDigest) {
		return ExactDelta{}, fmt.Errorf("byteDigest %q is invalid; use sha256:<64 lowercase hexadecimal digits>, never a placeholder", *w.ByteDigest)
	}
	return ExactDelta{Added: w.Added, Changed: w.Changed, Removed: w.Removed, RowCount: *w.RowCount, ByteDigest: *w.ByteDigest}, nil
}

func validDigest(raw string) bool {
	if len(raw) != 71 || !strings.HasPrefix(raw, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(raw, "sha256:"))
	return err == nil && len(decoded) == 32 && raw == strings.ToLower(raw)
}

func validateFixturePath(raw string) error {
	if len(raw) > 512 {
		return errors.New("fixture path exceeds the 512-byte bound")
	}
	clean := filepath.Clean(raw)
	if filepath.IsAbs(raw) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("fixture path %q escapes the reviewed fixture root; use a relative path without '..' traversal", raw)
	}
	return nil
}

func (w provenanceSourceWire) convert() (ProvenanceSource, error) {
	if w.Requirement == nil || w.Record == nil || w.Rationale == nil {
		return ProvenanceSource{}, errors.New("requirement, record, and rationale are required")
	}
	if strings.TrimSpace(*w.Requirement) == "" || strings.TrimSpace(*w.Record) == "" || strings.TrimSpace(*w.Rationale) == "" {
		return ProvenanceSource{}, errors.New("requirement, record, and rationale must be non-empty")
	}
	return ProvenanceSource{Requirement: *w.Requirement, Record: *w.Record, Rationale: *w.Rationale}, nil
}

// ValidateCorpus validates the final typed corpus. DecodeCorpus and
// ExpandMutations both use this boundary, so callers constructing Corpus values
// directly receive the same actionable failures as YAML callers.
func ValidateCorpus(c Corpus) error {
	if c.Schema != SchemaVersion {
		return fmt.Errorf("acceptance corpus: schema %q is unsupported; expected exactly %q", c.Schema, SchemaVersion)
	}
	if !corpusIDPattern.MatchString(c.ID) {
		return fmt.Errorf("acceptance corpus: id %q must match %s", c.ID, corpusIDPattern)
	}
	if c.MaxGenerated <= 0 || c.MaxGenerated > MaxGeneratedCases {
		return fmt.Errorf("acceptance corpus: maxGenerated=%d must be between 1 and %d", c.MaxGenerated, MaxGeneratedCases)
	}
	if len(c.Cases) == 0 || len(c.Cases) > MaxCorpusCases {
		return fmt.Errorf("acceptance corpus: cases count %d must be between 1 and %d", len(c.Cases), MaxCorpusCases)
	}
	if len(c.Operators) == 0 || len(c.Operators) > MaxCorpusOperators {
		return fmt.Errorf("acceptance corpus: operators count %d must be between 1 and %d", len(c.Operators), MaxCorpusOperators)
	}
	ops := make(map[MutationOperatorID]MutationOperator, len(c.Operators))
	for i, op := range c.Operators {
		if !op.ID.IsValid() || !op.Oracle.IsValid() || op.MaxPerCase != 1 || len(op.Compatible) == 0 {
			return fmt.Errorf("acceptance corpus: operator[%d] %q has invalid id, oracle, compatibility, or maxPerCase; pasture.acceptance-corpus/v1 requires maxPerCase exactly 1", i, op.ID)
		}
		if _, exists := ops[op.ID]; exists {
			return fmt.Errorf("acceptance corpus: duplicate operator id %q", op.ID)
		}
		seenKinds := map[TargetKind]bool{}
		for _, kind := range op.Compatible {
			if !kind.IsValid() || seenKinds[kind] {
				return fmt.Errorf("acceptance corpus: operator %q has invalid or duplicate compatible kind %q", op.ID, kind)
			}
			seenKinds[kind] = true
		}
		ops[op.ID] = op
	}
	classes := map[CaseClass]int{}
	ids := map[string]bool{}
	for i, row := range c.Cases {
		if !corpusIDPattern.MatchString(row.ID) || ids[row.ID] {
			return fmt.Errorf("acceptance corpus: case[%d] id %q is invalid or duplicate", i, row.ID)
		}
		ids[row.ID] = true
		if !row.Class.IsValid() {
			return fmt.Errorf("acceptance corpus: case %q has invalid class", row.ID)
		}
		classes[row.Class]++
		if err := validateTarget(row.ID, row.Target); err != nil {
			return err
		}
		if err := validateSetup(row); err != nil {
			return err
		}
		if err := validatePersistedDelta(row); err != nil {
			return err
		}
		if err := validateExpectation(row.ID, row.Target, row.Expect); err != nil {
			return err
		}
		if strings.TrimSpace(row.Provenance.Requirement) == "" || strings.TrimSpace(row.Provenance.Record) == "" || strings.TrimSpace(row.Provenance.Rationale) == "" {
			return fmt.Errorf("acceptance corpus: case %q provenance requirement, record, and rationale must be non-empty", row.ID)
		}
		seenMut := map[MutationOperatorID]bool{}
		if len(row.Mutations) > MaxCorpusOperators {
			return fmt.Errorf("acceptance corpus: case %q exceeds the %d-mutation bound", row.ID, MaxCorpusOperators)
		}
		for _, id := range row.Mutations {
			if seenMut[id] {
				return fmt.Errorf("acceptance corpus: case %q repeats mutation %q", row.ID, id)
			}
			seenMut[id] = true
			if _, ok := ops[id]; !ok {
				return fmt.Errorf("acceptance corpus: case %q references undeclared mutation %q", row.ID, id)
			}
		}
	}
	if classes[MustPass] == 0 || classes[MustFail] == 0 {
		return errors.New("acceptance corpus: non-vacuity failed; both must-pass and must-fail source classes must be nonempty")
	}
	return nil
}

func validateTarget(caseID string, target Target) error {
	if !target.Kind.IsValid() {
		return fmt.Errorf("acceptance corpus: case %q has invalid target kind %q", caseID, target.Kind)
	}
	if (target.Kind == TargetProductionCommand) != (target.Command != nil) || (target.Kind == TargetNativeEvent) != (target.NativeEvent != nil) {
		return fmt.Errorf("acceptance corpus: case %q target kind %q must select exactly one matching command or nativeEvent variant", caseID, target.Kind)
	}
	if target.Command != nil {
		if len(target.Command.Argv) == 0 {
			return fmt.Errorf("acceptance corpus: command case %q argv is empty; name the production binary and command", caseID)
		}
		if len(target.Command.Argv) > MaxArgvEntries {
			return fmt.Errorf("acceptance corpus: case %q argv exceeds %d entries", caseID, MaxArgvEntries)
		}
		for i, arg := range target.Command.Argv {
			if arg == "" || len(arg) > MaxDataValueBytes {
				return fmt.Errorf("acceptance corpus: case %q argv[%d] must be non-empty and at most %d bytes", caseID, i, MaxDataValueBytes)
			}
		}
		return validateDataValue("case "+caseID+" command stdin", target.Command.Stdin)
	}
	event := target.NativeEvent
	if !event.Harness.IsValid() || strings.TrimSpace(event.Contract) == "" || strings.TrimSpace(event.Event) == "" || len(event.Contract) > MaxDataValueBytes || len(event.Event) > MaxDataValueBytes {
		return fmt.Errorf("acceptance corpus: native event case %q requires a valid harness and non-empty contract/event", caseID)
	}
	return validateDataValue("case "+caseID+" native event inputJSON", event.InputJSON)
}

func validateDataValue(where string, value DataValue) error {
	if (value.Inline == nil) == (value.Fixture == "") {
		return fmt.Errorf("acceptance corpus: %s must contain exactly one of inline or fixture (inline may explicitly be empty)", where)
	}
	if value.Inline != nil && len(*value.Inline) > MaxDataValueBytes {
		return fmt.Errorf("acceptance corpus: %s inline bytes exceed %d-byte bound", where, MaxDataValueBytes)
	}
	if value.Fixture != "" && strings.TrimSpace(value.Fixture) == "" {
		return fmt.Errorf("acceptance corpus: %s fixture path is empty", where)
	}
	if value.Fixture != "" {
		if err := validateFixturePath(value.Fixture); err != nil {
			return fmt.Errorf("acceptance corpus: %s: %w", where, err)
		}
	}
	return nil
}

func validateSetup(row Case) error {
	if strings.TrimSpace(row.Setup.Fixture) == "" {
		return fmt.Errorf("acceptance corpus: case %q setup fixture is empty", row.ID)
	}
	if len(row.Setup.Fixture) > 512 {
		return fmt.Errorf("acceptance corpus: case %q setup fixture exceeds the 512-byte bound", row.ID)
	}
	if err := validateDataValue("case "+row.ID+" setup preState", row.Setup.PreState); err != nil {
		return err
	}
	if len(row.Setup.Actors) > MaxSetupRecords || len(row.Setup.Assignments) > MaxSetupRecords || len(row.Setup.Tasks) > MaxSetupRecords || len(row.Setup.Evidence) > MaxSetupRecords {
		return fmt.Errorf("acceptance corpus: case %q exceeds the %d-record bound for a setup collection", row.ID, MaxSetupRecords)
	}
	for i, actor := range row.Setup.Actors {
		if actor.ID.Namespace == "" || !actor.Kind.IsValid() {
			return fmt.Errorf("acceptance corpus: case %q actor[%d] requires a valid Provenance ActorID and agent kind", row.ID, i)
		}
	}
	for i, assignment := range row.Setup.Assignments {
		if assignment.ID == "" || assignment.Actor.Namespace == "" || assignment.Task.Namespace == "" || assignment.Epoch.Namespace == "" || !validAssignmentRole(assignment.Role) || !assignment.State.IsValid() {
			return fmt.Errorf("acceptance corpus: case %q assignment[%d] has an invalid id, actor, role, task, epoch, or state", row.ID, i)
		}
	}
	for i, task := range row.Setup.Tasks {
		if task.ID.Namespace == "" || !task.Kind.IsValid() || !validTaskStatus(task.Status) {
			return fmt.Errorf("acceptance corpus: case %q task[%d] has an invalid id, kind, or status", row.ID, i)
		}
	}
	for i, evidence := range row.Setup.Evidence {
		if evidence.ID <= 0 || strings.TrimSpace(evidence.Subject) == "" || provenance.ValidateEventKind(provenance.EventKind(evidence.Kind)) != nil {
			return fmt.Errorf("acceptance corpus: case %q evidence[%d] has an invalid id, kind, or subject", row.ID, i)
		}
		if err := validateDataValue(fmt.Sprintf("case %s evidence[%d].value", row.ID, i), evidence.Value); err != nil {
			return err
		}
	}
	return validateSetupIdentityUniqueness(row)
}

func validAssignmentRole(role tasks.AssignmentRole) bool {
	return role == tasks.RoleOwnerResponsibility || role == tasks.RoleGoverningSupervisor || role == tasks.RoleAxisReviewer
}

func validTaskStatus(status provenance.TaskStatus) bool {
	return status == provenance.TaskStatusOpen || status == provenance.TaskStatusInProgress || status == provenance.TaskStatusClosed
}

func validateExpectation(caseID string, target Target, expect Expectation) error {
	if !expect.Oracle.IsValid() {
		return fmt.Errorf("acceptance corpus: case %q has invalid expectation oracle %q", caseID, expect.Oracle)
	}
	values := []struct {
		name  string
		value *DataValue
	}{{"stdoutJSON", expect.StdoutJSON}, {"stderr", expect.Stderr}, {"outputMutation", expect.OutputMutation}}
	for _, field := range values {
		if field.value != nil {
			if err := validateDataValue("case "+caseID+" expectation "+field.name, *field.value); err != nil {
				return err
			}
		}
	}
	if target.Kind == TargetProductionCommand {
		if expect.ExitCode == nil || expect.StdoutJSON == nil || expect.Stderr == nil || expect.OutputMutation != nil {
			return fmt.Errorf("acceptance corpus: command case %q requires exitCode/stdoutJSON/stderr and forbids outputMutation", caseID)
		}
		if *expect.ExitCode < 0 || *expect.ExitCode > 255 {
			return fmt.Errorf("acceptance corpus: command case %q exitCode must be between 0 and 255", caseID)
		}
		return nil
	}
	if target.NativeEvent != nil && target.NativeEvent.Harness == HarnessOpenCode {
		if expect.OutputMutation == nil {
			return fmt.Errorf("acceptance corpus: OpenCode native event case %q requires outputMutation", caseID)
		}
	} else if expect.OutputMutation != nil {
		return fmt.Errorf("acceptance corpus: non-OpenCode case %q cannot declare outputMutation", caseID)
	}
	return nil
}

func validatePersistedDelta(row Case) error {
	for i, delta := range row.Delta.All() {
		if delta.Added == nil || delta.Changed == nil || delta.Removed == nil || delta.RowCount < 0 || !validDigest(delta.ByteDigest) {
			return fmt.Errorf("acceptance corpus: case %q delta section %d requires explicit added/changed/removed lists, non-negative rowCount, and a canonical sha256 digest", row.ID, i)
		}
	}
	return validateExactDeltaBounds(row)
}

func validateSetupIdentityUniqueness(row Case) error {
	actors := map[string]bool{}
	for _, actor := range row.Setup.Actors {
		id := actor.ID.String()
		if actors[id] {
			return fmt.Errorf("acceptance corpus: case %q setup repeats actor id %q", row.ID, id)
		}
		actors[id] = true
	}
	assignments := map[provenance.AssignmentID]bool{}
	for _, assignment := range row.Setup.Assignments {
		if assignments[assignment.ID] {
			return fmt.Errorf("acceptance corpus: case %q setup repeats assignment id %q", row.ID, assignment.ID)
		}
		assignments[assignment.ID] = true
	}
	tasks := map[string]bool{}
	for _, task := range row.Setup.Tasks {
		id := task.ID.String()
		if tasks[id] {
			return fmt.Errorf("acceptance corpus: case %q setup repeats task id %q", row.ID, id)
		}
		tasks[id] = true
	}
	evidence := map[provenance.JournalID]bool{}
	for _, item := range row.Setup.Evidence {
		if evidence[item.ID] {
			return fmt.Errorf("acceptance corpus: case %q setup repeats evidence journal id %d", row.ID, item.ID)
		}
		evidence[item.ID] = true
	}
	return nil
}

func validateExactDeltaBounds(row Case) error {
	for i, delta := range row.Delta.All() {
		if len(delta.Added) > MaxDeltaEntries || len(delta.Changed) > MaxDeltaEntries || len(delta.Removed) > MaxDeltaEntries {
			return fmt.Errorf("acceptance corpus: case %q delta section %d exceeds the %d-entry bound", row.ID, i, MaxDeltaEntries)
		}
	}
	return nil
}

func (t Target) CommandArgv() []string {
	if t.Command == nil {
		return nil
	}
	return t.Command.Argv
}

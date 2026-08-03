package acceptance

import (
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/tasks"
)

const SchemaVersion = "pasture.acceptance-corpus/v1"

const (
	MaxCorpusCases     = 1024
	MaxCorpusOperators = 64
	MaxGeneratedCases  = 4096
	MaxCorpusBytes     = 8 << 20
	MaxSetupRecords    = 4096
	MaxDeltaEntries    = 100_000
	MaxArgvEntries     = 256
	MaxDataValueBytes  = 1 << 20
)

type CaseClass string

const (
	MustPass CaseClass = "must-pass"
	MustFail CaseClass = "must-fail"
)

func (c CaseClass) IsValid() bool { return c == MustPass || c == MustFail }

func (c *CaseClass) UnmarshalText(text []byte) error {
	v := CaseClass(text)
	if !v.IsValid() {
		return fmt.Errorf("acceptance corpus: unknown case class %q; use must-pass or must-fail", text)
	}
	*c = v
	return nil
}

type TargetKind string

const (
	TargetProductionCommand TargetKind = "production-command"
	TargetNativeEvent       TargetKind = "native-event"
)

func (k TargetKind) IsValid() bool {
	return k == TargetProductionCommand || k == TargetNativeEvent
}

func (k *TargetKind) UnmarshalText(text []byte) error {
	v := TargetKind(text)
	if !v.IsValid() {
		return fmt.Errorf("acceptance corpus: unknown target kind %q; use production-command or native-event", text)
	}
	*k = v
	return nil
}

type HarnessKind string

const (
	HarnessClaudeCode  HarnessKind = "claude-code"
	HarnessCodexCLI    HarnessKind = "codex-cli"
	HarnessOpenCode    HarnessKind = "opencode"
	HarnessAntigravity HarnessKind = "antigravity"
)

func (h HarnessKind) IsValid() bool {
	switch h {
	case HarnessClaudeCode, HarnessCodexCLI, HarnessOpenCode, HarnessAntigravity:
		return true
	default:
		return false
	}
}

func (h *HarnessKind) UnmarshalText(text []byte) error {
	v := HarnessKind(text)
	if !v.IsValid() {
		return fmt.Errorf("acceptance corpus: unknown harness %q; use claude-code, codex-cli, opencode, or antigravity", text)
	}
	*h = v
	return nil
}

type OracleKind string

const (
	OracleSuccess         OracleKind = "success"
	OracleValidation      OracleKind = "validation-error"
	OracleAuthority       OracleKind = "authority-error"
	OracleConflict        OracleKind = "identity-conflict"
	OracleGate            OracleKind = "gate-rejection"
	OracleNativeTransport OracleKind = "native-transport"
	OracleNoWrites        OracleKind = "no-writes"
)

func (o OracleKind) IsValid() bool {
	switch o {
	case OracleSuccess, OracleValidation, OracleAuthority, OracleConflict, OracleGate, OracleNativeTransport, OracleNoWrites:
		return true
	default:
		return false
	}
}

func (o *OracleKind) UnmarshalText(text []byte) error {
	v := OracleKind(text)
	if !v.IsValid() {
		return fmt.Errorf("acceptance corpus: unknown oracle %q; use a declared pasture.acceptance-corpus/v1 oracle", text)
	}
	*o = v
	return nil
}

type MutationOperatorID string

const (
	MutRemoveActor         MutationOperatorID = "remove-actor"
	MutUnknownActor        MutationOperatorID = "unknown-actor"
	MutWrongActorKind      MutationOperatorID = "wrong-actor-kind"
	MutEndedAssignment     MutationOperatorID = "ended-assignment"
	MutWrongAssignmentRole MutationOperatorID = "wrong-assignment-role"
	MutWrongSubject        MutationOperatorID = "wrong-subject"
	MutChangedCommand      MutationOperatorID = "changed-command"
	MutChangedEvidence     MutationOperatorID = "changed-evidence"
	MutUnknownJSONField    MutationOperatorID = "unknown-json-field"
	MutTrailingJSON        MutationOperatorID = "trailing-json"
	MutNativeEventMismatch MutationOperatorID = "native-event-mismatch"
)

func (id MutationOperatorID) IsValid() bool {
	switch id {
	case MutRemoveActor, MutUnknownActor, MutWrongActorKind, MutEndedAssignment,
		MutWrongAssignmentRole, MutWrongSubject, MutChangedCommand,
		MutChangedEvidence, MutUnknownJSONField, MutTrailingJSON,
		MutNativeEventMismatch:
		return true
	default:
		return false
	}
}

func (id *MutationOperatorID) UnmarshalText(text []byte) error {
	v := MutationOperatorID(text)
	if !v.IsValid() {
		return fmt.Errorf("acceptance corpus: unknown mutation operator %q; declare one of the versioned operator IDs", text)
	}
	*id = v
	return nil
}

type Corpus struct {
	Schema       string
	ID           string
	MaxGenerated int
	Cases        []Case
	Operators    []MutationOperator
}

type Case struct {
	ID         string
	Class      CaseClass
	Target     Target
	Setup      Setup
	Expect     Expectation
	Delta      PersistedDelta
	Provenance ProvenanceSource
	Mutations  []MutationOperatorID
}

type Target struct {
	Kind        TargetKind
	Command     *ProductionCommand
	NativeEvent *NativeEvent
}

type ProductionCommand struct {
	Argv  []string
	Stdin DataValue
}

type NativeEvent struct {
	Harness   HarnessKind
	Contract  string
	Event     string
	InputJSON DataValue
}

type DataValue struct {
	Inline  *string
	Fixture string
}

func (v DataValue) IsInline() bool { return v.Inline != nil }

type Setup struct {
	Fixture     string
	PreState    DataValue
	Actors      []ActorSetup
	Assignments []AssignmentSetup
	Tasks       []TaskSetup
	Evidence    []EvidenceSetup
}

type ActorSetup struct {
	ID   provenance.ActorID
	Kind provenance.AgentKind
}

type AssignmentSetup struct {
	ID    provenance.AssignmentID
	Actor provenance.ActorID
	Role  tasks.AssignmentRole
	Task  provenance.TaskID
	Epoch provenance.TaskID
	State provenance.AssignmentTransition
}

type TaskSetup struct {
	ID     provenance.TaskID
	Kind   provenance.TaskType
	Status provenance.TaskStatus
}

type EvidenceSetup struct {
	ID      provenance.JournalID
	Kind    provenance.EvidenceKind
	Subject string
	Value   DataValue
}

type Expectation struct {
	Oracle         OracleKind
	ExitCode       *int
	StdoutJSON     *DataValue
	Stderr         *DataValue
	OutputMutation *DataValue
}

type PersistedDelta struct {
	Graph       ExactDelta
	Assignments ExactDelta
	Decisions   ExactDelta
	Evidence    ExactDelta
	Activities  ExactDelta
	Events      ExactDelta
	Journal     ExactDelta
	Projection  ExactDelta
}

func (d PersistedDelta) All() []ExactDelta {
	return []ExactDelta{d.Graph, d.Assignments, d.Decisions, d.Evidence, d.Activities, d.Events, d.Journal, d.Projection}
}

type ExactDelta struct {
	Added      []string
	Changed    []string
	Removed    []string
	RowCount   int
	ByteDigest string
}

type ProvenanceSource struct {
	Requirement string
	Record      string
	Rationale   string
	Capture     CaptureProvenance
}

type MutationOperator struct {
	ID         MutationOperatorID
	Compatible []TargetKind
	Oracle     OracleKind
	MaxPerCase int
}

// Package codegen provides types and canonical data maps for the Pasture
// protocol codegen system. These types are internal to the codegen package
// and are NOT exported outside of internal/codegen.
//
// They mirror the Python types.py spec dataclasses for use by Go template
// generators (schema.xml generation, SKILL.md generation, agent definitions).
//
// Import paths for referenced types:
//   - github.com/dayvidpham/pasture/internal/types — RoleId, Domain
//   - github.com/dayvidpham/pasture/pkg/protocol   — PhaseId
package codegen

import (
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Example ──────────────────────────────────────────────────────────────────

// Example is a labeled code example for a constraint or procedure step.
// Mirrors Python CodeExample dataclass.
type Example struct {
	Id              string
	Lang            string // ExampleLang wire value: "bash", "go", "python", etc.
	Label           string // ExampleLabel wire value: "correct", "anti-pattern", etc.
	Code            string
	AlsoIllustrates string // optional cross-reference to another constraint
}

// ─── ConstraintSpec ───────────────────────────────────────────────────────────

// ConstraintSpec is a single protocol constraint in Given/When/Then/Should-Not
// format. Mirrors Python ConstraintSpec dataclass.
type ConstraintSpec struct {
	Id        string
	Given     string
	When      string
	Then      string
	ShouldNot string
	Command   string    // optional primary command to run
	Examples  []Example // optional code examples
}

// ─── BehaviorSpec ─────────────────────────────────────────────────────────────

// BehaviorSpec is a role-tactical behavior in Given/When/Then/Should-Not
// format. Distinct from ConstraintSpec: behaviors are role-specific guidance,
// not formal protocol constraints. Mirrors Python BehaviorSpec dataclass.
//
// A non-zero FragRef marks this entry as a placement MARKER: all other
// fields are left zero and the entry is resolved to the SharedFragment payload
// pre-render by the resolution pass in skills.go.
type BehaviorSpec struct {
	Id        string
	Given     string
	When      string
	Then      string
	ShouldNot string
	FragRef   FragmentId // non-zero → marker; resolved pre-render from SharedFragmentSpecs
}

// ─── ProseSection ─────────────────────────────────────────────────────────────

// ProseSection is a titled block of markdown content for skill body rendering.
// Sections are rendered in slice order. Heading level is determined by the
// template (H2 for top-level, H3 for subsections).
//
// A non-zero FragRef marks this entry as a placement MARKER: all other
// fields are left zero and the entry is resolved to the SharedFragment payload
// pre-render by the resolution pass in skills.go.
type ProseSection struct {
	Id          string         // unique within skill body; not used during template rendering — available for programmatic lookup via ExtractSection or future ID-based access
	Title       string         // heading text, e.g. "What You Own"
	Content     string         // pre-formatted markdown content below the heading
	Subsections []ProseSection // optional nested sections (rendered as H3 under H2)
	FragRef     FragmentId     // non-zero → marker; resolved pre-render from SharedFragmentSpecs
}

// ─── FragmentKind ─────────────────────────────────────────────────────────────

// FragmentKind identifies the payload type stored in a SharedFragment.
type FragmentKind string

const (
	// FragmentKindBehavior indicates the fragment holds a *BehaviorSpec payload.
	FragmentKindBehavior FragmentKind = "behavior"

	// FragmentKindProse indicates the fragment holds a *ProseSection payload.
	FragmentKindProse FragmentKind = "prose"
)

// ─── FragmentId ───────────────────────────────────────────────────────────────

// FragmentId is the strongly-typed key for shared fragment registrations.
// Values match the frag--* canonical naming convention (e.g.
// "frag--rev-vote-options"). PascalCase constants are derived by dropping the
// "frag--" prefix, splitting on "-", and prefixing with "Frag".
//
// Mirrors the RoleId / PhaseId / CommandId convention.
type FragmentId string

// AllFragmentIds lists every declared FragmentId constant. It is maintained
// in sync with the constant declarations below and validated by
// ValidateGlobalIds (parity check: AllFragmentIds ↔ SharedFragmentSpecs keys).
// Mirrors AllRoleIds in pkg/protocol/enums.go.
var AllFragmentIds = []FragmentId{
	FragRevVoteOptions,
	FragSupReviewAllSlices,
	FragSupReviewCheckEach,
	FragSupReviewSeverityGroups,
	FragSupBlockerDualParent,
	FragSupDeferredFollowup,
	FragSupFollowupEpicTiming,
	FragSupSeverityTree,
	FragSupNamingConvention,
	FragRevPlanVoteOptions,
	FragValidationCases,
	FragReviewCleanExit,
}

const (
	// FragRevVoteOptions is the canonical vote-options table shared between the
	// reviewer and reviewer-vote skill bodies (D3-ratified ACCEPT row wording).
	FragRevVoteOptions FragmentId = "frag--rev-vote-options"

	// ── SLICE-3: supervisor review-wave behaviors (6) + severity-tree/naming prose (2) ──

	// FragSupReviewAllSlices is the canonical "spawn 3 reviewers for ALL slices"
	// behavior shared between the supervisor and impl-review skill bodies.
	FragSupReviewAllSlices FragmentId = "frag--sup-review-all-slices"

	// FragSupReviewCheckEach is the canonical "check each slice against criteria"
	// behavior shared between the supervisor and impl-review skill bodies.
	FragSupReviewCheckEach FragmentId = "frag--sup-review-check-each"

	// FragSupReviewSeverityGroups is the canonical "ALWAYS create 3 severity groups"
	// behavior shared between the supervisor and impl-review skill bodies.
	FragSupReviewSeverityGroups FragmentId = "frag--sup-review-severity-groups"

	// FragSupBlockerDualParent is the canonical "dual-parent: blocks BOTH severity
	// group AND slice" behavior shared between the supervisor and impl-review skill
	// bodies.
	FragSupBlockerDualParent FragmentId = "frag--sup-blocker-dual-parent"

	// FragSupDeferredFollowup is the canonical "route ONLY user-DEFER'd UAT items
	// to the FOLLOWUP epic; all review severities must reach 0 before wave close"
	// behavior shared between the supervisor and impl-review skill bodies.
	// (Renamed from FragSupImportantMinorFollowup per R7/A1: review severities are
	// no longer deferrable; the FOLLOWUP epic is fed solely by DEFER'd UAT items.)
	FragSupDeferredFollowup FragmentId = "frag--sup-deferred-followup"

	// FragSupFollowupEpicTiming is the canonical "supervisor creates EPIC_FOLLOWUP
	// immediately" behavior shared between the supervisor and impl-review skill
	// bodies.
	FragSupFollowupEpicTiming FragmentId = "frag--sup-followup-epic-timing"

	// FragSupSeverityTree is the canonical Severity Tree (EAGER Creation) prose
	// section shared between the supervisor and impl-review skill bodies.
	FragSupSeverityTree FragmentId = "frag--sup-severity-tree"

	// FragSupNamingConvention is the canonical Naming Convention prose section
	// (SLICE-{N}-REVIEW-{axis}-{round} format) shared between the supervisor and
	// impl-review skill bodies.
	FragSupNamingConvention FragmentId = "frag--sup-naming-convention"

	// ── SLICE-4: plan-review vote options (DISTINCT from code-review fragment) ──

	// FragRevPlanVoteOptions is the vote-options table for PLAN reviews (Phase 3
	// architect-request-review). DISTINCT from FragRevVoteOptions (code review)
	// by its final line: "Binary only. No severity tree for plan reviews."
	// (vs code review's "Binary only. No intermediate levels."); the ACCEPT-row
	// wording is shared (unified per UAT-2). Single-owner (reviewer-review-plan),
	// but promoted to a fragment for registry completeness and D2 distinctness
	// enforcement.
	FragRevPlanVoteOptions FragmentId = "frag--rev-plan-vote-options"

	// ── SLICE-1 (epoch improvements R6/R7): validation-cases + clean-review-exit ──

	// FragValidationCases is the canonical "elicit/confirm/evaluate concrete
	// validation cases for EVERY REQUEST — a definition of done plus correct and
	// incorrect behaviours, user-confirmed, with failing real-data cases stored as
	// test fixtures" behavior (R6/A2, generalized from fix-intent-only at v2-2).
	// Referenced via behaviorRef from the user-elicit, user-uat, and
	// worker-implement skill bodies (wired in SLICE-2).
	FragValidationCases FragmentId = "frag--validation-cases"

	// FragReviewCleanExit is the canonical "iterate review->fix->re-review up to the
	// chosen review-effort budget; clean = 0 BLOCKER + 0 IMPORTANT + 0 MINOR within
	// budget; on budget exhaustion without clean, surface outstanding findings to
	// the user at a gate" behavior (R7/A1, reworked to a configurable budget at
	// v2-2). Referenced via behaviorRef from the supervisor and impl-review skill
	// bodies (wired in SLICE-3).
	FragReviewCleanExit FragmentId = "frag--review-clean-exit"
)

// ─── SharedFragment ───────────────────────────────────────────────────────────

// SharedFragment is a reusable payload (either a BehaviorSpec or a
// ProseSection) stored in SharedFragmentSpecs and referenced by placement
// markers in SkillBody entries.
//
// Exactly one of Behavior or Prose must be non-nil. Owners are not stored
// directly — they are derived via FragmentToOwnerRefs() (D1: owners derived
// from consumer markers, not embedded).
type SharedFragment struct {
	Id       FragmentId
	Kind     FragmentKind
	Behavior *BehaviorSpec // non-nil when Kind == FragmentKindBehavior
	Prose    *ProseSection // non-nil when Kind == FragmentKindProse
}

// ─── RecipeBlock ──────────────────────────────────────────────────────────────

// RecipeBlock is a bd command recipe with context and code example.
type RecipeBlock struct {
	Id          string // unique within skill body; not used during template rendering — available for programmatic lookup
	Title       string // e.g. "Phase 1: REQUEST Task"
	Description string // context paragraph before the code block
	Lang        string // code block language, typically "bash"
	Code        string // the actual bd command template
}

// ─── SkillBody ────────────────────────────────────────────────────────────────

// SkillBody is the complete body content for a role or sub-skill.
// Rendered inside the BEGIN/END marker region by the unified skill.go.tmpl
// and skill_sub.go.tmpl templates.
type SkillBody struct {
	Preamble  string         // optional intro (e.g., PROCESS.md link)
	Sections  []ProseSection // ordered prose sections (rendered as H2)
	Recipes   []RecipeBlock  // ordered code recipes
	Behaviors []BehaviorSpec // body-specific G/W/T behaviors (NOT ConstraintSpec)
}

// ─── RoleSpec ─────────────────────────────────────────────────────────────────

// RoleSpec is the complete specification for an agent role.
// Mirrors Python RoleSpec dataclass.
type RoleSpec struct {
	Id                 protocol.RoleId
	Name               string
	Description        string
	Model              string // e.g. "opus", "sonnet", "haiku"
	Thinking           string // e.g. "medium"
	Tools              []string
	OwnedPhases        []protocol.PhaseId
	Introduction       string
	OwnershipNarrative string
	Behaviors          []BehaviorSpec
}

// ─── CommandSpec ──────────────────────────────────────────────────────────────

// CommandSpec is the complete specification for a protocol command (skill).
// Mirrors Python CommandSpec dataclass.
type CommandSpec struct {
	Id          string // CommandId wire value e.g. "cmd-worker"
	Name        string // e.g. "pasture:worker"
	Description string
	// Title is the curated H1 heading text for the command's sub-skill SKILL.md
	// (WITHOUT the leading "# "). It is captured statically from each sub-skill's
	// curated on-disk H1 (e.g. "User Acceptance Test (UAT)" for cmd-user-uat) so
	// that the generator can emit YAML frontmatter ABOVE the heading via
	// skill_sub.go.tmpl while PRESERVING the hand-authored title verbatim.
	//
	// Only sub-skill commands (those listed in commandSkillDirs in harness.go)
	// need Title populated; role-level commands
	// (cmd-supervisor, cmd-worker, etc.) render through skill.go.tmpl which
	// derives its H1 from RoleSpec.Name, so their Title is left empty.
	Title         string
	RoleRef       protocol.RoleId // may be zero value if unassigned
	Phases        []protocol.PhaseId
	File          string   // relative path to skill file
	CreatesLabels []string // label IDs this command creates
}

// ─── Transition ───────────────────────────────────────────────────────────────

// Transition is a single valid phase transition.
// Mirrors Python Transition dataclass.
type Transition struct {
	ToPhase   protocol.PhaseId
	Condition string
	Action    string // optional action on transition
}

// ─── PhaseSpec ────────────────────────────────────────────────────────────────

// PhaseSpec is the complete specification for a single protocol phase.
// Mirrors Python PhaseSpec dataclass.
type PhaseSpec struct {
	Id          protocol.PhaseId
	Name        string
	Number      int
	Domain      types.Domain
	OwnerRoles  []protocol.RoleId
	Transitions []Transition
}

// ─── HandoffSpec ──────────────────────────────────────────────────────────────

// HandoffSpec specifies an actor-change transition handoff document.
// Mirrors Python HandoffSpec dataclass.
type HandoffSpec struct {
	Id             string
	SourceRole     protocol.RoleId
	TargetRole     protocol.RoleId
	AtPhase        protocol.PhaseId
	ContentLevel   string // ContentLevel wire value: "full-provenance", "summary-with-ids"
	RequiredFields []string
}

// ─── FigureSpec ───────────────────────────────────────────────────────────────

// FigureSpec is a figure specification (ASCII diagram or other visual).
// Mirrors Python Figure dataclass.
type FigureSpec struct {
	Id           string // FigureId wire value
	Title        string
	Type         string // FigureType wire value: "ascii-diagram"
	RoleRefs     []protocol.RoleId
	SectionRef   string // SectionRef wire value: "workflows"
	WorkflowRefs []string
	CommandRefs  []string
	Content      string // loaded at generation time
}

// ─── ChecklistItem ────────────────────────────────────────────────────────────

// ChecklistItem is a single item in a completion checklist.
// Mirrors Python ChecklistItem dataclass.
type ChecklistItem struct {
	Id       string
	Text     string
	Required bool
}

// ─── Checklist ────────────────────────────────────────────────────────────────

// Checklist is a completion checklist for a role at a specific quality gate.
// Keyed in ChecklistSpecs by "{role}-{gate}". Mirrors Python Checklist dataclass.
type Checklist struct {
	Gate    string // GateType wire value: "completion", "slice-closure", etc.
	RoleRef protocol.RoleId
	Items   []ChecklistItem
}

// ─── CoordinationCommand ─────────────────────────────────────────────────────

// CoordinationCommand is a coordination command for inter-agent communication
// via Beads. Mirrors Python CoordinationCommand dataclass.
type CoordinationCommand struct {
	Id       string
	Action   string
	Template string
	RoleRef  protocol.RoleId // zero value means shared across all roles
	Shared   bool
}

// ─── WorkflowAction ───────────────────────────────────────────────────────────

// WorkflowAction is a single action within a workflow stage.
// Mirrors Python WorkflowAction dataclass.
type WorkflowAction struct {
	Id          string
	Instruction string
	Command     string // optional concrete shell/tool command
}

// ─── ExitCondition ────────────────────────────────────────────────────────────

// ExitCondition is an exit condition for a workflow stage.
// Type must be one of the ExitConditionType wire values.
// Mirrors Python ExitCondition dataclass.
type ExitCondition struct {
	Type      string // ExitConditionType: "success", "continue", "escalate", "proceed"
	Condition string
}

// ─── WorkflowStage ────────────────────────────────────────────────────────────

// WorkflowStage is a single stage in an agent workflow.
// Mirrors Python WorkflowStage dataclass.
type WorkflowStage struct {
	Id                string
	Name              string
	Order             int
	Execution         string           // WorkflowExecution: "sequential", "parallel", "conditional-loop"
	PhaseRef          protocol.PhaseId // optional phase this stage maps to
	Actions           []WorkflowAction
	ExitConditions    []ExitCondition
	OperationalDetail string // optional prose rendered immediately after formal actions, before exit conditions
}

// ─── Workflow ─────────────────────────────────────────────────────────────────

// Workflow is a complete workflow specification for an agent role.
// Keyed in WorkflowSpecs by workflow id. Mirrors Python Workflow dataclass.
type Workflow struct {
	Id          string
	Name        string
	Description string
	RoleRef     protocol.RoleId
	Stages      []WorkflowStage
}

// ─── ReviewAxisSpec ───────────────────────────────────────────────────────────

// ReviewAxisSpec is the complete specification for a code review axis.
// Mirrors Python ReviewAxisSpec dataclass.
type ReviewAxisSpec struct {
	Id           string
	Letter       string // ReviewAxis wire value: "correctness", "test_quality", "elegance"
	Name         string
	Short        string
	KeyQuestions []string
}

// ─── ProcedureStep ────────────────────────────────────────────────────────────

// ProcedureStep is a single step in a role's startup or operational procedure.
// Mirrors Python ProcedureStep dataclass.
type ProcedureStep struct {
	Id          string
	Order       int
	Instruction string
	Command     string           // optional exact shell/bd command
	Context     string           // optional situational context
	NextState   protocol.PhaseId // optional phase transition
	Examples    []Example
}

// ─── LabelSpec ────────────────────────────────────────────────────────────────

// LabelSpec is the complete specification for a protocol label.
// Mirrors Python LabelSpec dataclass.
type LabelSpec struct {
	Id          string
	Value       string // the actual label string e.g. "pasture:p9-impl:s9-slice"
	Special     bool
	PhaseRef    string // optional phase reference
	SubstepRef  string // optional substep reference
	SeverityRef string // optional severity reference
	Description string // optional description
}

// ─── TitleConvention ─────────────────────────────────────────────────────────

// TitleConvention is a task title naming convention for a phase/substep type.
// Mirrors Python TitleConvention dataclass.
type TitleConvention struct {
	Pattern       string
	LabelRef      string
	CreatedBy     string
	PhaseRef      string // optional phase reference
	ExtraLabelRef string // optional extra label reference
	Note          string // optional note
}

// ─── SubstepData ──────────────────────────────────────────────────────────────

// SubstepData holds canonical per-phase substep specifications.
// Mirrors the inner dicts in Python SUBSTEP_DATA.
type SubstepData struct {
	Id            string
	Type          string // SubstepType wire value
	Execution     string // ExecutionMode wire value
	Order         int
	LabelRef      string
	ParallelGroup string // optional parallel group name
	Description   string
	ExtraLabel    string // optional extra label ref
	// Instances specifies repeated substep instantiation metadata.
	Instances *SubstepInstances
	// StartupSequence signals that PROCEDURE_STEPS[supervisor] should be embedded.
	StartupSequence bool
}

// SubstepInstances describes how many times a substep is instantiated.
type SubstepInstances struct {
	Count string // e.g. "3", "N"
	Per   string // e.g. "review-axis", "production-code-path"
}

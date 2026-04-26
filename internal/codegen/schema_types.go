// Package codegen — encoding/xml annotated structs for schema.xml sections.
//
// These structs define the canonical XML shapes for all 17 sections of the
// Aura Protocol schema. They are used by SLICE-B to replace manual fmt.Fprintf
// calls with encoding/xml marshalling for 15 of the 17 sections.
//
// ConstraintsSection and ProcedureStepsSection are defined here for type
// safety and documentation purposes but are NOT used for xml.Marshal: those
// sections contain CDATA elements that encoding/xml cannot emit, so
// buildConstraints and buildProcedureSteps remain manual fmt.Fprintf builders.
package codegen

import "encoding/xml"

// ─── Enums section ────────────────────────────────────────────────────────────

// EnumsSection is the top-level <enums> element.
type EnumsSection struct {
	XMLName xml.Name   `xml:"enums"`
	Enums   []EnumType `xml:"enum"`
}

// EnumType is a single <enum name="..."> element containing values.
type EnumType struct {
	Name   string      `xml:"name,attr"`
	Values []EnumValue `xml:"value"`
}

// EnumValue is a single <value id="..." .../> element inside an <enum>.
type EnumValue struct {
	ID          string `xml:"id,attr"`
	Description string `xml:"description,attr"`
	// Optional attrs present on SeverityLevel values only.
	Blocks string `xml:"blocks,attr,omitempty"`
	Label  string `xml:"label,attr,omitempty"`
}

// ─── Labels section ───────────────────────────────────────────────────────────

// LabelsSection is the top-level <labels> element.
type LabelsSection struct {
	XMLName xml.Name    `xml:"labels"`
	Labels  []LabelElem `xml:"label"`
}

// LabelElem is a single <label .../> element.
type LabelElem struct {
	ID          string `xml:"id,attr"`
	Value       string `xml:"value,attr"`
	Special     string `xml:"special,attr,omitempty"`
	PhaseRef    string `xml:"phase-ref,attr,omitempty"`
	SubstepRef  string `xml:"substep-ref,attr,omitempty"`
	SeverityRef string `xml:"severity-ref,attr,omitempty"`
	Description string `xml:"description,attr,omitempty"`
}

// ─── Review axes section ──────────────────────────────────────────────────────

// ReviewAxesSection is the top-level <review-axes> element.
type ReviewAxesSection struct {
	XMLName xml.Name         `xml:"review-axes"`
	Axes    []ReviewAxisElem `xml:"axis"`
}

// ReviewAxisElem is a single <axis ...> element.
type ReviewAxisElem struct {
	ID           string            `xml:"id,attr"`
	Letter       string            `xml:"letter,attr"`
	Name         string            `xml:"name,attr"`
	Short        string            `xml:"short,attr"`
	KeyQuestions *KeyQuestionsElem `xml:"key-questions"`
}

// KeyQuestionsElem wraps <key-questions><q>...</q></key-questions>.
type KeyQuestionsElem struct {
	Questions []string `xml:"q"`
}

// ─── Phases section ───────────────────────────────────────────────────────────

// PhasesSection is the top-level <phases> element.
type PhasesSection struct {
	XMLName xml.Name    `xml:"phases"`
	Phases  []PhaseElem `xml:"phase"`
}

// PhaseElem is a single <phase id="..." number="..." domain="..." name="..."> element.
type PhaseElem struct {
	ID          string           `xml:"id,attr"`
	Number      string           `xml:"number,attr"`
	Domain      string           `xml:"domain,attr"`
	Name        string           `xml:"name,attr"`
	Description string           `xml:"description"`
	Substeps    *SubstepsElem    `xml:"substeps"`
	TaskTitles  []TaskTitleElem  `xml:"task-title"`
	Transitions *TransitionsElem `xml:"transitions"`
	// Optional special elements (present on specific phases only).
	SeverityTree *SeverityTreeElem `xml:"severity-tree"`
	SameActorAs  *SameActorAsElem  `xml:"same-actor-as"`
	TDDLayers    *TDDLayersElem    `xml:"tdd-layers"`
	FollowupEpic *FollowupEpicElem `xml:"followup-epic"`
}

// SubstepsElem wraps <substeps>.
type SubstepsElem struct {
	Substeps []SubstepElem `xml:"substep"`
}

// SubstepElem is a single <substep ...> element.
type SubstepElem struct {
	ID            string               `xml:"id,attr"`
	Type          string               `xml:"type,attr"`
	Execution     string               `xml:"execution,attr"`
	Order         string               `xml:"order,attr"`
	ParallelGroup string               `xml:"parallel-group,attr,omitempty"`
	LabelRef      string               `xml:"label-ref,attr"`
	Description   string               `xml:"description"`
	ExtraLabel    *ExtraLabelElem      `xml:"extra-label"`
	Instances     *InstancesElem       `xml:"instances"`
	Startup       *StartupSequenceElem `xml:"startup-sequence"`
}

// ExtraLabelElem is <extra-label ref="..."/>.
type ExtraLabelElem struct {
	Ref string `xml:"ref,attr"`
}

// InstancesElem is <instances count="..." per="..."/>.
type InstancesElem struct {
	Count string `xml:"count,attr"`
	Per   string `xml:"per,attr"`
}

// StartupSequenceElem wraps <startup-sequence> containing procedure steps.
type StartupSequenceElem struct {
	Steps []ProcedureStepElem `xml:"step"`
}

// ProcedureStepElem is a <step order="..." id="..." ...> element used in both
// startup-sequence and procedure-steps sections.
type ProcedureStepElem struct {
	Order       string `xml:"order,attr"`
	ID          string `xml:"id,attr"`
	NextState   string `xml:"next-state,attr,omitempty"`
	Instruction string `xml:"instruction"`
	Command     string `xml:"command,omitempty"`
	Context     string `xml:"context,omitempty"`
	// Examples contain CDATA — defined here for completeness but built manually.
	Examples []ExampleElem `xml:"example"`
}

// ExampleElem is an <example id="..." lang="..." label="..."> element.
// The <code> child contains CDATA and is handled by manual fmt.Fprintf.
type ExampleElem struct {
	ID              string `xml:"id,attr"`
	Lang            string `xml:"lang,attr"`
	Label           string `xml:"label,attr"`
	AlsoIllustrates string `xml:"also-illustrates,attr,omitempty"`
	// Code is omitted from xml struct tags: CDATA must be written manually.
}

// TaskTitleElem is a <task-title pattern="..." .../> or block element.
type TaskTitleElem struct {
	Pattern    string `xml:"pattern,attr"`
	Substep    string `xml:"substep,attr,omitempty"`
	Convention string `xml:"convention"`
}

// TransitionsElem wraps <transitions>.
type TransitionsElem struct {
	Transitions []TransitionElem `xml:"transition"`
}

// TransitionElem is a single <transition to-phase="..." condition="..." ...> element.
type TransitionElem struct {
	ToPhase         string               `xml:"to-phase,attr"`
	Condition       string               `xml:"condition,attr"`
	Action          string               `xml:"action,attr,omitempty"`
	SkillInvocation *SkillInvocationElem `xml:"skill-invocation"`
}

// SkillInvocationElem is <skill-invocation target-role="..." command-ref="..." directive="..."/>.
// target-role and command-ref are omitempty so they can be omitted when only directive
// (and optionally note) are needed (e.g., in handoff skill-invocation elements).
type SkillInvocationElem struct {
	TargetRole string `xml:"target-role,attr,omitempty"`
	CommandRef string `xml:"command-ref,attr,omitempty"`
	Directive  string `xml:"directive,attr"`
	Note       string `xml:"note,attr,omitempty"`
}

// SeverityTreeElem is the <severity-tree ...> element.
type SeverityTreeElem struct {
	Enabled  string         `xml:"enabled,attr"`
	Creation string         `xml:"creation,attr,omitempty"`
	Reason   string         `xml:"reason,attr,omitempty"`
	Rules    []string       `xml:"rule"`
	Groups   []SevGroupElem `xml:"group"`
}

// SevGroupElem is a <group severity-ref="..." label-ref="..." .../> element.
type SevGroupElem struct {
	SeverityRef string `xml:"severity-ref,attr"`
	LabelRef    string `xml:"label-ref,attr"`
	DualParent  string `xml:"dual-parent,attr,omitempty"`
}

// SameActorAsElem is <same-actor-as phase-ref="..." note="..."/>.
type SameActorAsElem struct {
	PhaseRef string `xml:"phase-ref,attr"`
	Note     string `xml:"note,attr"`
}

// TDDLayersElem wraps <tdd-layers>.
type TDDLayersElem struct {
	Layers []TDDLayerElem `xml:"layer"`
}

// TDDLayerElem is <layer number="..." name="..." description="..."/>.
type TDDLayerElem struct {
	Number      string `xml:"number,attr"`
	Name        string `xml:"name,attr"`
	Description string `xml:"description,attr"`
}

// FollowupEpicElem is <followup-epic label-ref="..." trigger="..." .../>.
type FollowupEpicElem struct {
	LabelRef       string `xml:"label-ref,attr"`
	Trigger        string `xml:"trigger,attr"`
	GatedOnBlocker string `xml:"gated-on-blocker,attr"`
	OwnerRole      string `xml:"owner-role,attr"`
}

// ─── Roles section ────────────────────────────────────────────────────────────

// RolesSection is the top-level <roles> element.
type RolesSection struct {
	XMLName xml.Name   `xml:"roles"`
	Roles   []RoleElem `xml:"role"`
}

// RoleElem is a single <role id="..." name="..." description="..."> element.
type RoleElem struct {
	ID                 string           `xml:"id,attr"`
	Name               string           `xml:"name,attr"`
	Description        string           `xml:"description,attr"`
	OwnedPhases        *OwnedPhasesElem `xml:"owns-phases"`
	Delegates          *DelegatesElem   `xml:"delegates"`
	LabelAwareness     string           `xml:"label-awareness"`
	UsesAxes           *UsesAxesElem    `xml:"uses-axes"`
	Invariants         *InvariantsElem  `xml:"invariants"`
	Tools              string           `xml:"tools"`
	Model              string           `xml:"model"`
	Thinking           string           `xml:"thinking"`
	OwnershipModel     string           `xml:"ownership-model"`
	Introduction       string           `xml:"introduction"`
	OwnershipNarrative string           `xml:"ownership-narrative"`
	Behaviors          *BehaviorsElem   `xml:"behaviors"`
}

// OwnedPhasesElem wraps <owns-phases>.
type OwnedPhasesElem struct {
	PhaseRefs []PhaseRefElem `xml:"phase-ref"`
}

// PhaseRefElem is <phase-ref ref="..."/>.
type PhaseRefElem struct {
	Ref string `xml:"ref,attr"`
}

// DelegatesElem wraps <delegates>.
type DelegatesElem struct {
	Delegates []DelegateElem `xml:"delegate"`
}

// DelegateElem is <delegate to-role="..." phases="..."/>.
type DelegateElem struct {
	ToRole string `xml:"to-role,attr"`
	Phases string `xml:"phases,attr"`
}

// UsesAxesElem wraps <uses-axes>.
type UsesAxesElem struct {
	AxisRefs []AxisRefElem `xml:"axis-ref"`
}

// AxisRefElem is <axis-ref ref="..."/>.
type AxisRefElem struct {
	Ref string `xml:"ref,attr"`
}

// InvariantsElem wraps <invariants>.
type InvariantsElem struct {
	Invariants []string `xml:"invariant"`
}

// BehaviorsElem wraps <behaviors>.
type BehaviorsElem struct {
	Behaviors []BehaviorElem `xml:"behavior"`
}

// BehaviorElem is a single <behavior id="..." given="..." when="..." then="..." should-not="..."/> element.
type BehaviorElem struct {
	ID        string `xml:"id,attr"`
	Given     string `xml:"given,attr"`
	When      string `xml:"when,attr"`
	Then      string `xml:"then,attr"`
	ShouldNot string `xml:"should-not,attr"`
}

// ─── Commands section ─────────────────────────────────────────────────────────

// CommandsSection is the top-level <commands> element.
type CommandsSection struct {
	XMLName  xml.Name      `xml:"commands"`
	Commands []CommandElem `xml:"command"`
}

// CommandElem is a single <command id="..." name="..." ...> element.
type CommandElem struct {
	ID            string             `xml:"id,attr"`
	Name          string             `xml:"name,attr"`
	RoleRef       string             `xml:"role-ref,attr,omitempty"`
	Description   string             `xml:"description,attr"`
	Phases        *CommandPhasesElem `xml:"phases"`
	CreatesLabels *CreatesLabelsElem `xml:"creates-labels"`
	File          string             `xml:"file"`
	Note          string             `xml:"note,omitempty"`
}

// CommandPhasesElem wraps <phases> inside a <command>.
type CommandPhasesElem struct {
	PhaseRefs []PhaseRefElem `xml:"phase-ref"`
}

// CreatesLabelsElem wraps <creates-labels>.
type CreatesLabelsElem struct {
	LabelRefs []LabelRefElem `xml:"label-ref"`
}

// LabelRefElem is <label-ref ref="..."/>.
type LabelRefElem struct {
	Ref string `xml:"ref,attr"`
}

// ─── Handoffs section ─────────────────────────────────────────────────────────

// HandoffsSection is the top-level <handoffs storage-pattern="..."> element.
type HandoffsSection struct {
	XMLName              xml.Name                  `xml:"handoffs"`
	StoragePattern       string                    `xml:"storage-pattern,attr"`
	Handoffs             []HandoffElem             `xml:"handoff"`
	SameActorTransitions *SameActorTransitionsElem `xml:"same-actor-transitions"`
}

// HandoffElem is a single <handoff id="..." ...> element.
type HandoffElem struct {
	ID              string               `xml:"id,attr"`
	SourceRole      string               `xml:"source-role,attr"`
	TargetRole      string               `xml:"target-role,attr"`
	AtPhase         string               `xml:"at-phase,attr"`
	ContentLevel    string               `xml:"content-level,attr"`
	FilePattern     string               `xml:"file-pattern,attr,omitempty"`
	Trigger         string               `xml:"trigger,attr,omitempty"`
	Context         string               `xml:"context,attr,omitempty"`
	RequiredFields  *RequiredFieldsElem  `xml:"required-fields"`
	SkillInvocation *SkillInvocationElem `xml:"skill-invocation"`
	Note            *HandoffNoteElem     `xml:"note"`
}

// RequiredFieldsElem wraps <required-fields> text content.
type RequiredFieldsElem struct {
	Text string `xml:",chardata"`
}

// HandoffNoteElem wraps <note> text content in a handoff.
type HandoffNoteElem struct {
	Text string `xml:",chardata"`
}

// SameActorTransitionsElem wraps <same-actor-transitions>.
type SameActorTransitionsElem struct {
	Note        string                    `xml:"note,attr"`
	Transitions []SameActorTransitionElem `xml:"transition"`
}

// SameActorTransitionElem is <transition from-phase="..." to-phase="..." actor="..."/>.
type SameActorTransitionElem struct {
	FromPhase string `xml:"from-phase,attr"`
	ToPhase   string `xml:"to-phase,attr"`
	Actor     string `xml:"actor,attr"`
}

// ─── Constraints section (type definition only — NOT for xml.Marshal) ─────────
//
// buildConstraints uses manual fmt.Fprintf because <code><![CDATA[...]]></code>
// requires CDATA output that encoding/xml cannot produce. These types are
// defined for documentation and type-safety only.

// ConstraintsSection documents the <constraints> element shape.
// NOT used for xml.Marshal.
type ConstraintsSection struct {
	Constraints []ConstraintElem
}

// ConstraintElem documents a single <constraint ...> element.
// NOT used for xml.Marshal.
type ConstraintElem struct {
	ID        string
	Given     string
	When      string
	Then      string
	ShouldNot string
	RoleRef   string
	PhaseRef  string
	Command   string
	Examples  []Example // see Example in specs.go
}

// ─── Task titles section ──────────────────────────────────────────────────────

// TaskTitlesSection is the top-level <task-titles> element.
type TaskTitlesSection struct {
	XMLName     xml.Name              `xml:"task-titles"`
	Conventions []TitleConventionElem `xml:"title-convention"`
}

// TitleConventionElem is a single <title-convention .../> element.
type TitleConventionElem struct {
	Pattern       string `xml:"pattern,attr"`
	LabelRef      string `xml:"label-ref,attr"`
	CreatedBy     string `xml:"created-by,attr"`
	PhaseRef      string `xml:"phase-ref,attr,omitempty"`
	ExtraLabelRef string `xml:"extra-label-ref,attr,omitempty"`
	Note          string `xml:"note,attr,omitempty"`
}

// ─── Documents section ────────────────────────────────────────────────────────

// DocumentsSection is the top-level <documents> element.
type DocumentsSection struct {
	XMLName   xml.Name       `xml:"documents"`
	Documents []DocumentElem `xml:"document"`
}

// DocumentElem is a single <document id="..." path="..." purpose="..."> element.
type DocumentElem struct {
	ID      string      `xml:"id,attr"`
	Path    string      `xml:"path,attr"`
	Purpose string      `xml:"purpose,attr"`
	Covers  *CoversElem `xml:"covers"`
}

// CoversElem wraps <covers>.
type CoversElem struct {
	Entities []CoverEntityElem `xml:"entity"`
}

// CoverEntityElem is <entity type="..." depth="..." .../> inside <covers>.
type CoverEntityElem struct {
	Type  string `xml:"type,attr"`
	Depth string `xml:"depth,attr"`
	Refs  string `xml:"refs,attr,omitempty"`
	Note  string `xml:"note,attr,omitempty"`
}

// ─── Dependency model section ─────────────────────────────────────────────────

// DependencyModelSection is the top-level <dependency-model> element.
// Content is mixed text/element — kept as raw text since it uses free-form
// prose and nested elements with no fixed structure.
type DependencyModelSection struct {
	XMLName        xml.Name            `xml:"dependency-model"`
	Rule           string              `xml:"rule"`
	CanonicalChain string              `xml:"canonical-chain"`
	Command        string              `xml:"command"`
	AntiPattern    string              `xml:"anti-pattern"`
	ReferenceLinks *ReferenceLinksElem `xml:"reference-links"`
}

// ReferenceLinksElem is the <reference-links note="..."> element.
type ReferenceLinksElem struct {
	Note    string `xml:"note,attr"`
	Pattern string `xml:"pattern"`
}

// ─── Followup lifecycle section ───────────────────────────────────────────────

// FollowupLifecycleSection is the top-level <followup-lifecycle> element.
type FollowupLifecycleSection struct {
	XMLName          xml.Name           `xml:"followup-lifecycle"`
	Trigger          string             `xml:"trigger"`
	OwnerRole        string             `xml:"owner-role"`
	GatedOnBlocker   string             `xml:"gated-on-blocker"`
	DependencyChain  *DepChainElem      `xml:"dependency-chain"`
	LeafTaskAdoption *LeafTaskAdoptElem `xml:"leaf-task-adoption"`
	References       *FollowupRefsElem  `xml:"references"`
	HandoffChain     *HandoffChainElem  `xml:"handoff-chain"`
}

// DepChainElem is <dependency-chain note="...">.
type DepChainElem struct {
	Note  string             `xml:"note,attr"`
	Steps []DepChainStepElem `xml:"step"`
}

// DepChainStepElem is <step task-title="..." phase-ref="..." description="..."/>.
type DepChainStepElem struct {
	TaskTitle   string `xml:"task-title,attr"`
	PhaseRef    string `xml:"phase-ref,attr"`
	Description string `xml:"description,attr"`
}

// LeafTaskAdoptElem is <leaf-task-adoption>.
type LeafTaskAdoptElem struct {
	Rule    string `xml:"rule"`
	Command string `xml:"command"`
	Note    string `xml:"note"`
}

// FollowupRefsElem wraps <references> in followup-lifecycle.
type FollowupRefsElem struct {
	Refs []FollowupRefElem `xml:"ref"`
}

// FollowupRefElem is <ref type="..." target="..." note="..."/>.
type FollowupRefElem struct {
	Type   string `xml:"type,attr"`
	Target string `xml:"target,attr"`
	Note   string `xml:"note,attr"`
}

// HandoffChainElem is <handoff-chain note="...">.
type HandoffChainElem struct {
	Note        string                  `xml:"note,attr"`
	Transitions []HandoffChainTransElem `xml:"transition"`
}

// HandoffChainTransElem is <transition order="..." handoff-ref="..." description="..." .../>.
type HandoffChainTransElem struct {
	Order       string `xml:"order,attr"`
	HandoffRef  string `xml:"handoff-ref,attr"`
	Description string `xml:"description,attr"`
	SameActor   string `xml:"same-actor,attr,omitempty"`
}

// ─── Procedure steps section (type definition only — NOT for xml.Marshal) ─────
//
// buildProcedureSteps uses manual fmt.Fprintf because <code><![CDATA[...]]></code>
// in example elements cannot be produced by encoding/xml. These types are
// defined for documentation and type-safety only.

// ProcedureStepsSection documents the <procedure-steps> element shape.
// NOT used for xml.Marshal.
type ProcedureStepsSection struct {
	RoleGroups []ProcedureRoleGroup
}

// ProcedureRoleGroup is a <role ref="..."> grouping inside <procedure-steps>.
// NOT used for xml.Marshal.
type ProcedureRoleGroup struct {
	Ref   string
	Steps []ProcedureStepElem
}

// ─── Checklists section ───────────────────────────────────────────────────────

// ChecklistsSection is the top-level <checklists> element.
type ChecklistsSection struct {
	XMLName    xml.Name        `xml:"checklists"`
	Checklists []ChecklistElem `xml:"checklist"`
}

// ChecklistElem is a single <checklist id="..." role-ref="..." gate="..."> element.
type ChecklistElem struct {
	ID      string              `xml:"id,attr"`
	RoleRef string              `xml:"role-ref,attr"`
	Gate    string              `xml:"gate,attr"`
	Items   []ChecklistItemElem `xml:"item"`
}

// ChecklistItemElem is an <item id="..." required="...">text</item> element.
type ChecklistItemElem struct {
	ID       string `xml:"id,attr"`
	Required string `xml:"required,attr"`
	Text     string `xml:",chardata"`
}

// ─── Coordination commands section ───────────────────────────────────────────

// CoordinationCommandsSection is the top-level <coordination-commands> element.
type CoordinationCommandsSection struct {
	XMLName  xml.Name       `xml:"coordination-commands"`
	Commands []CoordCmdElem `xml:"coord-cmd"`
}

// CoordCmdElem is a single <coord-cmd id="..." action="..." template="..." .../> element.
type CoordCmdElem struct {
	ID       string `xml:"id,attr"`
	Action   string `xml:"action,attr"`
	Template string `xml:"template,attr"`
	RoleRef  string `xml:"role-ref,attr,omitempty"`
	Shared   string `xml:"shared,attr,omitempty"`
}

// ─── Workflows section ────────────────────────────────────────────────────────

// WorkflowsSection is the top-level <workflows> element.
type WorkflowsSection struct {
	XMLName   xml.Name       `xml:"workflows"`
	Workflows []WorkflowElem `xml:"workflow"`
}

// WorkflowElem is a single <workflow id="..." name="..." role-ref="..." description="..."> element.
type WorkflowElem struct {
	ID          string      `xml:"id,attr"`
	Name        string      `xml:"name,attr"`
	RoleRef     string      `xml:"role-ref,attr"`
	Description string      `xml:"description,attr"`
	Stages      []StageElem `xml:"stage"`
}

// StageElem is a single <stage id="..." name="..." order="..." execution="..." ...> element.
type StageElem struct {
	ID             string         `xml:"id,attr"`
	Name           string         `xml:"name,attr"`
	Order          string         `xml:"order,attr"`
	Execution      string         `xml:"execution,attr"`
	PhaseRef       string         `xml:"phase-ref,attr,omitempty"`
	Actions        []ActionElem   `xml:"action"`
	ExitConditions []ExitCondElem `xml:"exit-condition"`
}

// ActionElem is <action id="..." instruction="..." .../> inside a stage.
type ActionElem struct {
	ID          string `xml:"id,attr"`
	Instruction string `xml:"instruction,attr"`
	Command     string `xml:"command,attr,omitempty"`
}

// ExitCondElem is <exit-condition type="..." condition="..."/> inside a stage.
type ExitCondElem struct {
	Type      string `xml:"type,attr"`
	Condition string `xml:"condition,attr"`
}

// ─── Figures section ──────────────────────────────────────────────────────────

// FiguresSection is the top-level <figures> element.
type FiguresSection struct {
	XMLName xml.Name     `xml:"figures"`
	Figures []FigureElem `xml:"figure"`
}

// FigureElem is a single <figure id="..." title="..." type="..." section-ref="..."> element.
type FigureElem struct {
	ID           string    `xml:"id,attr"`
	Title        string    `xml:"title,attr"`
	Type         string    `xml:"type,attr"`
	SectionRef   string    `xml:"section-ref,attr"`
	RoleRefs     []RefElem `xml:"role-ref"`
	WorkflowRefs []RefElem `xml:"workflow-ref"`
	CommandRefs  []RefElem `xml:"command-ref"`
}

// RefElem is a generic <*-ref ref="..."/> element used within a figure.
// encoding/xml resolves the element tag name from the slice field struct tag
// in FigureElem (role-ref, workflow-ref, or command-ref), so a single type
// serves all three reference collections.
type RefElem struct {
	Ref string `xml:"ref,attr"`
}

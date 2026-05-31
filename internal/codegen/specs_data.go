// Canonical data maps for the Pasture protocol codegen system.
//
// These are package-level vars that mirror the Python canonical dicts
// in aura_protocol/types.py. They are the single source of truth for
// code generation (schema.xml, SKILL.md, agent definitions).
//
// Integration with Python: test_schema_types_sync.py verifies the Python
// dicts match schema.xml; Go tests in specs_test.go verify Go maps are
// structurally complete (every RoleId, every PhaseId has an entry).
package codegen

import (
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── PhaseSpecs ───────────────────────────────────────────────────────────────

// PhaseSpecs maps each PhaseId to its full specification.
// Mirrors Python PHASE_SPECS dict.
var PhaseSpecs = map[protocol.PhaseId]PhaseSpec{
	protocol.PhaseRequest: {
		ID:         protocol.PhaseRequest,
		Name:       "Request",
		Number:     1,
		Domain:     types.DomainUser,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseElicit,
				Condition: "classification confirmed, research and explore complete",
			},
		},
	},
	protocol.PhaseElicit: {
		ID:         protocol.PhaseElicit,
		Name:       "Elicit",
		Number:     2,
		Domain:     types.DomainUser,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhasePropose,
				Condition: "URD created with structured requirements",
			},
		},
	},
	protocol.PhasePropose: {
		ID:         protocol.PhasePropose,
		Name:       "Propose",
		Number:     3,
		Domain:     types.DomainPlan,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseReview,
				Condition: "proposal created",
			},
		},
	},
	protocol.PhaseReview: {
		ID:         protocol.PhaseReview,
		Name:       "Review",
		Number:     4,
		Domain:     types.DomainPlan,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect, types.RoleReviewer},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhasePlanReview,
				Condition: "all 3 reviewers vote ACCEPT",
			},
			{
				ToPhase:   protocol.PhasePropose,
				Condition: "any reviewer votes REVISE",
				Action:    "create PROPOSAL-{N+1}, mark current aura:superseded",
			},
		},
	},
	protocol.PhasePlanReview: {
		ID:         protocol.PhasePlanReview,
		Name:       "Plan UAT",
		Number:     5,
		Domain:     types.DomainUser,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseRatify,
				Condition: "user accepts plan",
			},
			{
				ToPhase:   protocol.PhasePropose,
				Condition: "user requests changes",
				Action:    "create PROPOSAL-{N+1}",
			},
		},
	},
	protocol.PhaseRatify: {
		ID:         protocol.PhaseRatify,
		Name:       "Ratify",
		Number:     6,
		Domain:     types.DomainPlan,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseHandoff,
				Condition: "proposal ratified, IMPL_PLAN placeholder created",
			},
		},
	},
	protocol.PhaseHandoff: {
		ID:         protocol.PhaseHandoff,
		Name:       "Handoff",
		Number:     7,
		Domain:     types.DomainPlan,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleArchitect, types.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseImplPlan,
				Condition: "handoff document stored at .git/.aura/handoff/",
			},
		},
	},
	protocol.PhaseImplPlan: {
		ID:         protocol.PhaseImplPlan,
		Name:       "Impl Plan",
		Number:     8,
		Domain:     types.DomainImpl,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseWorkerSlices,
				Condition: "all slices created with leaf tasks, assigned, and dependency-chained",
			},
		},
	},
	protocol.PhaseWorkerSlices: {
		ID:         protocol.PhaseWorkerSlices,
		Name:       "Worker Slices",
		Number:     9,
		Domain:     types.DomainImpl,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleSupervisor, types.RoleWorker},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseCodeReview,
				Condition: "all slices complete, quality gates pass",
			},
		},
	},
	protocol.PhaseCodeReview: {
		ID:         protocol.PhaseCodeReview,
		Name:       "Code Review",
		Number:     10,
		Domain:     types.DomainImpl,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleSupervisor, types.RoleReviewer},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseImplUAT,
				Condition: "all 3 reviewers ACCEPT, all BLOCKERs resolved",
			},
			{
				ToPhase:   protocol.PhaseWorkerSlices,
				Condition: "any reviewer votes REVISE",
				Action:    "fix BLOCKERs in affected slices, then re-review",
			},
		},
	},
	protocol.PhaseImplUAT: {
		ID:         protocol.PhaseImplUAT,
		Name:       "Impl UAT",
		Number:     11,
		Domain:     types.DomainUser,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseLanding,
				Condition: "user accepts implementation",
			},
			{
				ToPhase:   protocol.PhaseWorkerSlices,
				Condition: "user requests changes",
				Action:    "create fix tasks in affected slices",
			},
		},
	},
	protocol.PhaseLanding: {
		ID:         protocol.PhaseLanding,
		Name:       "Landing",
		Number:     12,
		Domain:     types.DomainImpl,
		OwnerRoles: []types.RoleId{types.RoleEpoch, types.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseComplete,
				Condition: "git push succeeds, all tasks closed or dependency-resolved",
			},
		},
	},
}

// ─── ConstraintSpecs ─────────────────────────────────────────────────────────

// ConstraintSpecs maps constraint IDs to their full specifications.
// Mirrors Python CONSTRAINT_SPECS dict.
var ConstraintSpecs = map[string]ConstraintSpec{
	"C-audit-never-delete": {
		ID:        "C-audit-never-delete",
		Given:     "any task or label",
		When:      "modifying",
		Then:      "add labels and comments only",
		ShouldNot: "delete or close tasks prematurely, remove labels",
	},
	"C-audit-dep-chain": {
		ID:        "C-audit-dep-chain",
		Given:     "any phase transition",
		When:      "creating new task",
		Then:      "chain dependency: bd dep add parent --blocked-by child",
		ShouldNot: "skip dependency chaining or invert direction",
		Command:   "bd dep add <parent> --blocked-by <child>",
		Examples: []Example{
			{
				ID:    "C-audit-dep-chain-full",
				Lang:  "bash",
				Label: "correct",
				Code: "# Full dependency chain: work flows bottom-up, closure flows top-down\n" +
					"bd dep add request-id --blocked-by ure-id\n" +
					"bd dep add ure-id --blocked-by proposal-id\n" +
					"bd dep add proposal-id --blocked-by impl-plan-id\n" +
					"bd dep add impl-plan-id --blocked-by slice-1-id\n" +
					"bd dep add slice-1-id --blocked-by leaf-task-a-id",
			},
		},
	},
	"C-review-consensus": {
		ID:        "C-review-consensus",
		Given:     "review cycle (p4 or p10)",
		When:      "evaluating",
		Then:      "all 3 reviewers must ACCEPT before proceeding",
		ShouldNot: "proceed with any REVISE vote outstanding",
	},
	"C-review-binary": {
		ID:        "C-review-binary",
		Given:     "a reviewer",
		When:      "voting",
		Then:      "use ACCEPT or REVISE only",
		ShouldNot: "use APPROVE, APPROVE_WITH_COMMENTS, REQUEST_CHANGES, or REJECT",
	},
	"C-severity-eager": {
		ID:        "C-severity-eager",
		Given:     "code review round (p10 only)",
		When:      "starting review",
		Then:      "ALWAYS create 3 severity group tasks (BLOCKER, IMPORTANT, MINOR) immediately",
		ShouldNot: "lazily create severity groups only when findings exist",
		Examples: []Example{
			{
				ID:    "C-severity-eager-create",
				Lang:  "bash",
				Label: "correct",
				Code: "# Create all 3 severity groups immediately (even if empty)\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 BLOCKER\" \\\n" +
					"  --labels \"aura:severity:blocker,aura:p10-impl:s10-review\"\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 IMPORTANT\" \\\n" +
					"  --labels \"aura:severity:important,aura:p10-impl:s10-review\"\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 MINOR\" \\\n" +
					"  --labels \"aura:severity:minor,aura:p10-impl:s10-review\"\n\n" +
					"# Close empty groups immediately\n" +
					"bd close <empty-important-id>\n" +
					"bd close <empty-minor-id>",
			},
			{
				ID:    "C-severity-eager-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code: "# WRONG: only creating groups when findings exist\n" +
					"# This skips empty groups and breaks the audit trail\n" +
					"if blocker_findings:\n" +
					"    bd create --title \"BLOCKER\" ...",
			},
		},
	},
	"C-severity-not-plan": {
		ID:        "C-severity-not-plan",
		Given:     "plan review (p4)",
		When:      "reviewing",
		Then:      "use binary ACCEPT/REVISE only",
		ShouldNot: "create severity tree for plan reviews",
	},
	"C-blocker-dual-parent": {
		ID:        "C-blocker-dual-parent",
		Given:     "a BLOCKER finding in code review",
		When:      "recording",
		Then:      "add as child of BOTH the severity group AND the slice it blocks",
		ShouldNot: "add to severity group only",
	},
	"C-followup-timing": {
		ID:        "C-followup-timing",
		Given:     "code review completion with IMPORTANT or MINOR findings",
		When:      "creating follow-up epic",
		Then:      "create immediately upon review completion",
		ShouldNot: "gate follow-up epic on BLOCKER resolution",
	},
	"C-vertical-slices": {
		ID:        "C-vertical-slices",
		Given:     "implementation decomposition",
		When:      "assigning work",
		Then:      "each production code path owned by exactly ONE worker (full vertical)",
		ShouldNot: "assign horizontal layers or same path to multiple workers",
	},
	"C-supervisor-no-impl": {
		ID:        "C-supervisor-no-impl",
		Given:     "supervisor role",
		When:      "implementation phase",
		Then:      "spawn workers for all code changes",
		ShouldNot: "implement code directly",
	},
	"C-supervisor-explore-ephemeral": {
		ID:    "C-supervisor-explore-ephemeral",
		Given: "supervisor needs codebase exploration",
		When:  "starting Phase 8 (IMPL_PLAN)",
		Then: "spawn ephemeral Explore subagents via Task tool for scoped codebase queries; " +
			"each subagent is short-lived and returns findings; no standing team overhead",
		ShouldNot: "explore the codebase directly as supervisor; " +
			"maintain a standing explore team",
	},
	"C-clean-review-exit": {
		ID:    "C-clean-review-exit",
		Given: "per-slice code review",
		When:  "evaluating review results",
		Then: "clean review exit requires 0 BLOCKERs AND 0 IMPORTANTs; " +
			"MINORs are acceptable and tracked in FOLLOWUP epic; " +
			"each slice has its own independent review cycle counter (max 3 cycles); " +
			"after 3 failed cycles, escalate to architect for re-planning",
		ShouldNot: "accept review with open BLOCKERs or IMPORTANTs; " +
			"batch review across multiple slices; " +
			"exceed 3 cycles without escalating; " +
			"escalate to user instead of architect",
	},
	"C-autonomous-progression": {
		ID:    "C-autonomous-progression",
		Given: "supervisor orchestrating phases",
		When:  "deciding whether to proceed",
		Then: "4 user-gated phases only: (1) research depth decision, (2) URE survey, " +
			"(3) Plan UAT, (4) Impl UAT; all other phase transitions are auto-ratified " +
			"by the supervisor; after Plan UAT ACCEPT, proceed directly to ratification " +
			"without user gate",
		ShouldNot: "add additional user gates beyond the 4 defined; " +
			"require user approval for ratification after UAT ACCEPT",
	},
	"C-integration-points": {
		ID:    "C-integration-points",
		Given: "multiple vertical slices share types, interfaces, or data flows",
		When:  "decomposing IMPL_PLAN in Phase 8",
		Then: "identify horizontal Layer Integration Points and document them in IMPL_PLAN; " +
			"each integration point specifies: owning slice, consuming slices, shared contract, merge timing; " +
			"include integration points in slice descriptions so workers know what to export and import",
		ShouldNot: "leave cross-slice dependencies implicit; " +
			"assume workers will discover contracts on their own",
	},
	"C-slice-review-before-close": {
		ID:    "C-slice-review-before-close",
		Given: "workers complete their implementation slices",
		When:  "slice implementation is done",
		Then: "workers notify supervisor with bd comments add (not bd close); " +
			"slices must be reviewed at least once by reviewers before closure; " +
			"only the supervisor closes slices, after review passes",
		ShouldNot: "close slices immediately upon worker completion; " +
			"allow workers to close their own slices",
	},
	"C-max-review-cycles": {
		ID:    "C-max-review-cycles",
		Given: "per-slice review-fix cycles are ongoing",
		When:  "counting review-fix iterations per slice",
		Then: "limit to a maximum of 3 cycles per slice; " +
			"clean review exit = 0 BLOCKERs + 0 IMPORTANTs; " +
			"after cycle 3, escalate to architect for re-planning if BLOCKERs or IMPORTANTs remain; " +
			"remaining IMPORTANT findings move to FOLLOWUP epic",
		ShouldNot: "exceed 3 review cycles per slice; " +
			"escalate to user instead of architect; " +
			"batch review across multiple slices",
	},
	"C-slice-leaf-tasks": {
		ID:    "C-slice-leaf-tasks",
		Given: "vertical slice created",
		When:  "decomposing slice into implementation units",
		Then: "create Beads leaf tasks (L1: types, L2: tests, L3: impl) within each slice " +
			"with bd dep add slice-id --blocked-by leaf-task-id",
		ShouldNot: "create slices without leaf tasks — " +
			"a slice with no children is undecomposed and cannot be tracked",
		Command: "bd dep add <slice-id> --blocked-by <leaf-task-id>",
	},
	"C-handoff-skill-invocation": {
		ID:    "C-handoff-skill-invocation",
		Given: "an agent is launched for a new phase (especially p7 to p8 handoff)",
		When:  "composing the launch prompt",
		Then: "prompt MUST start with Skill(/aura:{role}) invocation directive " +
			"so the agent loads its role instructions",
		ShouldNot: "launch agents without skill invocation — " +
			"they skip role-critical procedures like ephemeral exploration and leaf task creation",
	},
	"C-dep-direction": {
		ID:        "C-dep-direction",
		Given:     "adding a Beads dependency",
		When:      "determining direction",
		Then:      "parent blocked-by child: bd dep add stays-open --blocked-by must-finish-first",
		ShouldNot: "invert (child blocked-by parent)",
		Command:   "bd dep add <stays-open> --blocked-by <must-finish-first>",
		Examples: []Example{
			{
				ID:              "C-dep-direction-correct",
				Lang:            "bash",
				Label:           "correct",
				Code:            "bd dep add request-id --blocked-by ure-id",
				AlsoIllustrates: "C-audit-dep-chain",
			},
			{
				ID:    "C-dep-direction-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code:  "bd dep add ure-id --blocked-by request-id",
			},
		},
	},
	"C-frontmatter-refs": {
		ID:        "C-frontmatter-refs",
		Given:     "cross-task references (URD, request, etc.)",
		When:      "linking tasks",
		Then:      "use description frontmatter references: block",
		ShouldNot: "use bd dep relate (buggy) or blocking dependencies for reference docs",
	},
	"C-agent-commit": {
		ID:        "C-agent-commit",
		Given:     "code is ready to commit",
		When:      "committing",
		Then:      "use git agent-commit -m ...",
		ShouldNot: "use git commit -m ...",
		Command:   "git agent-commit -m ...",
		Examples: []Example{
			{
				ID:    "C-agent-commit-correct",
				Lang:  "bash",
				Label: "correct",
				Code:  `git agent-commit -m "feat: add login"`,
			},
			{
				ID:    "C-agent-commit-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code:  `git commit -m "feat: add login"`,
			},
		},
	},
	"C-proposal-naming": {
		ID:        "C-proposal-naming",
		Given:     "a new or revised proposal",
		When:      "creating task",
		Then:      "title PROPOSAL-{N} where N increments; mark old as aura:superseded",
		ShouldNot: "reuse N or delete old proposals",
	},
	"C-review-naming": {
		ID:        "C-review-naming",
		Given:     "a review task",
		When:      "creating",
		Then:      "title {SCOPE}-REVIEW-{axis}-{round} where axis=A|B|C, round starts at 1",
		ShouldNot: "use numeric reviewer IDs (1/2/3) instead of axis letters",
	},
	"C-ure-verbatim": {
		ID:    "C-ure-verbatim",
		Given: "user interview (Request, URE, or UAT), URD update, or mid-implementation design decision",
		When:  "recording in Beads",
		Then: "capture full question text, ALL option descriptions, AND user's verbatim response; " +
			"the URD is the living document of ALL user requests, URE, UAT, and mid-implementation " +
			"design decisions and feedback — update it via bd comments add whenever user intent is captured",
		ShouldNot: "summarize options as (1)/(2)/(3) without option text, or paraphrase user responses",
		Examples: []Example{
			{
				ID:    "C-ure-verbatim-correct",
				Lang:  "bash",
				Label: "correct",
				Code: "# Full question, all options with descriptions, verbatim response\n" +
					"bd create --title \"UAT: Plan acceptance for feature-X\" \\\n" +
					"  --description \"## Component: Verbose fields\n" +
					"**Question:** Which verbose fields are useful?\n" +
					"**Options:**\n" +
					"- backupDir (full path): Shows where the backup landed\n" +
					"- session ID: Enables log correlation across events\n" +
					"- repo path + hash: Confirms which git repo was detected\n" +
					"**User response:** backupDir (full path), session ID\n" +
					"**Decision:** ACCEPT\"",
			},
			{
				ID:    "C-ure-verbatim-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code: "# WRONG: options summarized as numbers, response paraphrased\n" +
					"bd create --title \"UAT: Plan acceptance\" \\\n" +
					"  --description \"Asked about verbose fields (1-4). User picked 1 and 2. Accepted.\"",
			},
		},
	},
	"C-followup-lifecycle": {
		ID:    "C-followup-lifecycle",
		Given: "follow-up epic created",
		When:  "starting follow-up work",
		Then: "run same protocol phases with FOLLOWUP_* prefix: " +
			"FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE",
		ShouldNot: "skip the follow-up lifecycle or treat the follow-up epic as a flat task list",
	},
	"C-followup-leaf-adoption": {
		ID:    "C-followup-leaf-adoption",
		Given: "supervisor creates FOLLOWUP_SLICE-N",
		When:  "assigning original IMPORTANT/MINOR leaf tasks to follow-up slices",
		Then: "add leaf task as child of follow-up slice " +
			"(dual-parent: leaf blocks both severity group AND follow-up slice)",
		ShouldNot: "remove the leaf task from its original severity group parent",
	},
	"C-worker-gates": {
		ID:        "C-worker-gates",
		Given:     "worker finishes implementation",
		When:      "signaling completion",
		Then:      "run quality gates (typecheck + tests) AND verify production code path (no TODOs, real deps)",
		ShouldNot: "close with only 'tests pass' as completion gate",
	},
	"C-actionable-errors": {
		ID:    "C-actionable-errors",
		Given: "an error, exception, or user-facing message",
		When:  "creating or raising",
		Then: "make it actionable: describe (1) what went wrong, (2) why it happened, " +
			"(3) where it failed (file location, module, or function), " +
			"(4) when it failed (step, operation, or timestamp), " +
			"(5) what it means for the caller, and (6) how to fix it",
		ShouldNot: "raise generic or opaque error messages (e.g. 'invalid input', 'operation failed') " +
			"that don't guide the user toward resolution",
	},
}

// ─── RoleSpecs ────────────────────────────────────────────────────────────────

// RoleSpecs maps each RoleId to its full specification.
// Mirrors Python ROLE_SPECS dict.
var RoleSpecs = map[types.RoleId]RoleSpec{
	types.RoleEpoch: {
		ID:          types.RoleEpoch,
		Name:        "Epoch",
		Description: "Master orchestrator for full 12-phase workflow",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task"},
		OwnedPhases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		Introduction: "You are the master orchestrator for the full 12-phase epoch lifecycle. " +
			"You delegate planning phases (1-7) to the architect and implementation phases (7-12) " +
			"to the supervisor.",
		OwnershipNarrative: "You own the full 12-phase lifecycle from Request to Landing. " +
			"You delegate phases 1-7 to the architect and phases 7-12 to the supervisor. " +
			"The epoch role coordinates the complete workflow end-to-end and is the only role " +
			"that spans all phases.",
	},
	types.RoleArchitect: {
		ID:          types.RoleArchitect,
		Name:        "Architect",
		Description: "Specification writer and implementation designer",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task"},
		OwnedPhases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff,
		},
		Introduction: "You design specifications and coordinate the planning phases of epochs. " +
			"See the project's AGENTS.md and ~/.claude/CLAUDE.md for coding standards and constraints.",
		OwnershipNarrative: "You own Phases 1-7 of the epoch: " +
			"capture and classify user request (p1), " +
			"run requirements elicitation URE survey (p2), " +
			"create PROPOSAL-N with full technical plan (p3), " +
			"spawn 3 axis-specific reviewers and loop until consensus (p4), " +
			"present plan to user for acceptance test (p5), " +
			"add ratify label to accepted PROPOSAL-N (p6), " +
			"create handoff document and transfer to supervisor (p7).",
		Behaviors: []BehaviorSpec{
			{
				ID:        "B-arch-elicit",
				Given:     "user request captured",
				When:      "starting",
				Then:      "run /aura:user-elicit for URE survey",
				ShouldNot: "skip elicitation phase",
			},
			{
				ID:        "B-arch-bdd",
				Given:     "a feature request",
				When:      "writing plan",
				Then:      "use BDD Given/When/Then format with acceptance criteria",
				ShouldNot: "write vague requirements",
			},
			{
				ID:        "B-arch-reviewers",
				Given:     "plan ready",
				When:      "requesting review",
				Then:      "spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)",
				ShouldNot: "spawn reviewers without axis assignment",
			},
			{
				ID:        "B-arch-uat",
				Given:     "consensus reached (all 3 ACCEPT)",
				When:      "proceeding",
				Then:      "run /aura:user-uat before ratifying",
				ShouldNot: "skip user acceptance test",
			},
			{
				ID:        "B-arch-ratify",
				Given:     "UAT passed",
				When:      "ratifying",
				Then:      "add aura:p6-plan:s6-ratify label to PROPOSAL-N",
				ShouldNot: "close or delete the proposal task",
			},
		},
	},
	types.RoleReviewer: {
		ID:          types.RoleReviewer,
		Name:        "Reviewer",
		Description: "End-user alignment reviewer for plans and code",
		Model:       "sonnet",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill"},
		OwnedPhases: []protocol.PhaseId{
			protocol.PhaseReview, protocol.PhaseCodeReview,
		},
		Introduction: "You review from an end-user alignment perspective. " +
			"See the project's protocol/CONSTRAINTS.md for coding standards.",
		OwnershipNarrative: "You participate in two phases: " +
			"Phase 4 (plan review) — evaluate PROPOSAL-N against one axis using binary ACCEPT/REVISE, " +
			"NO severity tree; " +
			"Phase 10 (code review) — review ALL implementation slices against your axis using " +
			"full severity tree (BLOCKER/IMPORTANT/MINOR), EAGER creation of all 3 severity groups.",
		Behaviors: []BehaviorSpec{
			{
				ID:        "B-rev-end-user",
				Given:     "a review assignment",
				When:      "reviewing",
				Then:      "apply end-user alignment criteria",
				ShouldNot: "focus only on technical details",
			},
			{
				ID:        "B-rev-revise-feedback",
				Given:     "issues found",
				When:      "voting",
				Then:      "vote REVISE with specific actionable feedback",
				ShouldNot: "vote REVISE without suggestions",
			},
			{
				ID:        "B-rev-accept",
				Given:     "all criteria met",
				When:      "voting",
				Then:      "vote ACCEPT with brief rationale",
				ShouldNot: "delay consensus unnecessarily",
			},
			{
				ID:        "B-rev-all-slices",
				Given:     "impl review (Phase 10)",
				When:      "assigned",
				Then:      "review ALL slices (not just one)",
				ShouldNot: "skip any slice",
			},
		},
	},
	types.RoleSupervisor: {
		ID:          types.RoleSupervisor,
		Name:        "Supervisor",
		Description: "Task coordinator, spawns workers, manages parallel execution",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task"},
		OwnedPhases: []protocol.PhaseId{
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		Introduction: "You coordinate parallel task execution. " +
			"See the project's AGENTS.md and ~/.claude/CLAUDE.md for coding standards and constraints.",
		OwnershipNarrative: "You own Phases 7-12 of the epoch: " +
			"receive handoff from architect (p7), " +
			"create vertical slice decomposition IMPL_PLAN (p8), " +
			"spawn workers for parallel implementation SLICE-N (p9), " +
			"spawn reviewers for per-slice code review with severity tree (p10), " +
			"coordinate user acceptance test (p11), " +
			"commit, push, and hand off (p12). " +
			"You NEVER implement code directly — all implementation is delegated to workers.",
		Behaviors: []BehaviorSpec{
			{
				ID:        "B-sup-read-context",
				Given:     "handoff received",
				When:      "starting",
				Then:      "read ratified plan, URD, UAT, and elicit tasks for full context",
				ShouldNot: "start without reading all four",
			},
			{
				ID:        "B-sup-model-trivial",
				Given:     "trivial changes (single-file edits, config tweaks, typo fixes)",
				When:      "spawning a worker",
				Then:      "use model: haiku to minimize cost and latency",
				ShouldNot: "use a heavyweight model for trivial work",
			},
			{
				ID:        "B-sup-model-nontrivial",
				Given:     "non-trivial changes (multi-file, architectural, logic-heavy)",
				When:      "spawning a worker",
				Then:      "prefer model: sonnet for the Task tool to ensure quality",
				ShouldNot: "default to haiku for complex work",
			},
			{
				ID:    "B-sup-ride-the-wave",
				Given: "Phase 8-10 execution",
				When:  "starting implementation",
				Then: "follow the Ride the Wave cycle: plan tasks with integration points, " +
					"launch the wave of workers, spawn reviewers for per-slice review " +
					"(clean exit = 0 BLOCKERs + 0 IMPORTANTs), workers fix per-slice with atomic commits, " +
					"max 3 cycles per slice, escalate to architect after cycle 3",
				ShouldNot: "skip any stage; batch review across slices; exceed 3 review cycles per slice",
			},
		},
	},
	types.RoleWorker: {
		ID:          types.RoleWorker,
		Name:        "Worker",
		Description: "Vertical slice implementer (full production code path)",
		Model:       "sonnet",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Edit", "Write"},
		OwnedPhases: []protocol.PhaseId{protocol.PhaseWorkerSlices},
		Introduction: "You own a vertical slice (full production code path from CLI/API entry point " +
			"→ service → types). " +
			"See the project's AGENTS.md and ~/.claude/CLAUDE.md for coding standards and constraints.",
		OwnershipNarrative: "NOT: A single file or horizontal layer (e.g., 'all types' or 'all tests'). " +
			"YES: A full vertical slice (complete production code path end-to-end). " +
			"You own the FEATURE end-to-end, not a layer or file. " +
			"Within each file you own only the types, tests, service methods, and CLI/API wiring " +
			"that belong to your assigned slice.",
		Behaviors: []BehaviorSpec{
			{
				ID:        "B-worker-vertical-ownership",
				Given:     "vertical slice assignment",
				When:      "implementing",
				Then:      "own full production code path (types → tests → impl → wiring)",
				ShouldNot: "implement only horizontal layer",
			},
			{
				ID:        "B-worker-plan-backwards",
				Given:     "production code path",
				When:      "planning",
				Then:      "plan backwards from end point to types",
				ShouldNot: "start with types without knowing the end",
			},
			{
				ID:        "B-worker-test-production-code",
				Given:     "tests",
				When:      "writing",
				Then:      "import actual production code (CLI/API users will run)",
				ShouldNot: "create test-only export or dual code paths",
			},
			{
				ID:        "B-worker-verify-production",
				Given:     "implementation complete",
				When:      "verifying before signaling done",
				Then:      "manually trace the production code path end-to-end (entry point → service → types) to confirm wiring, error handling, and no dead code — beyond what automated gates check",
				ShouldNot: "treat passing tests as sufficient verification without a manual walkthrough",
			},
			{
				ID:        "B-worker-blocker",
				Given:     "a blocker",
				When:      "unable to proceed",
				Then:      "use /aura:worker-blocked with details",
				ShouldNot: "guess or work around",
			},
		},
	},
}

// ─── CommandSpecs ─────────────────────────────────────────────────────────────

// CommandSpecs maps command IDs to their full specifications.
// Mirrors Python COMMAND_SPECS dict.
var CommandSpecs = map[string]CommandSpec{
	"cmd-epoch": {
		ID:          "cmd-epoch",
		Name:        "aura:epoch",
		Description: "Master orchestrator for full 12-phase workflow",
		RoleRef:     types.RoleEpoch,
		Phases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		File: "skills/epoch/SKILL.md",
	},
	"cmd-status": {
		ID:          "cmd-status",
		Name:        "aura:status",
		Description: "Project status and monitoring via Beads queries",
		File:        "skills/status/SKILL.md",
	},
	"cmd-user-request": {
		ID:            "cmd-user-request",
		Name:          "aura:user:request",
		Description:   "Capture user feature request verbatim (Phase 1)",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseRequest},
		File:          "skills/user-request/SKILL.md",
		CreatesLabels: []string{"L-p1s1_1"},
	},
	"cmd-user-elicit": {
		ID:            "cmd-user-elicit",
		Name:          "aura:user:elicit",
		Description:   "User Requirements Elicitation survey (Phase 2)",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseElicit},
		File:          "skills/user-elicit/SKILL.md",
		CreatesLabels: []string{"L-p2s2_1", "L-p2s2_2", "L-urd"},
	},
	"cmd-user-uat": {
		ID:            "cmd-user-uat",
		Name:          "aura:user:uat",
		Description:   "User Acceptance Testing with demonstrative examples",
		Phases:        []protocol.PhaseId{protocol.PhasePlanReview, protocol.PhaseImplUAT},
		File:          "skills/user-uat/SKILL.md",
		CreatesLabels: []string{"L-p5s5", "L-p11s11"},
	},
	"cmd-architect": {
		ID:          "cmd-architect",
		Name:        "aura:architect",
		Description: "Specification writer and implementation designer",
		RoleRef:     types.RoleArchitect,
		Phases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff,
		},
		File: "skills/architect/SKILL.md",
	},
	"cmd-arch-propose": {
		ID:            "cmd-arch-propose",
		Name:          "aura:architect:propose-plan",
		Description:   "Create PROPOSAL-N task with full technical plan",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhasePropose},
		File:          "skills/architect-propose-plan/SKILL.md",
		CreatesLabels: []string{"L-p3s3"},
	},
	"cmd-arch-review": {
		ID:            "cmd-arch-review",
		Name:          "aura:architect:request-review",
		Description:   "Spawn 3 axis-specific reviewers (A/B/C)",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseReview},
		File:          "skills/architect-request-review/SKILL.md",
		CreatesLabels: []string{"L-p4s4"},
	},
	"cmd-arch-ratify": {
		ID:            "cmd-arch-ratify",
		Name:          "aura:architect:ratify",
		Description:   "Ratify proposal, mark old proposals aura:superseded",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseRatify},
		File:          "skills/architect-ratify/SKILL.md",
		CreatesLabels: []string{"L-p6s6", "L-superseded"},
	},
	"cmd-arch-handoff": {
		ID:            "cmd-arch-handoff",
		Name:          "aura:architect:handoff",
		Description:   "Create handoff document and transfer to supervisor",
		RoleRef:       types.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseHandoff},
		File:          "skills/architect-handoff/SKILL.md",
		CreatesLabels: []string{"L-p7s7"},
	},
	"cmd-supervisor": {
		ID:          "cmd-supervisor",
		Name:        "aura:supervisor",
		Description: "Task coordinator, spawns workers, manages parallel execution",
		RoleRef:     types.RoleSupervisor,
		Phases: []protocol.PhaseId{
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		File: "skills/supervisor/SKILL.md",
	},
	"cmd-sup-plan": {
		ID:            "cmd-sup-plan",
		Name:          "aura:supervisor:plan-tasks",
		Description:   "Decompose ratified plan into vertical slices (SLICE-N)",
		RoleRef:       types.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseImplPlan},
		File:          "skills/supervisor-plan-tasks/SKILL.md",
		CreatesLabels: []string{"L-p8s8", "L-p9s9"},
	},
	"cmd-sup-spawn": {
		ID:            "cmd-sup-spawn",
		Name:          "aura:supervisor:spawn-worker",
		Description:   "Launch a worker agent for an assigned slice",
		RoleRef:       types.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/supervisor-spawn-worker/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-sup-track": {
		ID:          "cmd-sup-track",
		Name:        "aura:supervisor:track-progress",
		Description: "Monitor worker status via Beads",
		RoleRef:     types.RoleSupervisor,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices, protocol.PhaseCodeReview},
		File:        "skills/supervisor-track-progress/SKILL.md",
	},
	"cmd-sup-commit": {
		ID:            "cmd-sup-commit",
		Name:          "aura:supervisor:commit",
		Description:   "Atomic commit per completed layer/slice",
		RoleRef:       types.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseLanding},
		File:          "skills/supervisor-commit/SKILL.md",
		CreatesLabels: []string{"L-p12s12"},
	},
	"cmd-worker": {
		ID:          "cmd-worker",
		Name:        "aura:worker",
		Description: "Vertical slice implementer (full production code path)",
		RoleRef:     types.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker/SKILL.md",
	},
	"cmd-work-impl": {
		ID:            "cmd-work-impl",
		Name:          "aura:worker:implement",
		Description:   "Implement assigned vertical slice following TDD layers",
		RoleRef:       types.RoleWorker,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/worker-implement/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-work-complete": {
		ID:          "cmd-work-complete",
		Name:        "aura:worker:complete",
		Description: "Signal slice completion after quality gates pass",
		RoleRef:     types.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker-complete/SKILL.md",
	},
	"cmd-work-blocked": {
		ID:          "cmd-work-blocked",
		Name:        "aura:worker:blocked",
		Description: "Report a blocker to supervisor via Beads",
		RoleRef:     types.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker-blocked/SKILL.md",
	},
	"cmd-reviewer": {
		ID:          "cmd-reviewer",
		Name:        "aura:reviewer",
		Description: "End-user alignment reviewer for plans and code",
		RoleRef:     types.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer/SKILL.md",
	},
	"cmd-rev-plan": {
		ID:            "cmd-rev-plan",
		Name:          "aura:reviewer:review-plan",
		Description:   "Evaluate proposal against one axis (binary ACCEPT/REVISE)",
		RoleRef:       types.RoleReviewer,
		Phases:        []protocol.PhaseId{protocol.PhaseReview},
		File:          "skills/reviewer-review-plan/SKILL.md",
		CreatesLabels: []string{"L-p4s4"},
	},
	"cmd-rev-code": {
		ID:            "cmd-rev-code",
		Name:          "aura:reviewer:review-code",
		Description:   "Review implementation slices with EAGER severity tree",
		RoleRef:       types.RoleReviewer,
		Phases:        []protocol.PhaseId{protocol.PhaseCodeReview},
		File:          "skills/reviewer-review-code/SKILL.md",
		CreatesLabels: []string{"L-p10s10", "L-sev-blocker", "L-sev-import", "L-sev-minor"},
	},
	"cmd-rev-comment": {
		ID:          "cmd-rev-comment",
		Name:        "aura:reviewer:comment",
		Description: "Leave structured review comment via Beads",
		RoleRef:     types.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer-comment/SKILL.md",
	},
	"cmd-rev-vote": {
		ID:          "cmd-rev-vote",
		Name:        "aura:reviewer:vote",
		Description: "Cast ACCEPT or REVISE vote (binary only)",
		RoleRef:     types.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer-vote/SKILL.md",
	},
	"cmd-impl-slice": {
		ID:            "cmd-impl-slice",
		Name:          "aura:impl:slice",
		Description:   "Vertical slice assignment and tracking",
		RoleRef:       types.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/impl-slice/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-impl-review": {
		ID:            "cmd-impl-review",
		Name:          "aura:impl:review",
		Description:   "Code review coordination across all slices (Phase 10)",
		RoleRef:       types.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseCodeReview},
		File:          "skills/impl-review/SKILL.md",
		CreatesLabels: []string{"L-p10s10", "L-sev-blocker", "L-sev-import", "L-sev-minor"},
	},
	"cmd-explore": {
		ID:            "cmd-explore",
		Name:          "aura:explore",
		Description:   "Codebase exploration — find integration points, existing patterns, and related code",
		Phases:        []protocol.PhaseId{protocol.PhaseRequest, protocol.PhaseImplPlan},
		File:          "skills/explore/SKILL.md",
		CreatesLabels: []string{"L-p1s1_3"},
	},
	"cmd-research": {
		ID:            "cmd-research",
		Name:          "aura:research",
		Description:   "Domain research — find standards, prior art, and competing approaches",
		Phases:        []protocol.PhaseId{protocol.PhaseRequest},
		File:          "skills/research/SKILL.md",
		CreatesLabels: []string{"L-p1s1_2"},
	},
}

// ─── HandoffSpecs ─────────────────────────────────────────────────────────────

// HandoffSpecs maps handoff IDs to their full specifications.
// Mirrors Python HANDOFF_SPECS dict.
var HandoffSpecs = map[string]HandoffSpec{
	"h1": {
		ID:           "h1",
		SourceRole:   types.RoleArchitect,
		TargetRole:   types.RoleSupervisor,
		AtPhase:      protocol.PhaseHandoff,
		ContentLevel: "full-provenance",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan",
			"context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h2": {
		ID:           "h2",
		SourceRole:   types.RoleSupervisor,
		TargetRole:   types.RoleWorker,
		AtPhase:      protocol.PhaseWorkerSlices,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan", "impl-plan",
			"slice", "context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h3": {
		ID:           "h3",
		SourceRole:   types.RoleSupervisor,
		TargetRole:   types.RoleReviewer,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan", "impl-plan",
			"context", "key-decisions", "acceptance-criteria",
		},
	},
	"h4": {
		ID:           "h4",
		SourceRole:   types.RoleWorker,
		TargetRole:   types.RoleReviewer,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "impl-plan", "slice",
			"context", "key-decisions", "open-items",
		},
	},
	"h5": {
		ID:           "h5",
		SourceRole:   types.RoleReviewer,
		TargetRole:   types.RoleSupervisor,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal",
			"context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h6": {
		ID:           "h6",
		SourceRole:   types.RoleSupervisor,
		TargetRole:   types.RoleArchitect,
		AtPhase:      protocol.PhasePropose,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "followup-epic", "followup-ure", "followup-urd",
			"context", "key-decisions", "findings-summary", "acceptance-criteria",
		},
	},
}

// ─── FigureSpecs ──────────────────────────────────────────────────────────────

// FigureSpecs maps figure IDs to their full specifications.
// Mirrors Python FIGURE_SPECS dict. Content is loaded at generation time.
var FigureSpecs = map[string]FigureSpec{
	"layer-cake": {
		ID:           "layer-cake",
		Title:        "Layer Cake — TDD Parallelism Within Vertical Slices",
		Type:         "ascii-diagram",
		RoleRefs:     []types.RoleId{types.RoleWorker},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"layer-cake"},
		CommandRefs:  []string{"cmd-sup-plan"},
	},
	"ride-the-wave": {
		ID:           "ride-the-wave",
		Title:        "Ride the Wave — Coordinated Phase 8-10 Execution",
		Type:         "ascii-diagram",
		RoleRefs:     []types.RoleId{types.RoleSupervisor},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"ride-the-wave"},
		CommandRefs:  []string{"cmd-sup-spawn"},
	},
	"architect-state-flow": {
		ID:           "architect-state-flow",
		Title:        "Architect State Flow — Sequential Planning Phases 1-7",
		Type:         "ascii-diagram",
		RoleRefs:     []types.RoleId{types.RoleArchitect},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"architect-state-flow"},
	},
}

// ─── ChecklistSpecs ───────────────────────────────────────────────────────────

// ChecklistSpecs maps "{role}-{gate}" keys to completion checklists.
// Mirrors Python CHECKLIST_SPECS dict.
var ChecklistSpecs = map[string]Checklist{
	"worker-completion": {
		RoleRef: types.RoleWorker,
		Gate:    "completion",
		Items: []ChecklistItem{
			{ID: "CL-worker-no-todos", Text: "No TODO placeholders in CLI/API actions", Required: true},
			{ID: "CL-worker-real-deps", Text: "Real dependencies wired (not mocks in production code)", Required: true},
			{ID: "CL-worker-test-import", Text: "Tests import production code (not test-only export)", Required: true},
			{ID: "CL-worker-no-dual-export", Text: "No dual-export anti-pattern (one code path for tests and production)", Required: true},
			{ID: "CL-worker-quality-gates", Text: "Quality gates pass (typecheck + tests)", Required: true},
			{ID: "CL-worker-production-path", Text: "Production code path verified end-to-end via code inspection", Required: true},
		},
	},
	"worker-slice-closure": {
		RoleRef: types.RoleWorker,
		Gate:    "slice-closure",
		Items: []ChecklistItem{
			{ID: "CL-worker-notified-supervisor", Text: "Supervisor notified via bd comments add (not bd close)", Required: true},
			{ID: "CL-worker-completion-done", Text: "All completion-gate items passed", Required: true},
			{ID: "CL-worker-close-on-review-wave", Text: "Can only close on a review wave, not a worker wave", Required: true},
			{ID: "CL-worker-review-eligible", Text: "Eligible to close only after review by independent agents with no BLOCKERS or IMPORTANT findings", Required: true},
		},
	},
	"supervisor-review-ready": {
		RoleRef: types.RoleSupervisor,
		Gate:    "review-ready",
		Items: []ChecklistItem{
			{ID: "CL-sup-all-slices-notified", Text: "All workers have notified completion via bd comments add", Required: true},
			{ID: "CL-sup-reviewers-assigned", Text: "Ephemeral reviewers spawned for all slices", Required: true},
			{ID: "CL-sup-severity-groups-created", Text: "Severity groups (BLOCKER/IMPORTANT/MINOR) eagerly created per slice", Required: true},
		},
	},
	"supervisor-landing": {
		RoleRef: types.RoleSupervisor,
		Gate:    "landing",
		Items: []ChecklistItem{
			{ID: "CL-sup-all-accept", Text: "All 3 reviewers ACCEPT, no open BLOCKERs", Required: true},
			{ID: "CL-sup-followup-created", Text: "FOLLOWUP epic created if any IMPORTANT/MINOR findings exist", Required: true},
			{ID: "CL-sup-agent-commit", Text: "git agent-commit used (not git commit -m)", Required: true},
			{ID: "CL-sup-tasks-closed", Text: "All upstream tasks closed or dependency-resolved", Required: true},
			{ID: "CL-sup-close-on-review-wave", Text: "Can only close on a review wave, not a worker wave", Required: true},
			{ID: "CL-sup-review-eligible", Text: "Eligible to close only after review by independent agents with no BLOCKERS or IMPORTANT findings", Required: true},
		},
	},
}

// ─── CoordinationCommands ─────────────────────────────────────────────────────

// CoordinationCommands maps command IDs to coordination command specs.
// Mirrors Python COORDINATION_COMMANDS dict.
var CoordinationCommands = map[string]CoordinationCommand{
	"cmd-coord-show": {
		ID:       "cmd-coord-show",
		Action:   "Check task details",
		Template: "bd show <task-id>",
		Shared:   true,
	},
	"cmd-coord-status": {
		ID:       "cmd-coord-status",
		Action:   "Update status",
		Template: "bd update <task-id> --status=in_progress",
		Shared:   true,
	},
	"cmd-coord-comment": {
		ID:       "cmd-coord-comment",
		Action:   "Add progress note",
		Template: `bd comments add <task-id> "Progress: ..."`,
		Shared:   true,
	},
	"cmd-coord-list": {
		ID:       "cmd-coord-list",
		Action:   "List in-progress",
		Template: "bd list --pretty --status=in_progress",
		Shared:   true,
	},
	"cmd-coord-blocked": {
		ID:       "cmd-coord-blocked",
		Action:   "List blocked",
		Template: "bd blocked",
		Shared:   true,
	},
	"cmd-coord-assign": {
		ID:       "cmd-coord-assign",
		Action:   "Assign task",
		Template: `bd update <task-id> --assignee "<worker-name>"`,
		RoleRef:  types.RoleSupervisor,
	},
	"cmd-coord-label": {
		ID:       "cmd-coord-label",
		Action:   "Label completed slice",
		Template: "bd label add <slice-id> aura:p9-impl:slice-complete",
		RoleRef:  types.RoleSupervisor,
	},
	"cmd-coord-dep-add": {
		ID:       "cmd-coord-dep-add",
		Action:   "Chain dependency",
		Template: "bd dep add <parent> --blocked-by <child>",
		RoleRef:  types.RoleSupervisor,
	},
	"cmd-coord-close": {
		ID:       "cmd-coord-close",
		Action:   "Report completion",
		Template: "bd close <task-id>",
		RoleRef:  types.RoleWorker,
	},
	"cmd-coord-worker-notes": {
		ID:       "cmd-coord-worker-notes",
		Action:   "Add completion notes",
		Template: `bd update <task-id> --notes="Implementation complete. Production code verified."`,
		RoleRef:  types.RoleWorker,
	},
}

// ─── WorkflowSpecs ────────────────────────────────────────────────────────────

// WorkflowSpecs maps workflow IDs to their full specifications.
// Mirrors Python WORKFLOW_SPECS dict.
var WorkflowSpecs = map[string]Workflow{
	"ride-the-wave": {
		ID:      "ride-the-wave",
		Name:    "Ride the Wave",
		RoleRef: types.RoleSupervisor,
		Description: "Coordinated Phase 8-10 execution pattern. The supervisor orchestrates " +
			"the full cycle: plan slices, launch workers, " +
			"spawn reviewers for per-slice review, workers fix, repeat max 3 cycles per slice.",
		Stages: []WorkflowStage{
			{
				ID:        "rtw-plan",
				Name:      "Plan",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseImplPlan,
				Actions: []WorkflowAction{
					{
						ID:          "rtw-plan-read",
						Instruction: "Read RATIFIED_PLAN and URD via bd show",
						Command:     "bd show <ratified-plan-id> && bd show <urd-id>",
					},
					{
						ID:          "rtw-plan-explore",
						Instruction: "Spawn ephemeral Explore subagents (`subagent_type=Explore`) for scoped codebase queries — NOT standing teams",
					},
					{
						ID:          "rtw-plan-decompose",
						Instruction: "Use Explore findings to decompose into vertical slices with integration points",
					},
					{
						ID:          "rtw-plan-leaf-tasks",
						Instruction: "Create leaf tasks (L1/L2/L3) for every slice",
						Command:     "bd dep add <slice-id> --blocked-by <leaf-task-id>",
					},
				},
				OperationalDetail: "",
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "All slices created with leaf tasks, dependency-chained, assigned",
					},
				},
			},
			{
				ID:        "rtw-build",
				Name:      "Build",
				Order:     2,
				Execution: "parallel",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						ID: "rtw-build-spawn",
						Instruction: "Spawn workers via the Agent tool — " +
							"set `name` for a named teammate, leave `name` empty for a backgrounded subagent " +
							"(NOT aura-swarm). " +
							"Choose model: sonnet for non-trivial slices, haiku for trivial changes. " +
							"Set thinking effort to match slice complexity.",
					},
					{
						ID:          "rtw-build-monitor",
						Instruction: "Monitor worker progress via bd list and bd show",
						Command:     `bd list --labels="aura:p9-impl:s9-slice" --status=in_progress`,
					},
					{
						ID:          "rtw-build-integrate",
						Instruction: "Supervisor commits at integration points (atomic commits) — commit small, integrate early and often",
					},
				},
				OperationalDetail: "",
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "All workers have notified completion via bd comments add",
					},
				},
			},
			{
				ID:        "rtw-review-fix",
				Name:      "Review + Fix Cycles",
				Order:     3,
				Execution: "conditional-loop",
				PhaseRef:  protocol.PhaseCodeReview,
				Actions: []WorkflowAction{
					{
						ID:          "rtw-review-spawn",
						Instruction: "Spawn reviewers via Task tool for per-slice code review",
					},
					{
						ID:          "rtw-review-severity",
						Instruction: "Reviewers create severity groups (BLOCKER/IMPORTANT/MINOR) per slice",
					},
					{
						ID:          "rtw-review-followup",
						Instruction: "Create FOLLOWUP epic if any IMPORTANT/MINOR findings exist",
					},
					{
						ID:          "rtw-review-fix",
						Instruction: "Workers fix BLOCKERs and IMPORTANT findings",
					},
				},
				OperationalDetail: "- Spawn 3 ephemeral reviewer subagents per round (same pattern as Phase 4 plan review)\n" +
					"- **CLEAN REVIEW** = 0 BLOCKERs + 0 IMPORTANTs from ALL reviewers\n" +
					"- Per-slice fix+review with independent cycle counters per slice\n" +
					"- Fix flow: Stage 3 (dirty review) -> Stage 2 (worker fixes) -> Stage 3 (re-review)\n" +
					"- Max 3 cycles per slice, then escalate to architect for re-planning\n" +
					"- **MUST end on a review wave** — cannot proceed after a worker wave without review\n" +
					"\n" +
					"```text\n" +
					"Stage 3 Flow (per-slice):\n" +
					"\n" +
					"  \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\n" +
					"  \u2502 Spawn 3 ephemeral reviewers             \u2502\n" +
					"  \u2502 Review slice (severity: BLOCKER/IMP/MIN)\u2502\n" +
					"  \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\n" +
					"                 \u2502\n" +
					"          CLEAN? \u251c\u2500\u2500 YES \u2192 slice passes, proceed\n" +
					"                 \u2502\n" +
					"                 \u2514\u2500\u2500 NO (cycle < 3)\n" +
					"                       \u2502\n" +
					"                       \u25bc\n" +
					"              \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\n" +
					"              \u2502 Stage 2: worker    \u2502\n" +
					"              \u2502 fixes BLOCKERs +   \u2502\n" +
					"              \u2502 IMPORTANTs         \u2502\n" +
					"              \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\n" +
					"                       \u2502\n" +
					"                       \u25bc\n" +
					"              \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\n" +
					"              \u2502 Stage 3: re-review \u2502\n" +
					"              \u2502 (new ephemeral     \u2502\n" +
					"              \u2502  reviewers)        \u2502\n" +
					"              \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\n" +
					"                       \u2502\n" +
					"                 cycle++ \u2192 loop\n" +
					"                       \u2502\n" +
					"          3 cycles exhausted \u2192 escalate to architect\n" +
					"```",
				ExitConditions: []ExitCondition{
					{
						Type:      "success",
						Condition: "All reviewers ACCEPT, no open BLOCKERs — proceed to Phase 11 UAT",
					},
					{
						Type:      "continue",
						Condition: "BLOCKERs or IMPORTANTs remain, cycles < 3 per slice — workers fix, spawn new ephemeral reviewers",
					},
					{
						Type:      "proceed",
						Condition: "3 cycles exhausted, IMPORTANT remain — track in FOLLOWUP, proceed to Phase 11",
					},
					{
						Type:      "escalate",
						Condition: "3 cycles exhausted per slice, BLOCKERs remain — escalate to architect for re-planning",
					},
				},
			},
		},
	},
	"layer-cake": {
		ID:      "layer-cake",
		Name:    "Layer Cake",
		RoleRef: types.RoleWorker,
		Description: "TDD layer-by-layer implementation within a vertical slice. " +
			"Worker implements types first, then tests (will fail), " +
			"then production code to make tests pass.",
		Stages: []WorkflowStage{
			{
				ID:        "lc-types",
				Name:      "Types",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						ID:          "lc-types-read",
						Instruction: "Read slice task and identify required types",
						Command:     "bd show <slice-task-id>",
					},
					{
						ID:          "lc-types-define",
						Instruction: "Define types, interfaces, and schemas (no deps) — only types for YOUR slice",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "All required types defined; file imports without error",
					},
				},
			},
			{
				ID:        "lc-tests",
				Name:      "Tests",
				Order:     2,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						ID:          "lc-tests-write",
						Instruction: "Write tests importing production code (CLI/API users will run) — tests WILL fail",
					},
					{
						ID:          "lc-tests-verify-import",
						Instruction: "Verify tests import actual production code, not test-only export",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "Tests written and import production code; typecheck passes; tests fail (expected)",
					},
				},
			},
			{
				ID:        "lc-impl",
				Name:      "Implementation + Wiring",
				Order:     3,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						ID:          "lc-impl-code",
						Instruction: "Implement production code to make Layer 2 tests pass",
					},
					{
						ID:          "lc-impl-wire",
						Instruction: "Wire with real dependencies (not mocks in production code)",
					},
					{
						ID:          "lc-impl-run-tests",
						Instruction: "Run tests — all Layer 2 tests must pass",
					},
					{
						ID:          "lc-impl-commit",
						Instruction: "Commit completed work",
						Command:     "git agent-commit -m ...",
					},
					{
						ID:          "lc-impl-notify",
						Instruction: "Notify supervisor of completion via bd comments add",
						Command:     `bd comments add <slice-id> "Implementation complete"`,
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type: "success",
						Condition: "All tests pass; no TODO placeholders; real deps wired; " +
							"production code path verified via code inspection",
					},
					{
						Type:      "escalate",
						Condition: "Blocker encountered — use /aura:worker-blocked with details",
					},
				},
			},
		},
	},
	"architect-state-flow": {
		ID:      "architect-state-flow",
		Name:    "Architect State Flow",
		RoleRef: types.RoleArchitect,
		Description: "Sequential planning phases 1-7. The architect captures requirements, " +
			"writes proposals, coordinates review consensus, and hands off to supervisor.",
		Stages: []WorkflowStage{
			{
				ID:        "asf-request",
				Name:      "Request",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseRequest,
				Actions: []WorkflowAction{
					{
						ID:          "asf-request-capture",
						Instruction: "Capture user request verbatim via /aura:user-request",
					},
					{
						ID:          "asf-request-classify",
						Instruction: "Classify request along 4 axes: scope, complexity, risk, domain novelty",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "Classification confirmed, research and explore complete",
					},
				},
			},
			{
				ID:        "asf-elicit",
				Name:      "Elicit",
				Order:     2,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseElicit,
				Actions: []WorkflowAction{
					{
						ID:          "asf-elicit-ure",
						Instruction: "Run URE survey with user via /aura:user-elicit",
					},
					{
						ID:          "asf-elicit-urd",
						Instruction: "Create URD as single source of truth for requirements",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "URD created with structured requirements",
					},
				},
			},
			{
				ID:        "asf-propose",
				Name:      "Propose",
				Order:     3,
				Execution: "sequential",
				PhaseRef:  protocol.PhasePropose,
				Actions: []WorkflowAction{
					{
						ID:          "asf-propose-write",
						Instruction: "Write full technical proposal: interfaces, approach, validation checklist, BDD criteria",
					},
					{
						ID:          "asf-propose-create",
						Instruction: "Create PROPOSAL-N task via /aura:architect:propose-plan",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "Proposal created",
					},
				},
			},
			{
				ID:        "asf-review",
				Name:      "Review",
				Order:     4,
				Execution: "conditional-loop",
				PhaseRef:  protocol.PhaseReview,
				Actions: []WorkflowAction{
					{
						ID:          "asf-review-spawn",
						Instruction: "Spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)",
					},
					{
						ID:          "asf-review-wait",
						Instruction: "Wait for all 3 reviewers to vote",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "All 3 reviewers vote ACCEPT",
					},
					{
						Type:      "continue",
						Condition: "Any reviewer votes REVISE — create PROPOSAL-N+1, mark old as superseded, re-spawn reviewers",
					},
				},
			},
			{
				ID:        "asf-uat",
				Name:      "Plan UAT",
				Order:     5,
				Execution: "sequential",
				PhaseRef:  protocol.PhasePlanReview,
				Actions: []WorkflowAction{
					{
						ID:          "asf-uat-present",
						Instruction: "Present plan to user with demonstrative examples via /aura:user-uat",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "User accepts plan",
					},
					{
						Type:      "continue",
						Condition: "User requests changes — create PROPOSAL-N+1",
					},
				},
			},
			{
				ID:        "asf-ratify",
				Name:      "Ratify",
				Order:     6,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseRatify,
				Actions: []WorkflowAction{
					{
						ID:          "asf-ratify-label",
						Instruction: "Add ratify label to accepted PROPOSAL-N",
					},
					{
						ID:          "asf-ratify-supersede",
						Instruction: "Mark all prior proposals aura:superseded",
					},
					{
						ID:          "asf-ratify-placeholder",
						Instruction: "Create placeholder IMPL_PLAN task",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "proceed",
						Condition: "Proposal ratified, IMPL_PLAN placeholder created",
					},
				},
			},
			{
				ID:        "asf-handoff",
				Name:      "Handoff",
				Order:     7,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseHandoff,
				Actions: []WorkflowAction{
					{
						ID:          "asf-handoff-doc",
						Instruction: "Create handoff document with full inline provenance at .git/.aura/handoff/",
					},
					{
						ID:          "asf-handoff-transfer",
						Instruction: "Transfer to supervisor via /aura:architect:handoff",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "success",
						Condition: "Handoff document stored at .git/.aura/handoff/, supervisor notified",
					},
				},
			},
		},
	},
}

// ─── ReviewAxisSpecs ──────────────────────────────────────────────────────────

// ReviewAxisSpecs maps axis IDs to their full specifications.
// Mirrors Python REVIEW_AXIS_SPECS dict.
var ReviewAxisSpecs = map[string]ReviewAxisSpec{
	"axis-correctness": {
		ID:     "axis-correctness",
		Letter: "correctness",
		Name:   "Correctness",
		Short:  "Spirit and technicality",
		KeyQuestions: []string{
			"Does the implementation faithfully serve the user's original request?",
			"Are technical decisions consistent with the rationale in the proposal?",
			"Are there gaps where the proposal says one thing but the code does another?",
		},
	},
	"axis-test_quality": {
		ID:     "axis-test_quality",
		Letter: "test_quality",
		Name:   "Test quality",
		Short:  "Test strategy adequacy",
		KeyQuestions: []string{
			"Favour integration tests over brittle unit tests?",
			"System under test NOT mocked — mock dependencies only?",
			"Shared fixtures for common test values?",
			"Assert observable outcomes, not internal state?",
		},
	},
	"axis-elegance": {
		ID:     "axis-elegance",
		Letter: "elegance",
		Name:   "Elegance",
		Short:  "Complexity matching",
		KeyQuestions: []string{
			"Design the API you know you will need?",
			"No over-engineering (premature abstractions, plugin systems)?",
			"No under-engineering (cutting corners on security or correctness)?",
			"Complexity proportional to innate problem complexity?",
		},
	},
}

// ─── ProcedureSteps ───────────────────────────────────────────────────────────

// ProcedureSteps maps each RoleId to its ordered startup procedure steps.
// Mirrors Python PROCEDURE_STEPS dict.
var ProcedureSteps = map[types.RoleId][]ProcedureStep{
	types.RoleEpoch:     {},
	types.RoleArchitect: {},
	types.RoleReviewer:  {},
	types.RoleSupervisor: {
		{
			ID:          "S-supervisor-call-skill",
			Order:       1,
			Instruction: "Call Skill(/aura:supervisor) to load role instructions",
			Command:     "Skill(/aura:supervisor)",
		},
		{
			ID:          "S-supervisor-read-plan",
			Order:       2,
			Instruction: "Read RATIFIED_PLAN, URD, UAT, and elicit tasks via bd show for full context",
			Command:     "bd show <ratified-plan-id> && bd show <urd-id> && bd show <uat-id> && bd show <elicit-id>",
		},
		{
			ID:          "S-supervisor-explore-ephemeral",
			Order:       3,
			Instruction: "Spawn ephemeral Explore subagents via Task tool for scoped codebase queries",
			Context:     "Each subagent is short-lived and returns findings; no standing team overhead",
		},
		{
			ID:          "S-supervisor-decompose-slices",
			Order:       4,
			Instruction: "Decompose into vertical slices",
			Context: "Vertical slices give one worker end-to-end ownership of a feature path " +
				"(types → tests → impl → wiring) with clear file boundaries",
			NextState: protocol.PhaseImplPlan,
		},
		{
			ID:          "S-supervisor-create-leaf-tasks",
			Order:       5,
			Instruction: "Create leaf tasks (L1/L2/L3) for every slice",
			Command:     `bd create --labels aura:p9-impl:s9-slice --title "SLICE-{K}-L{1,2,3}: <description>" ...`,
			Examples: []Example{
				{
					ID:    "S-supervisor-create-leaf-tasks-frontmatter",
					Lang:  "bash",
					Label: "template",
					Code: "bd create --labels aura:p9-impl:s9-slice \\\n" +
						"  --title \"SLICE-1-L1: Types -- <slice name>\" \\\n" +
						"  --description \"---\n" +
						"references:\n" +
						"  slice: <slice-1-id>\n" +
						"  impl_plan: <impl-plan-task-id>\n" +
						"  urd: <urd-task-id>\n" +
						"---\n" +
						"Layer 1: types and interfaces for <slice name>.\"",
					AlsoIllustrates: "C-frontmatter-refs",
				},
			},
		},
		{
			ID:    "S-supervisor-spawn-workers",
			Order: 6,
			Instruction: "Spawn workers via the Agent tool — " +
				"set `name` for a named teammate, leave `name` empty for a backgrounded subagent " +
				"(NOT aura-swarm). " +
				"Choose model: sonnet for non-trivial slices, haiku for trivial changes. " +
				"Set thinking effort to match slice complexity.",
			NextState: protocol.PhaseWorkerSlices,
		},
	},
	types.RoleWorker: {
		{
			ID:          "S-worker-types",
			Order:       1,
			Instruction: "Types, interfaces, schemas (no deps)",
		},
		{
			ID:          "S-worker-tests",
			Order:       2,
			Instruction: "Tests importing production code (will fail initially)",
		},
		{
			ID:          "S-worker-impl",
			Order:       3,
			Instruction: "Make tests pass. Wire with real dependencies. No TODOs.",
			NextState:   protocol.PhaseWorkerSlices,
		},
	},
}

// ─── LabelSpecs ───────────────────────────────────────────────────────────────

// LabelSpecs maps label IDs to their full specifications.
// Mirrors Python LABEL_SPECS dict.
var LabelSpecs = map[string]LabelSpec{
	"L-p1s1_1":      {ID: "L-p1s1_1", Value: "aura:p1-user:s1_1-classify", Special: false, PhaseRef: "p1", SubstepRef: "s1_1"},
	"L-p1s1_2":      {ID: "L-p1s1_2", Value: "aura:p1-user:s1_2-research", Special: false, PhaseRef: "p1", SubstepRef: "s1_2"},
	"L-p1s1_3":      {ID: "L-p1s1_3", Value: "aura:p1-user:s1_3-explore", Special: false, PhaseRef: "p1", SubstepRef: "s1_3"},
	"L-p2s2_1":      {ID: "L-p2s2_1", Value: "aura:p2-user:s2_1-elicit", Special: false, PhaseRef: "p2", SubstepRef: "s2_1"},
	"L-p2s2_2":      {ID: "L-p2s2_2", Value: "aura:p2-user:s2_2-urd", Special: false, PhaseRef: "p2", SubstepRef: "s2_2"},
	"L-p3s3":        {ID: "L-p3s3", Value: "aura:p3-plan:s3-propose", Special: false, PhaseRef: "p3", SubstepRef: "s3"},
	"L-p4s4":        {ID: "L-p4s4", Value: "aura:p4-plan:s4-review", Special: false, PhaseRef: "p4", SubstepRef: "s4"},
	"L-p5s5":        {ID: "L-p5s5", Value: "aura:p5-user:s5-uat", Special: false, PhaseRef: "p5", SubstepRef: "s5"},
	"L-p6s6":        {ID: "L-p6s6", Value: "aura:p6-plan:s6-ratify", Special: false, PhaseRef: "p6", SubstepRef: "s6"},
	"L-p7s7":        {ID: "L-p7s7", Value: "aura:p7-plan:s7-handoff", Special: false, PhaseRef: "p7", SubstepRef: "s7"},
	"L-p8s8":        {ID: "L-p8s8", Value: "aura:p8-impl:s8-plan", Special: false, PhaseRef: "p8", SubstepRef: "s8"},
	"L-p9s9":        {ID: "L-p9s9", Value: "aura:p9-impl:s9-slice", Special: false, PhaseRef: "p9", SubstepRef: "s9"},
	"L-p10s10":      {ID: "L-p10s10", Value: "aura:p10-impl:s10-review", Special: false, PhaseRef: "p10", SubstepRef: "s10"},
	"L-p11s11":      {ID: "L-p11s11", Value: "aura:p11-user:s11-uat", Special: false, PhaseRef: "p11", SubstepRef: "s11"},
	"L-p12s12":      {ID: "L-p12s12", Value: "aura:p12-impl:s12-landing", Special: false, PhaseRef: "p12", SubstepRef: "s12"},
	"L-urd":         {ID: "L-urd", Value: "aura:urd", Special: true, Description: "User Requirements Document"},
	"L-superseded":  {ID: "L-superseded", Value: "aura:superseded", Special: true, Description: "Superseded proposal or plan"},
	"L-sev-blocker": {ID: "L-sev-blocker", Value: "aura:severity:blocker", Special: true, SeverityRef: "BLOCKER"},
	"L-sev-import":  {ID: "L-sev-import", Value: "aura:severity:important", Special: true, SeverityRef: "IMPORTANT"},
	"L-sev-minor":   {ID: "L-sev-minor", Value: "aura:severity:minor", Special: true, SeverityRef: "MINOR"},
	"L-followup":    {ID: "L-followup", Value: "aura:epic-followup", Special: true, Description: "Follow-up epic for non-blocking findings"},
}

// ─── TitleConventions ─────────────────────────────────────────────────────────

// TitleConventions is the ordered list of task title naming conventions.
// Mirrors Python TITLE_CONVENTIONS list.
// Note: In Go we use a slice (not a map) to preserve order, matching Python.
var TitleConventions = []TitleConvention{
	{Pattern: "REQUEST: {description}", LabelRef: "L-p1s1_1", CreatedBy: "epoch,architect", PhaseRef: "p1"},
	{Pattern: "ELICIT: {description}", LabelRef: "L-p2s2_1", CreatedBy: "architect", PhaseRef: "p2"},
	{Pattern: "URD: {description}", LabelRef: "L-p2s2_2", CreatedBy: "architect", PhaseRef: "p2", ExtraLabelRef: "L-urd"},
	{
		Pattern: "PROPOSAL-{N}: {description}", LabelRef: "L-p3s3", CreatedBy: "architect", PhaseRef: "p3",
		Note: "N increments per revision. Old proposals marked aura:superseded.",
	},
	{
		Pattern: "PROPOSAL-{N}-REVIEW-{axis}-{round}: {description}", LabelRef: "L-p4s4", CreatedBy: "reviewer", PhaseRef: "p4",
		Note: "axis=A|B|C, round starts at 1",
	},
	{Pattern: "UAT-{N}: {description}", LabelRef: "L-p5s5", CreatedBy: "architect", PhaseRef: "p5"},
	{Pattern: "IMPL_PLAN: {description}", LabelRef: "L-p8s8", CreatedBy: "supervisor", PhaseRef: "p8"},
	{
		Pattern: "SLICE-{N}: {description}", LabelRef: "L-p9s9", CreatedBy: "supervisor", PhaseRef: "p9",
		Note: "N identifies slice within the implementation plan",
	},
	{
		Pattern: "SLICE-{N}-REVIEW-{axis}-{round}: {description}", LabelRef: "L-p10s10", CreatedBy: "reviewer", PhaseRef: "p10",
		Note: "axis=A|B|C, round starts at 1",
	},
	{
		Pattern: "IMPL-REVIEW-{axis}-{round}: {description}", LabelRef: "L-p10s10", CreatedBy: "supervisor", PhaseRef: "p10",
		Note: "When reviewing all slices collectively",
	},
	{
		Pattern: "FOLLOWUP: {description}", LabelRef: "L-followup", CreatedBy: "supervisor",
		Note: "Follow-up epic created after code review with IMPORTANT/MINOR findings. " +
			"Single-parent epic relationship — no followup-of-followup.",
	},
	{
		Pattern: "FOLLOWUP_URE: {description}", LabelRef: "L-p2s2_1", CreatedBy: "supervisor", PhaseRef: "p2",
		Note: "Scoping URE to determine which IMPORTANT/MINOR findings to address",
	},
	{
		Pattern: "FOLLOWUP_URD: {description}", LabelRef: "L-p2s2_2", CreatedBy: "supervisor", PhaseRef: "p2",
		ExtraLabelRef: "L-urd",
		Note:          "Requirements doc for follow-up scope. References original URD.",
	},
	{
		Pattern: "FOLLOWUP_PROPOSAL-{N}: {description}", LabelRef: "L-p3s3", CreatedBy: "architect", PhaseRef: "p3",
		Note: "Proposal accounting for original URD + FOLLOWUP_URD + outstanding findings",
	},
	{
		Pattern: "FOLLOWUP_IMPL_PLAN: {description}", LabelRef: "L-p8s8", CreatedBy: "supervisor", PhaseRef: "p8",
		Note: "Implementation plan for follow-up slices",
	},
	{
		Pattern: "FOLLOWUP_SLICE-{N}: {description}", LabelRef: "L-p9s9", CreatedBy: "supervisor", PhaseRef: "p9",
		Note: "Follow-up slice. Adopts IMPORTANT/MINOR leaf tasks from original review as children " +
			"(dual-parent: leaf blocks both original severity group AND follow-up slice).",
	},
}

// ─── SubstepDataMap ───────────────────────────────────────────────────────────

// SubstepDataMap maps phase ID strings to their ordered substep data.
// Mirrors Python SUBSTEP_DATA dict.
var SubstepDataMap = map[string][]SubstepData{
	"p1": {
		{
			ID: "s1_1", Type: "classify", Execution: "sequential", Order: 1,
			LabelRef:    "L-p1s1_1",
			Description: "Classify request along 4 axes: scope, complexity, risk, domain novelty",
		},
		{
			ID: "s1_2", Type: "research", Execution: "parallel", Order: 2,
			ParallelGroup: "p1-discovery", LabelRef: "L-p1s1_2",
			Description: "Find domain standards, prior art, relevant documentation",
		},
		{
			ID: "s1_3", Type: "explore", Execution: "parallel", Order: 2,
			ParallelGroup: "p1-discovery", LabelRef: "L-p1s1_3",
			Description: "Codebase exploration for integration points",
		},
	},
	"p2": {
		{
			ID: "s2_1", Type: "elicit", Execution: "sequential", Order: 1,
			LabelRef:    "L-p2s2_1",
			Description: "URE survey: structured Q&A with user to capture requirements",
		},
		{
			ID: "s2_2", Type: "urd", Execution: "sequential", Order: 2,
			LabelRef:    "L-p2s2_2",
			Description: "Create URD as single source of truth for requirements",
			ExtraLabel:  "L-urd",
		},
	},
	"p3": {
		{
			ID: "s3", Type: "propose", Execution: "sequential", Order: 1,
			LabelRef:    "L-p3s3",
			Description: "Full technical proposal: interfaces, approach, validation checklist, BDD criteria",
		},
	},
	"p4": {
		{
			ID: "s4", Type: "review", Execution: "parallel", Order: 1,
			LabelRef:    "L-p4s4",
			Description: "Each reviewer assesses one axis (A/B/C). All 3 must ACCEPT.",
			Instances:   &SubstepInstances{Count: "3", Per: "review-axis"},
		},
	},
	"p5": {
		{
			ID: "s5", Type: "uat", Execution: "sequential", Order: 1,
			LabelRef:    "L-p5s5",
			Description: "Present plan to user with demonstrative examples. User approves or requests changes.",
		},
	},
	"p6": {
		{
			ID: "s6", Type: "ratify", Execution: "sequential", Order: 1,
			LabelRef:    "L-p6s6",
			Description: "Add ratify label. Mark prior proposals aura:superseded. Create placeholder IMPL_PLAN.",
		},
	},
	"p7": {
		{
			ID: "s7", Type: "handoff", Execution: "sequential", Order: 1,
			LabelRef:    "L-p7s7",
			Description: "Create handoff document with full inline provenance. Transfer to supervisor.",
		},
	},
	"p8": {
		{
			ID: "s8", Type: "plan", Execution: "sequential", Order: 1,
			LabelRef:        "L-p8s8",
			Description:     "Identify production code paths. Create SLICE-N tasks with leaf tasks. Assign workers.",
			StartupSequence: true,
		},
	},
	"p9": {
		{
			ID: "s9", Type: "slice", Execution: "parallel", Order: 1,
			LabelRef:    "L-p9s9",
			Description: "Each worker owns full vertical: types, tests, implementation, wiring",
			Instances:   &SubstepInstances{Count: "N", Per: "production-code-path"},
		},
	},
	"p10": {
		{
			ID: "s10", Type: "review", Execution: "parallel", Order: 1,
			LabelRef:    "L-p10s10",
			Description: "Each reviewer reviews ALL slices against their axis. EAGER severity tree.",
			Instances:   &SubstepInstances{Count: "3", Per: "review-axis"},
		},
	},
	"p11": {
		{
			ID: "s11", Type: "uat", Execution: "sequential", Order: 1,
			LabelRef:    "L-p11s11",
			Description: "Present implementation to user. User approves or requests fixes.",
		},
	},
	"p12": {
		{
			ID: "s12", Type: "landing", Execution: "sequential", Order: 1,
			LabelRef:    "L-p12s12",
			Description: "git agent-commit, bd sync, git push. Close upstream tasks.",
		},
	},
}

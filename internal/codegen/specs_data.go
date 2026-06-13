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
		Id:         protocol.PhaseRequest,
		Name:       "Request",
		Number:     1,
		Domain:     types.DomainUser,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseElicit,
				Condition: "classification confirmed, research and explore complete",
			},
		},
	},
	protocol.PhaseElicit: {
		Id:         protocol.PhaseElicit,
		Name:       "Elicit",
		Number:     2,
		Domain:     types.DomainUser,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhasePropose,
				Condition: "URD created with structured requirements",
			},
		},
	},
	protocol.PhasePropose: {
		Id:         protocol.PhasePropose,
		Name:       "Propose",
		Number:     3,
		Domain:     types.DomainPlan,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseReview,
				Condition: "proposal created",
			},
		},
	},
	protocol.PhaseReview: {
		Id:         protocol.PhaseReview,
		Name:       "Review",
		Number:     4,
		Domain:     types.DomainPlan,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect, protocol.RoleReviewer},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhasePlanReview,
				Condition: "all 3 reviewers vote ACCEPT",
			},
			{
				ToPhase:   protocol.PhasePropose,
				Condition: "any reviewer votes REVISE",
				Action:    "create PROPOSAL-{N+1}, mark current pasture:superseded",
			},
		},
	},
	protocol.PhasePlanReview: {
		Id:         protocol.PhasePlanReview,
		Name:       "Plan UAT",
		Number:     5,
		Domain:     types.DomainUser,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect},
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
		Id:         protocol.PhaseRatify,
		Name:       "Ratify",
		Number:     6,
		Domain:     types.DomainPlan,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseHandoff,
				Condition: "proposal ratified, IMPL_PLAN placeholder created",
			},
		},
	},
	protocol.PhaseHandoff: {
		Id:         protocol.PhaseHandoff,
		Name:       "Handoff",
		Number:     7,
		Domain:     types.DomainPlan,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleArchitect, protocol.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseImplPlan,
				Condition: "handoff authored in the HANDOFF Beads task body",
			},
		},
	},
	protocol.PhaseImplPlan: {
		Id:         protocol.PhaseImplPlan,
		Name:       "Impl Plan",
		Number:     8,
		Domain:     types.DomainImpl,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleSupervisor},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseWorkerSlices,
				Condition: "all slices created with leaf tasks, assigned, and dependency-chained",
			},
		},
	},
	protocol.PhaseWorkerSlices: {
		Id:         protocol.PhaseWorkerSlices,
		Name:       "Worker Slices",
		Number:     9,
		Domain:     types.DomainImpl,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleSupervisor, protocol.RoleWorker},
		Transitions: []Transition{
			{
				ToPhase:   protocol.PhaseCodeReview,
				Condition: "all slices complete, quality gates pass",
			},
		},
	},
	protocol.PhaseCodeReview: {
		Id:         protocol.PhaseCodeReview,
		Name:       "Code Review",
		Number:     10,
		Domain:     types.DomainImpl,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleSupervisor, protocol.RoleReviewer},
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
		Id:         protocol.PhaseImplUAT,
		Name:       "Impl UAT",
		Number:     11,
		Domain:     types.DomainUser,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleSupervisor},
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
		Id:         protocol.PhaseLanding,
		Name:       "Landing",
		Number:     12,
		Domain:     types.DomainImpl,
		OwnerRoles: []protocol.RoleId{protocol.RoleEpoch, protocol.RoleSupervisor},
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
		Id:        "C-audit-never-delete",
		Given:     "any task or label",
		When:      "modifying",
		Then:      "add labels and comments only",
		ShouldNot: "delete or close tasks prematurely, remove labels",
	},
	"C-audit-dep-chain": {
		Id:        "C-audit-dep-chain",
		Given:     "any phase transition",
		When:      "creating new task",
		Then:      "chain dependency: bd dep add parent --blocked-by child",
		ShouldNot: "skip dependency chaining or invert direction",
		Command:   "bd dep add <parent> --blocked-by <child>",
		Examples: []Example{
			{
				Id:    "C-audit-dep-chain-full",
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
		Id:        "C-review-consensus",
		Given:     "review cycle (p4 or p10)",
		When:      "evaluating",
		Then:      "all 3 reviewers must ACCEPT before proceeding",
		ShouldNot: "proceed with any REVISE vote outstanding",
	},
	"C-review-binary": {
		Id:        "C-review-binary",
		Given:     "a reviewer",
		When:      "voting",
		Then:      "use ACCEPT or REVISE only",
		ShouldNot: "use APPROVE, APPROVE_WITH_COMMENTS, REQUEST_CHANGES, or REJECT",
	},
	"C-severity-eager": {
		Id:        "C-severity-eager",
		Given:     "code review round (p10 only)",
		When:      "starting review",
		Then:      "ALWAYS create 3 severity group tasks (BLOCKER, IMPORTANT, MINOR) immediately",
		ShouldNot: "lazily create severity groups only when findings exist",
		Examples: []Example{
			{
				Id:    "C-severity-eager-create",
				Lang:  "bash",
				Label: "correct",
				Code: "# Create all 3 severity groups immediately (even if empty)\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 BLOCKER\" \\\n" +
					"  --labels \"pasture:severity:blocker,pasture:p10-impl:s10-review\"\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 IMPORTANT\" \\\n" +
					"  --labels \"pasture:severity:important,pasture:p10-impl:s10-review\"\n" +
					"bd create --title \"SLICE-1-REVIEW-A-1 MINOR\" \\\n" +
					"  --labels \"pasture:severity:minor,pasture:p10-impl:s10-review\"\n\n" +
					"# Close empty groups immediately\n" +
					"bd close <empty-important-id>\n" +
					"bd close <empty-minor-id>",
			},
			{
				Id:    "C-severity-eager-anti",
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
		Id:        "C-severity-not-plan",
		Given:     "plan review (p4)",
		When:      "reviewing",
		Then:      "use binary ACCEPT/REVISE only",
		ShouldNot: "create severity tree for plan reviews",
	},
	"C-blocker-dual-parent": {
		Id:        "C-blocker-dual-parent",
		Given:     "a BLOCKER finding in code review",
		When:      "recording",
		Then:      "add as child of BOTH the severity group AND the slice it blocks",
		ShouldNot: "add to severity group only",
	},
	"C-followup-timing": {
		Id:    "C-followup-timing",
		Given: "UAT (Phase 5 or Phase 11) produces one or more user-DEFER'd items",
		When:  "creating the FOLLOWUP epic",
		Then: "create the FOLLOWUP epic at UAT when user-DEFER'd items exist; " +
			"the FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items",
		ShouldNot: "trigger FOLLOWUP from any review severity (BLOCKER/IMPORTANT/MINOR) — " +
			"all review findings must reach 0 before wave close, no severity is deferrable to FOLLOWUP",
	},
	"C-vertical-slices": {
		Id:        "C-vertical-slices",
		Given:     "implementation decomposition",
		When:      "assigning work",
		Then:      "each production code path owned by exactly ONE worker (full vertical)",
		ShouldNot: "assign horizontal layers or same path to multiple workers",
	},
	"C-supervisor-no-impl": {
		Id:        "C-supervisor-no-impl",
		Given:     "supervisor role",
		When:      "implementation phase",
		Then:      "spawn workers for all code changes",
		ShouldNot: "implement code directly",
	},
	"C-supervisor-explore-ephemeral": {
		Id:    "C-supervisor-explore-ephemeral",
		Given: "supervisor needs codebase exploration",
		When:  "starting Phase 8 (IMPL_PLAN)",
		Then: "spawn ephemeral Explore subagents via Task tool for scoped codebase queries; " +
			"each subagent is short-lived and returns findings; no standing team overhead",
		ShouldNot: "explore the codebase directly as supervisor; " +
			"maintain a standing explore team",
	},
	"C-clean-review-exit": {
		Id:    "C-clean-review-exit",
		Given: "per-slice code review",
		When:  "evaluating review results",
		Then: "iterate review -> fix -> re-review up to the chosen review-effort budget until a fix-free clean round " +
			"confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR within budget; " +
			"a clean round is one where the re-review applies no fixes and finds nothing across all three severities; " +
			"on budget exhaustion without a clean round, SURFACE the outstanding findings to the user at a gate for a decision",
		ShouldNot: "close a wave on a fix-applying round; " +
			"proceed with ANY finding (BLOCKER, IMPORTANT, or MINOR) outstanding without surfacing it to the user; " +
			"hardcode the budget; proceed past the chosen budget without surfacing to the user; " +
			"batch review across multiple slices",
	},
	"C-review-effort-budget": {
		Id:    "C-review-effort-budget",
		Given: "the start of Phase 8 (IMPL_PLAN), like the Phase-1 research-depth gate",
		When:  "deciding how much review-and-fix effort to spend per slice",
		Then: "request a configurable review-effort budget from the user — defaults: " +
			"(1) three rounds, (2) one round, (3) zero rounds, (4) unlimited, (5) custom; " +
			"the review->fix->re-review loop iterates up to the chosen budget; " +
			"on budget exhaustion WITHOUT a clean 0/0/0 round, surface the outstanding findings to the user for a decision",
		ShouldNot: "hardcode the review-cycle budget (e.g. an unconditional fixed cap baked into the prose instead of asked); " +
			"proceed past the chosen budget without surfacing outstanding findings to the user; " +
			"loop forever when a finite budget was chosen",
	},
	"C-autonomous-progression": {
		Id:    "C-autonomous-progression",
		Given: "supervisor orchestrating phases",
		When:  "deciding whether to proceed",
		Then: "5 user-gated phases only: (1) research depth decision, (2) URE survey, " +
			"(3) Plan UAT, (4) Phase 8 implementation-effort / review-effort budget request, " +
			"(5) Impl UAT; all other phase transitions are auto-ratified " +
			"by the supervisor; after Plan UAT ACCEPT, proceed directly to ratification " +
			"without user gate",
		ShouldNot: "add additional user gates beyond the 5 defined; " +
			"require user approval for ratification after UAT ACCEPT",
	},
	"C-integration-points": {
		Id:    "C-integration-points",
		Given: "multiple vertical slices share types, interfaces, or data flows",
		When:  "decomposing IMPL_PLAN in Phase 8",
		Then: "identify horizontal Layer Integration Points and document them in IMPL_PLAN; " +
			"each integration point specifies: owning slice, consuming slices, shared contract, merge timing; " +
			"include integration points in slice descriptions so workers know what to export and import",
		ShouldNot: "leave cross-slice dependencies implicit; " +
			"assume workers will discover contracts on their own",
	},
	"C-slice-review-before-close": {
		Id:    "C-slice-review-before-close",
		Given: "workers complete their implementation slices",
		When:  "slice implementation is done",
		Then: "workers notify supervisor with bd comments add (not bd close); " +
			"slices must be reviewed at least once by reviewers before closure; " +
			"only the supervisor closes slices, after review passes",
		ShouldNot: "close slices immediately upon worker completion; " +
			"allow workers to close their own slices",
	},
	"C-slice-leaf-tasks": {
		Id:    "C-slice-leaf-tasks",
		Given: "vertical slice created",
		When:  "decomposing slice into implementation units",
		Then: "create one or more Beads leaf tasks per slice, named after the real work units they represent, " +
			"with bd dep add slice-id --blocked-by leaf-task-id; a slice may have ANY number of leaves " +
			"(the L1: types / L2: tests / L3: impl triple is ONE illustrative shape, not a required count)",
		ShouldNot: "create slices without leaf tasks — " +
			"a slice with no children is undecomposed and cannot be tracked; " +
			"force every slice into a fixed L1/L2/L3 triple when the real work units differ",
		Command: "bd dep add <slice-id> --blocked-by <leaf-task-id>",
	},
	"C-handoff-skill-invocation": {
		Id:    "C-handoff-skill-invocation",
		Given: "an agent is launched for a new phase (especially p7 to p8 handoff)",
		When:  "composing the launch prompt",
		Then: "prompt MUST start with Skill(/pasture:{role}) invocation directive " +
			"so the agent loads its role instructions",
		ShouldNot: "launch agents without skill invocation — " +
			"they skip role-critical procedures like ephemeral exploration and leaf task creation",
	},
	"C-dep-direction": {
		Id:        "C-dep-direction",
		Given:     "adding a Beads dependency",
		When:      "determining direction",
		Then:      "parent blocked-by child: bd dep add stays-open --blocked-by must-finish-first",
		ShouldNot: "invert (child blocked-by parent)",
		Command:   "bd dep add <stays-open> --blocked-by <must-finish-first>",
		Examples: []Example{
			{
				Id:              "C-dep-direction-correct",
				Lang:            "bash",
				Label:           "correct",
				Code:            "bd dep add request-id --blocked-by ure-id",
				AlsoIllustrates: "C-audit-dep-chain",
			},
			{
				Id:    "C-dep-direction-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code:  "bd dep add ure-id --blocked-by request-id",
			},
		},
	},
	"C-frontmatter-refs": {
		Id:        "C-frontmatter-refs",
		Given:     "cross-task references (URD, request, etc.)",
		When:      "linking tasks",
		Then:      "use description frontmatter references: block",
		ShouldNot: "use bd dep relate (buggy) or blocking dependencies for reference docs",
	},
	"C-agent-commit": {
		Id:        "C-agent-commit",
		Given:     "code is ready to commit",
		When:      "committing",
		Then:      "use git agent-commit -m ...",
		ShouldNot: "use git commit -m ...",
		Command:   "git agent-commit -m ...",
		Examples: []Example{
			{
				Id:    "C-agent-commit-correct",
				Lang:  "bash",
				Label: "correct",
				Code:  `git agent-commit -m "feat: add login"`,
			},
			{
				Id:    "C-agent-commit-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code:  `git commit -m "feat: add login"`,
			},
		},
	},
	"C-proposal-naming": {
		Id:        "C-proposal-naming",
		Given:     "a new or revised proposal",
		When:      "creating task",
		Then:      "title PROPOSAL-{N} where N increments; mark old as pasture:superseded",
		ShouldNot: "reuse N or delete old proposals",
	},
	"C-review-naming": {
		Id:        "C-review-naming",
		Given:     "a review task",
		When:      "creating",
		Then:      "title {SCOPE}-REVIEW-{axis}-{round} where axis=A|B|C, round starts at 1",
		ShouldNot: "use numeric reviewer IDs (1/2/3) instead of axis letters",
	},
	"C-ure-verbatim": {
		Id:    "C-ure-verbatim",
		Given: "user interview (Request, URE, or UAT), URD update, or mid-implementation design decision",
		When:  "recording in Beads",
		Then: "capture full question text, ALL option descriptions, AND user's verbatim response, " +
			"INCLUDING any code, snippets, or examples shown inside AskUserQuestion option labels, descriptions, " +
			"or definition blocks (the preview/stimulus the user actually saw); " +
			"the URD is the living document of ALL user requests, URE, UAT, and mid-implementation " +
			"design decisions and feedback — update it via bd comments add whenever user intent is captured",
		ShouldNot: "summarize options as (1)/(2)/(3) without option text, paraphrase user responses, " +
			"or omit code/snippets shown inside option previews",
		Examples: []Example{
			{
				Id:    "C-ure-verbatim-correct",
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
				Id:    "C-ure-verbatim-anti",
				Lang:  "bash",
				Label: "anti-pattern",
				Code: "# WRONG: options summarized as numbers, response paraphrased\n" +
					"bd create --title \"UAT: Plan acceptance\" \\\n" +
					"  --description \"Asked about verbose fields (1-4). User picked 1 and 2. Accepted.\"",
			},
		},
	},
	"C-followup-lifecycle": {
		Id:    "C-followup-lifecycle",
		Given: "follow-up epic created",
		When:  "starting follow-up work",
		Then: "run same protocol phases with FOLLOWUP_* prefix: " +
			"FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE",
		ShouldNot: "skip the follow-up lifecycle or treat the follow-up epic as a flat task list",
	},
	"C-followup-leaf-adoption": {
		Id:    "C-followup-leaf-adoption",
		Given: "supervisor creates FOLLOWUP_SLICE-N",
		When:  "assigning user-DEFER'd UAT-item leaf tasks to follow-up slices",
		Then: "add leaf task as child of follow-up slice " +
			"(dual-parent: leaf blocks both the DEFER'd-items tracking group AND follow-up slice)",
		ShouldNot: "remove the leaf task from its original DEFER'd-items tracking parent",
	},
	"C-worker-gates": {
		Id:        "C-worker-gates",
		Given:     "worker finishes implementation",
		When:      "signaling completion",
		Then:      "run quality gates (typecheck + tests) AND verify production code path (no TODOs, real deps)",
		ShouldNot: "close with only 'tests pass' as completion gate",
	},
	"C-actionable-errors": {
		Id:    "C-actionable-errors",
		Given: "an error, exception, or user-facing message",
		When:  "creating or raising",
		Then: "make it actionable: describe (1) what went wrong, (2) why it happened, " +
			"(3) where it failed (file location, module, or function), " +
			"(4) when it failed (step, operation, or timestamp), " +
			"(5) what it means for the caller, and (6) how to fix it",
		ShouldNot: "raise generic or opaque error messages (e.g. 'invalid input', 'operation failed') " +
			"that don't guide the user toward resolution",
	},
	"C-validation-cases": {
		Id:    "C-validation-cases",
		Given: "any REQUEST (every request, not only fix-intent ones)",
		When:  "eliciting (URE), acceptance-testing (UAT), or implementing",
		Then: "elicit concrete validation cases for the request — a definition of done plus correct and " +
			"incorrect behaviours (inputs/behaviors that must pass or must fail), " +
			"confirm the case set with the user in UAT, evaluate the implementation against them, " +
			"and store failing real-data cases as test fixtures",
		ShouldNot: "ship without validation cases; " +
			"treat validation cases as applying to fix-intent requests only; " +
			"introduce a request-type axis or enum to gate them (recognize what a request needs semantically instead)",
	},
	"C-interview-skill-invocation": {
		Id:    "C-interview-skill-invocation",
		Given: "a user-interview phase (Phase 2 URE, Phase 5 Plan UAT, or Phase 11 Impl UAT)",
		When:  "conducting the phase",
		Then: "the agent MUST invoke the matching interview skill (Skill(/pasture:user-elicit) for URE, " +
			"Skill(/pasture:user-uat) for UAT) so the verbatim-capture and disposition procedures are loaded",
		ShouldNot: "conduct an interview phase without invoking its skill — " +
			"skipping it loses the verbatim-capture and FIX-NOW/DEFER disposition procedures",
	},
	"C-uat-feedback-disposition": {
		Id:    "C-uat-feedback-disposition",
		Given: "any UAT feedback item (Phase 5 or Phase 11) — flagged by the user OR a deferral proposed by the architect/supervisor",
		When:  "recording each item",
		Then: "assign every item an explicit, user-confirmed disposition of FIX-NOW or DEFER; " +
			"deferrals may be agent-proposed, but ALL deferred items — whoever proposed them — MUST be raised to the user " +
			"at the next user gate (URE, Plan UAT, or Impl UAT) for confirmation; " +
			"FIX-NOW items are resolved in the current wave, DEFER'd items are the SOLE source feeding the FOLLOWUP epic",
		ShouldNot: "leave a feedback item without a confirmed disposition; " +
			"silently defer any item without raising it to the user at the next gate; " +
			"route any review severity (BLOCKER/IMPORTANT/MINOR) into FOLLOWUP — only DEFER'd UAT items feed it",
	},
	"C-interface-first-slices": {
		Id:    "C-interface-first-slices",
		Given: "decomposing a RATIFIED plan into vertical slices (Phase 8 IMPL_PLAN)",
		When:  "ordering the slices",
		Then: "prefer an interface-first FOUNDATION slice that exports all shared identifiers " +
			"(types, constraints, fragments) and lands green BEFORE dependent slices (Strong SHOULD); " +
			"if a linear (non-interface-first) decomposition is chosen instead, justify it explicitly in the IMPL_PLAN",
		ShouldNot: "leave cross-slice contracts implicit; " +
			"choose a linear decomposition without recording the justification in the IMPL_PLAN",
	},
}

// ─── RoleSpecs ────────────────────────────────────────────────────────────────

// RoleSpecs maps each RoleId to its full specification.
// Mirrors Python ROLE_SPECS dict.
var RoleSpecs = map[protocol.RoleId]RoleSpec{
	protocol.RoleEpoch: {
		Id:          protocol.RoleEpoch,
		Name:        "Epoch",
		Description: "Master orchestrator for full 12-phase workflow",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task", "SendMessage"},
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
	protocol.RoleArchitect: {
		Id:          protocol.RoleArchitect,
		Name:        "Architect",
		Description: "Specification writer and implementation designer",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task", "SendMessage"},
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
				Id:        "B-arch-elicit",
				Given:     "user request captured",
				When:      "starting",
				Then:      "run /pasture:user-elicit for URE survey",
				ShouldNot: "skip elicitation phase",
			},
			{
				Id:        "B-arch-bdd",
				Given:     "a feature request",
				When:      "writing plan",
				Then:      "use BDD Given/When/Then format with acceptance criteria",
				ShouldNot: "write vague requirements",
			},
			{
				Id:        "B-arch-reviewers",
				Given:     "plan ready",
				When:      "requesting review",
				Then:      "spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)",
				ShouldNot: "spawn reviewers without axis assignment",
			},
			{
				Id:        "B-arch-uat",
				Given:     "consensus reached (all 3 ACCEPT)",
				When:      "proceeding",
				Then:      "run /pasture:user-uat before ratifying",
				ShouldNot: "skip user acceptance test",
			},
			{
				Id:        "B-arch-ratify",
				Given:     "UAT passed",
				When:      "ratifying",
				Then:      "add pasture:p6-plan:s6-ratify label to PROPOSAL-N",
				ShouldNot: "close or delete the proposal task",
			},
		},
	},
	protocol.RoleReviewer: {
		Id:          protocol.RoleReviewer,
		Name:        "Reviewer",
		Description: "End-user alignment reviewer for plans and code",
		Model:       "sonnet",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "SendMessage"},
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
				Id:        "B-rev-end-user",
				Given:     "a review assignment",
				When:      "reviewing",
				Then:      "apply end-user alignment criteria",
				ShouldNot: "focus only on technical details",
			},
			{
				Id:        "B-rev-revise-feedback",
				Given:     "issues found",
				When:      "voting",
				Then:      "vote REVISE with specific actionable feedback",
				ShouldNot: "vote REVISE without suggestions",
			},
			{
				Id:        "B-rev-accept",
				Given:     "all criteria met",
				When:      "voting",
				Then:      "vote ACCEPT with brief rationale",
				ShouldNot: "delay consensus unnecessarily",
			},
			{
				Id:        "B-rev-all-slices",
				Given:     "impl review (Phase 10)",
				When:      "assigned",
				Then:      "review ALL slices (not just one)",
				ShouldNot: "skip any slice",
			},
		},
	},
	protocol.RoleSupervisor: {
		Id:          protocol.RoleSupervisor,
		Name:        "Supervisor",
		Description: "Task coordinator, spawns workers, manages parallel execution",
		Model:       "opus",
		Thinking:    "medium",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Agent", "Task", "SendMessage"},
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
				Id:        "B-sup-read-context",
				Given:     "handoff received",
				When:      "starting",
				Then:      "read ratified plan, URD, UAT, and elicit tasks for full context",
				ShouldNot: "start without reading all four",
			},
			{
				Id:        "B-sup-model-trivial",
				Given:     "trivial changes (single-file edits, config tweaks, typo fixes)",
				When:      "spawning a worker",
				Then:      "use model: haiku to minimize cost and latency",
				ShouldNot: "use a heavyweight model for trivial work",
			},
			{
				Id:        "B-sup-model-nontrivial",
				Given:     "non-trivial changes (multi-file, architectural, logic-heavy)",
				When:      "spawning a worker",
				Then:      "prefer model: sonnet for the Task tool to ensure quality",
				ShouldNot: "default to haiku for complex work",
			},
			{
				Id:    "B-sup-ride-the-wave",
				Given: "Phase 8-10 execution",
				When:  "starting implementation",
				Then: "follow the Ride the Wave cycle: plan tasks with integration points, " +
					"launch the wave of workers, spawn reviewers for per-slice review " +
					"(clean exit = 0 BLOCKER + 0 IMPORTANT + 0 MINOR), workers fix per-slice with atomic commits, " +
					"and iterate review -> fix -> re-review up to the chosen review-effort budget until a fix-free clean round confirms 0/0/0; on budget exhaustion without clean, surface outstanding findings to the user at a gate",
				ShouldNot: "skip any stage; batch review across slices; hardcode the budget; proceed past the chosen budget without surfacing to the user; " +
					"close a wave with any finding silently outstanding",
			},
		},
	},
	protocol.RoleWorker: {
		Id:          protocol.RoleWorker,
		Name:        "Worker",
		Description: "Vertical slice implementer (full production code path)",
		Model:       "sonnet",
		Tools:       []string{"Read", "Glob", "Grep", "Bash", "Skill", "Edit", "Write", "SendMessage"},
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
				Id:        "B-worker-vertical-ownership",
				Given:     "vertical slice assignment",
				When:      "implementing",
				Then:      "own full production code path (types → tests → impl → wiring)",
				ShouldNot: "implement only horizontal layer",
			},
			{
				Id:        "B-worker-plan-backwards",
				Given:     "production code path",
				When:      "planning",
				Then:      "plan backwards from end point to types",
				ShouldNot: "start with types without knowing the end",
			},
			{
				Id:        "B-worker-test-production-code",
				Given:     "tests",
				When:      "writing",
				Then:      "import actual production code (CLI/API users will run)",
				ShouldNot: "create test-only export or dual code paths",
			},
			{
				Id:        "B-worker-verify-production",
				Given:     "implementation complete",
				When:      "verifying before signaling done",
				Then:      "manually trace the production code path end-to-end (entry point → service → types) to confirm wiring, error handling, and no dead code — beyond what automated gates check",
				ShouldNot: "treat passing tests as sufficient verification without a manual walkthrough",
			},
			{
				Id:        "B-worker-blocker",
				Given:     "a blocker",
				When:      "unable to proceed",
				Then:      "use /pasture:worker-blocked with details",
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
		Id:          "cmd-epoch",
		Name:        "pasture:epoch",
		Description: "Master orchestrator for full 12-phase workflow",
		RoleRef:     protocol.RoleEpoch,
		Phases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		File: "skills/epoch/SKILL.md",
	},
	"cmd-status": {
		Id:          "cmd-status",
		Name:        "pasture:status",
		Description: "Project status and monitoring via Beads queries",
		Title:       "Pasture Status",
		File:        "skills/status/SKILL.md",
	},
	"cmd-user-request": {
		Id:            "cmd-user-request",
		Name:          "pasture:user:request",
		Description:   "Capture user feature request verbatim (Phase 1)",
		Title:         "User Request (Phase 1)",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseRequest},
		File:          "skills/user-request/SKILL.md",
		CreatesLabels: []string{"L-p1s1_1"},
	},
	"cmd-user-elicit": {
		Id:            "cmd-user-elicit",
		Name:          "pasture:user:elicit",
		Description:   "User Requirements Elicitation survey (Phase 2)",
		Title:         "User Requirements Elicitation (Phase 2)",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseElicit},
		File:          "skills/user-elicit/SKILL.md",
		CreatesLabels: []string{"L-p2s2_1", "L-p2s2_2", "L-urd"},
	},
	"cmd-user-uat": {
		Id:            "cmd-user-uat",
		Name:          "pasture:user:uat",
		Description:   "User Acceptance Testing with demonstrative examples",
		Title:         "User Acceptance Test (UAT)",
		Phases:        []protocol.PhaseId{protocol.PhasePlanReview, protocol.PhaseImplUAT},
		File:          "skills/user-uat/SKILL.md",
		CreatesLabels: []string{"L-p5s5", "L-p11s11"},
	},
	"cmd-architect": {
		Id:          "cmd-architect",
		Name:        "pasture:architect",
		Description: "Specification writer and implementation designer",
		RoleRef:     protocol.RoleArchitect,
		Phases: []protocol.PhaseId{
			protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
			protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
			protocol.PhaseHandoff,
		},
		File: "skills/architect/SKILL.md",
	},
	"cmd-arch-propose": {
		Id:            "cmd-arch-propose",
		Name:          "pasture:architect:propose-plan",
		Description:   "Create PROPOSAL-N task with full technical plan",
		Title:         "Architect: Propose Plan",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhasePropose},
		File:          "skills/architect-propose-plan/SKILL.md",
		CreatesLabels: []string{"L-p3s3"},
	},
	"cmd-arch-review": {
		Id:            "cmd-arch-review",
		Name:          "pasture:architect:request-review",
		Description:   "Spawn 3 axis-specific reviewers (A/B/C)",
		Title:         "Architect: Request Review",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseReview},
		File:          "skills/architect-request-review/SKILL.md",
		CreatesLabels: []string{"L-p4s4"},
	},
	"cmd-arch-ratify": {
		Id:            "cmd-arch-ratify",
		Name:          "pasture:architect:ratify",
		Description:   "Ratify proposal, mark old proposals pasture:superseded",
		Title:         "Architect: Ratify Plan",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseRatify},
		File:          "skills/architect-ratify/SKILL.md",
		CreatesLabels: []string{"L-p6s6", "L-superseded"},
	},
	"cmd-arch-handoff": {
		Id:            "cmd-arch-handoff",
		Name:          "pasture:architect:handoff",
		Description:   "Create handoff document and transfer to supervisor",
		Title:         "Architect: Handoff to Supervisor",
		RoleRef:       protocol.RoleArchitect,
		Phases:        []protocol.PhaseId{protocol.PhaseHandoff},
		File:          "skills/architect-handoff/SKILL.md",
		CreatesLabels: []string{"L-p7s7"},
	},
	"cmd-supervisor": {
		Id:          "cmd-supervisor",
		Name:        "pasture:supervisor",
		Description: "Task coordinator, spawns workers, manages parallel execution",
		RoleRef:     protocol.RoleSupervisor,
		Phases: []protocol.PhaseId{
			protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
			protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
		},
		File: "skills/supervisor/SKILL.md",
	},
	"cmd-sup-plan": {
		Id:            "cmd-sup-plan",
		Name:          "pasture:supervisor:plan-tasks",
		Description:   "Decompose ratified plan into vertical slices (SLICE-N)",
		Title:         "Supervisor Plan Tasks",
		RoleRef:       protocol.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseImplPlan},
		File:          "skills/supervisor-plan-tasks/SKILL.md",
		CreatesLabels: []string{"L-p8s8", "L-p9s9"},
	},
	"cmd-sup-spawn": {
		Id:            "cmd-sup-spawn",
		Name:          "pasture:supervisor:spawn-worker",
		Description:   "Launch a worker agent for an assigned slice",
		Title:         "Supervisor Spawn Worker",
		RoleRef:       protocol.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/supervisor-spawn-worker/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-sup-track": {
		Id:          "cmd-sup-track",
		Name:        "pasture:supervisor:track-progress",
		Description: "Monitor worker status via Beads",
		Title:       "Supervisor: Track Progress",
		RoleRef:     protocol.RoleSupervisor,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices, protocol.PhaseCodeReview},
		File:        "skills/supervisor-track-progress/SKILL.md",
	},
	"cmd-sup-commit": {
		Id:            "cmd-sup-commit",
		Name:          "pasture:supervisor:commit",
		Description:   "Atomic commit per completed layer/slice",
		Title:         "Supervisor: Commit",
		RoleRef:       protocol.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseLanding},
		File:          "skills/supervisor-commit/SKILL.md",
		CreatesLabels: []string{"L-p12s12"},
	},
	"cmd-worker": {
		Id:          "cmd-worker",
		Name:        "pasture:worker",
		Description: "Vertical slice implementer (full production code path)",
		RoleRef:     protocol.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker/SKILL.md",
	},
	"cmd-work-impl": {
		Id:            "cmd-work-impl",
		Name:          "pasture:worker:implement",
		Description:   "Implement assigned vertical slice following TDD layers",
		Title:         "Worker: Implement Vertical Slice",
		RoleRef:       protocol.RoleWorker,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/worker-implement/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-work-complete": {
		Id:          "cmd-work-complete",
		Name:        "pasture:worker:complete",
		Description: "Signal slice completion after quality gates pass",
		Title:       "Worker: Signal Completion",
		RoleRef:     protocol.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker-complete/SKILL.md",
	},
	"cmd-work-blocked": {
		Id:          "cmd-work-blocked",
		Name:        "pasture:worker:blocked",
		Description: "Report a blocker to supervisor via Beads",
		Title:       "Worker: Handle Blockers",
		RoleRef:     protocol.RoleWorker,
		Phases:      []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:        "skills/worker-blocked/SKILL.md",
	},
	"cmd-reviewer": {
		Id:          "cmd-reviewer",
		Name:        "pasture:reviewer",
		Description: "End-user alignment reviewer for plans and code",
		RoleRef:     protocol.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer/SKILL.md",
	},
	"cmd-rev-plan": {
		Id:            "cmd-rev-plan",
		Name:          "pasture:reviewer:review-plan",
		Description:   "Evaluate proposal against one axis (binary ACCEPT/REVISE)",
		Title:         "Review Plan",
		RoleRef:       protocol.RoleReviewer,
		Phases:        []protocol.PhaseId{protocol.PhaseReview},
		File:          "skills/reviewer-review-plan/SKILL.md",
		CreatesLabels: []string{"L-p4s4"},
	},
	"cmd-rev-code": {
		Id:            "cmd-rev-code",
		Name:          "pasture:reviewer:review-code",
		Description:   "Review implementation slices with EAGER severity tree",
		Title:         "Review Code Implementation",
		RoleRef:       protocol.RoleReviewer,
		Phases:        []protocol.PhaseId{protocol.PhaseCodeReview},
		File:          "skills/reviewer-review-code/SKILL.md",
		CreatesLabels: []string{"L-p10s10", "L-sev-blocker", "L-sev-import", "L-sev-minor"},
	},
	"cmd-rev-comment": {
		Id:          "cmd-rev-comment",
		Name:        "pasture:reviewer:comment",
		Description: "Leave structured review comment via Beads",
		Title:       "Leave Structured Review Comment",
		RoleRef:     protocol.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer-comment/SKILL.md",
	},
	"cmd-rev-vote": {
		Id:          "cmd-rev-vote",
		Name:        "pasture:reviewer:vote",
		Description: "Cast ACCEPT or REVISE vote (binary only)",
		Title:       "Cast Review Vote",
		RoleRef:     protocol.RoleReviewer,
		Phases:      []protocol.PhaseId{protocol.PhaseReview, protocol.PhaseCodeReview},
		File:        "skills/reviewer-vote/SKILL.md",
	},
	"cmd-impl-slice": {
		Id:            "cmd-impl-slice",
		Name:          "pasture:impl:slice",
		Description:   "Vertical slice assignment and tracking",
		Title:         "Implementation Slice (Phase 9)",
		RoleRef:       protocol.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseWorkerSlices},
		File:          "skills/impl-slice/SKILL.md",
		CreatesLabels: []string{"L-p9s9"},
	},
	"cmd-impl-review": {
		Id:            "cmd-impl-review",
		Name:          "pasture:impl:review",
		Description:   "Code review coordination across all slices (Phase 10)",
		Title:         "Implementation Code Review (Phase 10)",
		RoleRef:       protocol.RoleSupervisor,
		Phases:        []protocol.PhaseId{protocol.PhaseCodeReview},
		File:          "skills/impl-review/SKILL.md",
		CreatesLabels: []string{"L-p10s10", "L-sev-blocker", "L-sev-import", "L-sev-minor"},
	},
	"cmd-explore": {
		Id:            "cmd-explore",
		Name:          "pasture:explore",
		Description:   "Codebase exploration — find integration points, existing patterns, and related code",
		Title:         "Explore",
		Phases:        []protocol.PhaseId{protocol.PhaseRequest, protocol.PhaseImplPlan},
		File:          "skills/explore/SKILL.md",
		CreatesLabels: []string{"L-p1s1_3"},
	},
	"cmd-research": {
		Id:            "cmd-research",
		Name:          "pasture:research",
		Description:   "Domain research — find standards, prior art, and competing approaches",
		Title:         "Research",
		Phases:        []protocol.PhaseId{protocol.PhaseRequest},
		File:          "skills/research/SKILL.md",
		CreatesLabels: []string{"L-p1s1_2"},
	},
	"cmd-swarm": {
		Id:          "cmd-swarm",
		Name:        "pasture:swarm",
		Description: "Launch worktree-based or intree agent workflows using aura-swarm",
		Title:       "Swarm — Unified Agent Orchestration",
		File:        "skills/swarm/SKILL.md",
	},
}

// ─── HandoffSpecs ─────────────────────────────────────────────────────────────

// HandoffSpecs maps handoff IDs to their full specifications.
// Mirrors Python HANDOFF_SPECS dict.
var HandoffSpecs = map[string]HandoffSpec{
	"h1": {
		Id:           "h1",
		SourceRole:   protocol.RoleArchitect,
		TargetRole:   protocol.RoleSupervisor,
		AtPhase:      protocol.PhaseHandoff,
		ContentLevel: "full-provenance",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan",
			"context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h2": {
		Id:           "h2",
		SourceRole:   protocol.RoleSupervisor,
		TargetRole:   protocol.RoleWorker,
		AtPhase:      protocol.PhaseWorkerSlices,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan", "impl-plan",
			"slice", "context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h3": {
		Id:           "h3",
		SourceRole:   protocol.RoleSupervisor,
		TargetRole:   protocol.RoleReviewer,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal", "ratified-plan", "impl-plan",
			"context", "key-decisions", "acceptance-criteria",
		},
	},
	"h4": {
		Id:           "h4",
		SourceRole:   protocol.RoleWorker,
		TargetRole:   protocol.RoleReviewer,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "impl-plan", "slice",
			"context", "key-decisions", "open-items",
		},
	},
	"h5": {
		Id:           "h5",
		SourceRole:   protocol.RoleReviewer,
		TargetRole:   protocol.RoleSupervisor,
		AtPhase:      protocol.PhaseCodeReview,
		ContentLevel: "summary-with-ids",
		RequiredFields: []string{
			"request", "urd", "proposal",
			"context", "key-decisions", "open-items", "acceptance-criteria",
		},
	},
	"h6": {
		Id:           "h6",
		SourceRole:   protocol.RoleSupervisor,
		TargetRole:   protocol.RoleArchitect,
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
		Id:           "layer-cake",
		Title:        "Layer Cake — TDD Parallelism Within Vertical Slices",
		Type:         "ascii-diagram",
		RoleRefs:     []protocol.RoleId{protocol.RoleWorker},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"layer-cake"},
		CommandRefs:  []string{"cmd-sup-plan"},
	},
	"ride-the-wave": {
		Id:           "ride-the-wave",
		Title:        "Ride the Wave — Coordinated Phase 8-10 Execution",
		Type:         "ascii-diagram",
		RoleRefs:     []protocol.RoleId{protocol.RoleSupervisor},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"ride-the-wave"},
		CommandRefs:  []string{"cmd-sup-spawn"},
	},
	"architect-state-flow": {
		Id:           "architect-state-flow",
		Title:        "Architect State Flow — Sequential Planning Phases 1-7",
		Type:         "ascii-diagram",
		RoleRefs:     []protocol.RoleId{protocol.RoleArchitect},
		SectionRef:   "workflows",
		WorkflowRefs: []string{"architect-state-flow"},
	},
}

// ─── ChecklistSpecs ───────────────────────────────────────────────────────────

// ChecklistSpecs maps "{role}-{gate}" keys to completion checklists.
// Mirrors Python CHECKLIST_SPECS dict.
var ChecklistSpecs = map[string]Checklist{
	"worker-completion": {
		RoleRef: protocol.RoleWorker,
		Gate:    "completion",
		Items: []ChecklistItem{
			{Id: "CL-worker-no-todos", Text: "No TODO placeholders in CLI/API actions", Required: true},
			{Id: "CL-worker-real-deps", Text: "Real dependencies wired (not mocks in production code)", Required: true},
			{Id: "CL-worker-test-import", Text: "Tests import production code (not test-only export)", Required: true},
			{Id: "CL-worker-no-dual-export", Text: "No dual-export anti-pattern (one code path for tests and production)", Required: true},
			{Id: "CL-worker-quality-gates", Text: "Quality gates pass (typecheck + tests)", Required: true},
			{Id: "CL-worker-production-path", Text: "Production code path verified end-to-end via code inspection", Required: true},
		},
	},
	"worker-slice-closure": {
		RoleRef: protocol.RoleWorker,
		Gate:    "slice-closure",
		Items: []ChecklistItem{
			{Id: "CL-worker-notified-supervisor", Text: "Supervisor notified via bd comments add (not bd close)", Required: true},
			{Id: "CL-worker-completion-done", Text: "All completion-gate items passed", Required: true},
			{Id: "CL-worker-close-on-review-wave", Text: "Can only close on a review wave, not a worker wave", Required: true},
			{Id: "CL-worker-review-eligible", Text: "Eligible to close only after review by independent agents with no BLOCKERS or IMPORTANT findings", Required: true},
		},
	},
	"supervisor-review-ready": {
		RoleRef: protocol.RoleSupervisor,
		Gate:    "review-ready",
		Items: []ChecklistItem{
			{Id: "CL-sup-all-slices-notified", Text: "All workers have notified completion via bd comments add", Required: true},
			{Id: "CL-sup-reviewers-assigned", Text: "Ephemeral reviewers spawned for all slices", Required: true},
			{Id: "CL-sup-severity-groups-created", Text: "Severity groups (BLOCKER/IMPORTANT/MINOR) eagerly created per slice", Required: true},
		},
	},
	"supervisor-landing": {
		RoleRef: protocol.RoleSupervisor,
		Gate:    "landing",
		Items: []ChecklistItem{
			{Id: "CL-sup-all-accept", Text: "Fix-free clean re-review: 0 BLOCKER + 0 IMPORTANT + 0 MINOR from all 3 reviewers", Required: true},
			{Id: "CL-sup-followup-created", Text: "FOLLOWUP epic created at UAT only if user-DEFER'd items exist (never from review severities)", Required: true},
			{Id: "CL-sup-agent-commit", Text: "git agent-commit used (not git commit -m)", Required: true},
			{Id: "CL-sup-tasks-closed", Text: "All upstream tasks closed or dependency-resolved", Required: true},
			{Id: "CL-sup-close-on-review-wave", Text: "Can only close on a review wave, not a worker wave", Required: true},
			{Id: "CL-sup-review-eligible", Text: "Eligible to close only after review by independent agents with 0 BLOCKER + 0 IMPORTANT + 0 MINOR findings", Required: true},
		},
	},
}

// ─── CoordinationCommands ─────────────────────────────────────────────────────

// CoordinationCommands maps command IDs to coordination command specs.
// Mirrors Python COORDINATION_COMMANDS dict.
var CoordinationCommands = map[string]CoordinationCommand{
	"cmd-coord-show": {
		Id:       "cmd-coord-show",
		Action:   "Check task details",
		Template: "bd show <task-id>",
		Shared:   true,
	},
	"cmd-coord-status": {
		Id:       "cmd-coord-status",
		Action:   "Update status",
		Template: "bd update <task-id> --status=in_progress",
		Shared:   true,
	},
	"cmd-coord-comment": {
		Id:       "cmd-coord-comment",
		Action:   "Add progress note",
		Template: `bd comments add <task-id> "Progress: ..."`,
		Shared:   true,
	},
	"cmd-coord-list": {
		Id:       "cmd-coord-list",
		Action:   "List in-progress",
		Template: "bd list --pretty --status=in_progress",
		Shared:   true,
	},
	"cmd-coord-blocked": {
		Id:       "cmd-coord-blocked",
		Action:   "List blocked",
		Template: "bd blocked",
		Shared:   true,
	},
	"cmd-coord-assign": {
		Id:       "cmd-coord-assign",
		Action:   "Assign task",
		Template: `bd update <task-id> --assignee "<worker-name>"`,
		RoleRef:  protocol.RoleSupervisor,
	},
	"cmd-coord-label": {
		Id:       "cmd-coord-label",
		Action:   "Label completed slice",
		Template: "bd label add <slice-id> pasture:p9-impl:slice-complete",
		RoleRef:  protocol.RoleSupervisor,
	},
	"cmd-coord-dep-add": {
		Id:       "cmd-coord-dep-add",
		Action:   "Chain dependency",
		Template: "bd dep add <parent> --blocked-by <child>",
		RoleRef:  protocol.RoleSupervisor,
	},
	"cmd-coord-close": {
		Id:       "cmd-coord-close",
		Action:   "Report completion",
		Template: "bd close <task-id>",
		RoleRef:  protocol.RoleWorker,
	},
	"cmd-coord-worker-notes": {
		Id:       "cmd-coord-worker-notes",
		Action:   "Add completion notes",
		Template: `bd update <task-id> --notes="Implementation complete. Production code verified."`,
		RoleRef:  protocol.RoleWorker,
	},
}

// ─── WorkflowSpecs ────────────────────────────────────────────────────────────

// WorkflowSpecs maps workflow IDs to their full specifications.
// Mirrors Python WORKFLOW_SPECS dict.
var WorkflowSpecs = map[string]Workflow{
	"ride-the-wave": {
		Id:      "ride-the-wave",
		Name:    "Ride the Wave",
		RoleRef: protocol.RoleSupervisor,
		Description: "Coordinated Phase 8-10 execution pattern. The supervisor orchestrates " +
			"the full cycle: plan slices, launch workers, " +
			"spawn reviewers for per-slice review, workers fix, and re-review up to the chosen review-effort budget " +
			"until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR; on budget exhaustion without clean, surface outstanding findings to the user at a gate.",
		Stages: []WorkflowStage{
			{
				Id:        "rtw-plan",
				Name:      "Plan",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseImplPlan,
				Actions: []WorkflowAction{
					{
						Id:          "rtw-plan-read",
						Instruction: "Read RATIFIED_PLAN and URD via bd show",
						Command:     "bd show <ratified-plan-id> && bd show <urd-id>",
					},
					{
						Id:          "rtw-plan-explore",
						Instruction: "Spawn ephemeral Explore subagents (`subagent_type=Explore`) for scoped codebase queries — NOT standing teams",
					},
					{
						Id:          "rtw-plan-decompose",
						Instruction: "Use Explore findings to decompose into vertical slices with integration points",
					},
					{
						Id:          "rtw-plan-leaf-tasks",
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
				Id:        "rtw-build",
				Name:      "Build",
				Order:     2,
				Execution: "parallel",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						Id: "rtw-build-spawn",
						Instruction: "Spawn workers via the Agent tool — " +
							"set `name` for a named teammate, leave `name` empty for a backgrounded subagent " +
							"(NOT aura-swarm). " +
							"Choose model: sonnet for non-trivial slices, haiku for trivial changes. " +
							"Set thinking effort to match slice complexity.",
					},
					{
						Id:          "rtw-build-monitor",
						Instruction: "Monitor worker progress via bd list and bd show",
						Command:     `bd list --labels="pasture:p9-impl:s9-slice" --status=in_progress`,
					},
					{
						Id:          "rtw-build-integrate",
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
				Id:        "rtw-review-fix",
				Name:      "Review + Fix Cycles",
				Order:     3,
				Execution: "conditional-loop",
				PhaseRef:  protocol.PhaseCodeReview,
				Actions: []WorkflowAction{
					{
						Id:          "rtw-review-spawn",
						Instruction: "Spawn reviewers via Task tool for per-slice code review",
					},
					{
						Id:          "rtw-review-severity",
						Instruction: "Reviewers create severity groups (BLOCKER/IMPORTANT/MINOR) per slice",
					},
					{
						Id:          "rtw-review-severity-track",
						Instruction: "Track findings in the 3 severity groups; ALL groups must reach 0 before wave close (FOLLOWUP is created later at UAT, fed only by user-DEFER'd items)",
					},
					{
						Id:          "rtw-review-fix",
						Instruction: "Workers fix ALL findings (BLOCKER, IMPORTANT, and MINOR)",
					},
				},
				OperationalDetail: "- Spawn 3 ephemeral reviewer subagents per round (same pattern as Phase 4 plan review)\n" +
					"- **CLEAN REVIEW** = 0 BLOCKER + 0 IMPORTANT + 0 MINOR from ALL reviewers on a fix-free round\n" +
					"- Per-slice fix+review; iterate up to the chosen review-effort budget\n" +
					"- Fix flow: Stage 3 (dirty review) -> Stage 2 (worker fixes) -> Stage 3 (re-review)\n" +
					"- Configurable review-effort budget (chosen at Phase 8: 3 rounds / 1 round / 0 rounds / unlimited / custom) — repeat review -> fix -> re-review until the slice is clean (0/0/0); on budget exhaustion without clean, surface outstanding findings to the user at a gate\n" +
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
					"          CLEAN? \u251c\u2500\u2500 YES (0/0/0) \u2192 slice passes, proceed\n" +
					"                 \u2502\n" +
					"                 \u2514\u2500\u2500 NO (any finding remains)\n" +
					"                       \u2502\n" +
					"                       \u25bc\n" +
					"              \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\n" +
					"              \u2502 Stage 2: worker    \u2502\n" +
					"              \u2502 fixes ALL findings \u2502\n" +
					"              \u2502 (BLOCK/IMP/MINOR)  \u2502\n" +
					"              \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\n" +
					"                       \u2502\n" +
					"                       \u25bc\n" +
					"              \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\n" +
					"              \u2502 Stage 3: re-review \u2502\n" +
					"              \u2502 (new ephemeral     \u2502\n" +
					"              \u2502  reviewers)        \u2502\n" +
					"              \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u252c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\n" +
					"                       \u2502\n" +
					"                 loop (re-review)\n" +
					"                       \u2502\n" +
					"          repeat until clean (0/0/0) \u2014 up to the chosen budget, else surface to user\n" +
					"```",
				ExitConditions: []ExitCondition{
					{
						Type:      "success",
						Condition: "All reviewers report 0 BLOCKER + 0 IMPORTANT + 0 MINOR on a fix-free clean round — proceed to Phase 11 UAT",
					},
					{
						Type:      "continue",
						Condition: "Any finding (BLOCKER, IMPORTANT, or MINOR) remains within budget — workers fix, spawn new ephemeral reviewers (up to the chosen review-effort budget; on exhaustion, surface to the user)",
					},
				},
			},
		},
	},
	"layer-cake": {
		Id:      "layer-cake",
		Name:    "Layer Cake",
		RoleRef: protocol.RoleWorker,
		Description: "TDD layer-by-layer implementation within a vertical slice. " +
			"Worker implements types first, then tests (will fail), " +
			"then production code to make tests pass.",
		Stages: []WorkflowStage{
			{
				Id:        "lc-types",
				Name:      "Types",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						Id:          "lc-types-read",
						Instruction: "Read slice task and identify required types",
						Command:     "bd show <slice-task-id>",
					},
					{
						Id:          "lc-types-define",
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
				Id:        "lc-tests",
				Name:      "Tests",
				Order:     2,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						Id:          "lc-tests-write",
						Instruction: "Write tests importing production code (CLI/API users will run) — tests WILL fail",
					},
					{
						Id:          "lc-tests-verify-import",
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
				Id:        "lc-impl",
				Name:      "Implementation + Wiring",
				Order:     3,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseWorkerSlices,
				Actions: []WorkflowAction{
					{
						Id:          "lc-impl-code",
						Instruction: "Implement production code to make Layer 2 tests pass",
					},
					{
						Id:          "lc-impl-wire",
						Instruction: "Wire with real dependencies (not mocks in production code)",
					},
					{
						Id:          "lc-impl-run-tests",
						Instruction: "Run tests — all Layer 2 tests must pass",
					},
					{
						Id:          "lc-impl-commit",
						Instruction: "Commit completed work",
						Command:     "git agent-commit -m ...",
					},
					{
						Id:          "lc-impl-notify",
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
						Condition: "Blocker encountered — use /pasture:worker-blocked with details",
					},
				},
			},
		},
	},
	"architect-state-flow": {
		Id:      "architect-state-flow",
		Name:    "Architect State Flow",
		RoleRef: protocol.RoleArchitect,
		Description: "Sequential planning phases 1-7. The architect captures requirements, " +
			"writes proposals, coordinates review consensus, and hands off to supervisor.",
		Stages: []WorkflowStage{
			{
				Id:        "asf-request",
				Name:      "Request",
				Order:     1,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseRequest,
				Actions: []WorkflowAction{
					{
						Id:          "asf-request-capture",
						Instruction: "Capture user request verbatim via /pasture:user-request",
					},
					{
						Id:          "asf-request-classify",
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
				Id:        "asf-elicit",
				Name:      "Elicit",
				Order:     2,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseElicit,
				Actions: []WorkflowAction{
					{
						Id:          "asf-elicit-ure",
						Instruction: "Run URE survey with user via /pasture:user-elicit",
					},
					{
						Id:          "asf-elicit-urd",
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
				Id:        "asf-propose",
				Name:      "Propose",
				Order:     3,
				Execution: "sequential",
				PhaseRef:  protocol.PhasePropose,
				Actions: []WorkflowAction{
					{
						Id:          "asf-propose-write",
						Instruction: "Write full technical proposal: interfaces, approach, validation checklist, BDD criteria",
					},
					{
						Id:          "asf-propose-create",
						Instruction: "Create PROPOSAL-N task via /pasture:architect:propose-plan",
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
				Id:        "asf-review",
				Name:      "Review",
				Order:     4,
				Execution: "conditional-loop",
				PhaseRef:  protocol.PhaseReview,
				Actions: []WorkflowAction{
					{
						Id:          "asf-review-spawn",
						Instruction: "Spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)",
					},
					{
						Id:          "asf-review-wait",
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
				Id:        "asf-uat",
				Name:      "Plan UAT",
				Order:     5,
				Execution: "sequential",
				PhaseRef:  protocol.PhasePlanReview,
				Actions: []WorkflowAction{
					{
						Id:          "asf-uat-present",
						Instruction: "Present plan to user with demonstrative examples via /pasture:user-uat",
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
				Id:        "asf-ratify",
				Name:      "Ratify",
				Order:     6,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseRatify,
				Actions: []WorkflowAction{
					{
						Id:          "asf-ratify-label",
						Instruction: "Add ratify label to accepted PROPOSAL-N",
					},
					{
						Id:          "asf-ratify-supersede",
						Instruction: "Mark all prior proposals pasture:superseded",
					},
					{
						Id:          "asf-ratify-placeholder",
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
				Id:        "asf-handoff",
				Name:      "Handoff",
				Order:     7,
				Execution: "sequential",
				PhaseRef:  protocol.PhaseHandoff,
				Actions: []WorkflowAction{
					{
						Id:          "asf-handoff-doc",
						Instruction: "Author the HANDOFF in its Beads task body with full inline provenance (include the HANDOFF task ID)",
					},
					{
						Id:          "asf-handoff-transfer",
						Instruction: "Transfer to supervisor via /pasture:architect:handoff",
					},
				},
				ExitConditions: []ExitCondition{
					{
						Type:      "success",
						Condition: "Handoff authored in the HANDOFF Beads task body, supervisor notified",
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
		Id:     "axis-correctness",
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
		Id:     "axis-test_quality",
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
		Id:     "axis-elegance",
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
var ProcedureSteps = map[protocol.RoleId][]ProcedureStep{
	protocol.RoleEpoch:     {},
	protocol.RoleArchitect: {},
	protocol.RoleReviewer:  {},
	protocol.RoleSupervisor: {
		{
			Id:          "S-supervisor-call-skill",
			Order:       1,
			Instruction: "Call Skill(/pasture:supervisor) to load role instructions",
			Command:     "Skill(/pasture:supervisor)",
		},
		{
			Id:          "S-supervisor-read-plan",
			Order:       2,
			Instruction: "Read RATIFIED_PLAN, URD, UAT, and elicit tasks via bd show for full context",
			Command:     "bd show <ratified-plan-id> && bd show <urd-id> && bd show <uat-id> && bd show <elicit-id>",
		},
		{
			Id:          "S-supervisor-explore-ephemeral",
			Order:       3,
			Instruction: "Spawn ephemeral Explore subagents via Task tool for scoped codebase queries",
			Context:     "Each subagent is short-lived and returns findings; no standing team overhead",
		},
		{
			Id:          "S-supervisor-decompose-slices",
			Order:       4,
			Instruction: "Decompose into vertical slices",
			Context: "Vertical slices give one worker end-to-end ownership of a feature path " +
				"(types → tests → impl → wiring) with clear file boundaries",
			NextState: protocol.PhaseImplPlan,
		},
		{
			Id:          "S-supervisor-create-leaf-tasks",
			Order:       5,
			Instruction: "Create leaf tasks (L1/L2/L3) for every slice",
			Command:     `bd create --labels pasture:p9-impl:s9-slice --title "SLICE-{K}-L{1,2,3}: <description>" ...`,
			Examples: []Example{
				{
					Id:    "S-supervisor-create-leaf-tasks-frontmatter",
					Lang:  "bash",
					Label: "template",
					Code: "bd create --labels pasture:p9-impl:s9-slice \\\n" +
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
			Id:    "S-supervisor-spawn-workers",
			Order: 6,
			Instruction: "Spawn workers via the Agent tool — " +
				"set `name` for a named teammate, leave `name` empty for a backgrounded subagent " +
				"(NOT aura-swarm). " +
				"Choose model: sonnet for non-trivial slices, haiku for trivial changes. " +
				"Set thinking effort to match slice complexity.",
			NextState: protocol.PhaseWorkerSlices,
		},
	},
	protocol.RoleWorker: {
		{
			Id:          "S-worker-types",
			Order:       1,
			Instruction: "Types, interfaces, schemas (no deps)",
		},
		{
			Id:          "S-worker-tests",
			Order:       2,
			Instruction: "Tests importing production code (will fail initially)",
		},
		{
			Id:          "S-worker-impl",
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
	"L-p1s1_1":      {Id: "L-p1s1_1", Value: "pasture:p1-user:s1_1-classify", Special: false, PhaseRef: "p1", SubstepRef: "s1_1"},
	"L-p1s1_2":      {Id: "L-p1s1_2", Value: "pasture:p1-user:s1_2-research", Special: false, PhaseRef: "p1", SubstepRef: "s1_2"},
	"L-p1s1_3":      {Id: "L-p1s1_3", Value: "pasture:p1-user:s1_3-explore", Special: false, PhaseRef: "p1", SubstepRef: "s1_3"},
	"L-p2s2_1":      {Id: "L-p2s2_1", Value: "pasture:p2-user:s2_1-elicit", Special: false, PhaseRef: "p2", SubstepRef: "s2_1"},
	"L-p2s2_2":      {Id: "L-p2s2_2", Value: "pasture:p2-user:s2_2-urd", Special: false, PhaseRef: "p2", SubstepRef: "s2_2"},
	"L-p3s3":        {Id: "L-p3s3", Value: "pasture:p3-plan:s3-propose", Special: false, PhaseRef: "p3", SubstepRef: "s3"},
	"L-p4s4":        {Id: "L-p4s4", Value: "pasture:p4-plan:s4-review", Special: false, PhaseRef: "p4", SubstepRef: "s4"},
	"L-p5s5":        {Id: "L-p5s5", Value: "pasture:p5-user:s5-uat", Special: false, PhaseRef: "p5", SubstepRef: "s5"},
	"L-p6s6":        {Id: "L-p6s6", Value: "pasture:p6-plan:s6-ratify", Special: false, PhaseRef: "p6", SubstepRef: "s6"},
	"L-p7s7":        {Id: "L-p7s7", Value: "pasture:p7-plan:s7-handoff", Special: false, PhaseRef: "p7", SubstepRef: "s7"},
	"L-p8s8":        {Id: "L-p8s8", Value: "pasture:p8-impl:s8-plan", Special: false, PhaseRef: "p8", SubstepRef: "s8"},
	"L-p9s9":        {Id: "L-p9s9", Value: "pasture:p9-impl:s9-slice", Special: false, PhaseRef: "p9", SubstepRef: "s9"},
	"L-p10s10":      {Id: "L-p10s10", Value: "pasture:p10-impl:s10-review", Special: false, PhaseRef: "p10", SubstepRef: "s10"},
	"L-p11s11":      {Id: "L-p11s11", Value: "pasture:p11-user:s11-uat", Special: false, PhaseRef: "p11", SubstepRef: "s11"},
	"L-p12s12":      {Id: "L-p12s12", Value: "pasture:p12-impl:s12-landing", Special: false, PhaseRef: "p12", SubstepRef: "s12"},
	"L-urd":         {Id: "L-urd", Value: "pasture:urd", Special: true, Description: "User Requirements Document"},
	"L-superseded":  {Id: "L-superseded", Value: "pasture:superseded", Special: true, Description: "Superseded proposal or plan"},
	"L-sev-blocker": {Id: "L-sev-blocker", Value: "pasture:severity:blocker", Special: true, SeverityRef: "BLOCKER"},
	"L-sev-import":  {Id: "L-sev-import", Value: "pasture:severity:important", Special: true, SeverityRef: "IMPORTANT"},
	"L-sev-minor":   {Id: "L-sev-minor", Value: "pasture:severity:minor", Special: true, SeverityRef: "MINOR"},
	"L-followup":    {Id: "L-followup", Value: "pasture:epic-followup", Special: true, Description: "Follow-up epic for non-blocking findings"},
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
		Note: "N increments per revision. Old proposals marked pasture:superseded.",
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
		Note: "Follow-up epic created at UAT when user-DEFER'd items exist (never from review severities; " +
			"all review findings must reach 0 before wave close). " +
			"Single-parent epic relationship — no followup-of-followup.",
	},
	{
		Pattern: "FOLLOWUP_URE: {description}", LabelRef: "L-p2s2_1", CreatedBy: "supervisor", PhaseRef: "p2",
		Note: "Scoping URE to determine which user-DEFER'd UAT items to address",
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
		Note: "Follow-up slice. Adopts user-DEFER'd UAT-item leaf tasks as children " +
			"(dual-parent: leaf blocks both the DEFER'd-items tracking group AND follow-up slice).",
	},
}

// ─── SubstepDataMap ───────────────────────────────────────────────────────────

// SubstepDataMap maps phase ID strings to their ordered substep data.
// Mirrors Python SUBSTEP_DATA dict.
var SubstepDataMap = map[string][]SubstepData{
	"p1": {
		{
			Id: "s1_1", Type: "classify", Execution: "sequential", Order: 1,
			LabelRef:    "L-p1s1_1",
			Description: "Classify request along 4 axes: scope, complexity, risk, domain novelty",
		},
		{
			Id: "s1_2", Type: "research", Execution: "parallel", Order: 2,
			ParallelGroup: "p1-discovery", LabelRef: "L-p1s1_2",
			Description: "Find domain standards, prior art, relevant documentation",
		},
		{
			Id: "s1_3", Type: "explore", Execution: "parallel", Order: 2,
			ParallelGroup: "p1-discovery", LabelRef: "L-p1s1_3",
			Description: "Codebase exploration for integration points",
		},
	},
	"p2": {
		{
			Id: "s2_1", Type: "elicit", Execution: "sequential", Order: 1,
			LabelRef:    "L-p2s2_1",
			Description: "URE survey: structured Q&A with user to capture requirements",
		},
		{
			Id: "s2_2", Type: "urd", Execution: "sequential", Order: 2,
			LabelRef:    "L-p2s2_2",
			Description: "Create URD as single source of truth for requirements",
			ExtraLabel:  "L-urd",
		},
	},
	"p3": {
		{
			Id: "s3", Type: "propose", Execution: "sequential", Order: 1,
			LabelRef:    "L-p3s3",
			Description: "Full technical proposal: interfaces, approach, validation checklist, BDD criteria",
		},
	},
	"p4": {
		{
			Id: "s4", Type: "review", Execution: "parallel", Order: 1,
			LabelRef:    "L-p4s4",
			Description: "Each reviewer assesses one axis (A/B/C). All 3 must ACCEPT.",
			Instances:   &SubstepInstances{Count: "3", Per: "review-axis"},
		},
	},
	"p5": {
		{
			Id: "s5", Type: "uat", Execution: "sequential", Order: 1,
			LabelRef:    "L-p5s5",
			Description: "Present plan to user with demonstrative examples. User approves or requests changes.",
		},
	},
	"p6": {
		{
			Id: "s6", Type: "ratify", Execution: "sequential", Order: 1,
			LabelRef:    "L-p6s6",
			Description: "Add ratify label. Mark prior proposals pasture:superseded. Create placeholder IMPL_PLAN.",
		},
	},
	"p7": {
		{
			Id: "s7", Type: "handoff", Execution: "sequential", Order: 1,
			LabelRef:    "L-p7s7",
			Description: "Create handoff document with full inline provenance. Transfer to supervisor.",
		},
	},
	"p8": {
		{
			Id: "s8", Type: "plan", Execution: "sequential", Order: 1,
			LabelRef:        "L-p8s8",
			Description:     "Identify production code paths. Create SLICE-N tasks with leaf tasks. Assign workers.",
			StartupSequence: true,
		},
	},
	"p9": {
		{
			Id: "s9", Type: "slice", Execution: "parallel", Order: 1,
			LabelRef:    "L-p9s9",
			Description: "Each worker owns full vertical: types, tests, implementation, wiring",
			Instances:   &SubstepInstances{Count: "N", Per: "production-code-path"},
		},
	},
	"p10": {
		{
			Id: "s10", Type: "review", Execution: "parallel", Order: 1,
			LabelRef:    "L-p10s10",
			Description: "Each reviewer reviews ALL slices against their axis. EAGER severity tree.",
			Instances:   &SubstepInstances{Count: "3", Per: "review-axis"},
		},
	},
	"p11": {
		{
			Id: "s11", Type: "uat", Execution: "sequential", Order: 1,
			LabelRef:    "L-p11s11",
			Description: "Present implementation to user. User approves or requests fixes.",
		},
	},
	"p12": {
		{
			Id: "s12", Type: "landing", Execution: "sequential", Order: 1,
			LabelRef:    "L-p12s12",
			Description: "git agent-commit, bd sync, git push. Close upstream tasks.",
		},
	},
}

// Body content for the reviewer-review-code skill SKILL.md.
// Ported from aura-plugins/skills/reviewer-review-code/SKILL.md.
package codegen

var reviewerReviewCodeBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-code-quality-gates",
			Given:     "code assignment",
			When:      "reviewing",
			Then:      "apply end-user alignment criteria and verify production code paths",
			ShouldNot: "approve without running quality gates",
		},
		{
			Id:        "rev-code-verify-gates",
			Given:     "implementation",
			When:      "verifying",
			Then:      "run the project's quality gates",
			ShouldNot: "approve without passing checks",
		},
		{
			Id:        "rev-code-eager-severity",
			Given:     "issues found",
			When:      "categorizing",
			Then:      "use BLOCKER/IMPORTANT/MINOR severity with EAGER group creation",
			ShouldNot: "skip creating empty severity groups",
		},
		// 3rd owner of frag--sup-blocker-dual-parent: canonical Then
		// "add dual-parent: blocks BOTH the severity group AND the slice".
		behaviorRef(FragSupBlockerDualParent),
		// R7/A1: code review gates each wave on open BLOCKERs and then converges
		// over open findings; on budget exhaustion without convergence, surface
		// outstanding findings to the user. Resolves to
		// SharedFragmentSpecs[FragReviewCleanExit].
		behaviorRef(FragReviewCleanExit),
		// R6: verify EVERY request carries validation-case fixtures (generalized
		// from fix-intent-only at v2-2). Resolves to
		// SharedFragmentSpecs[FragValidationCases].
		behaviorRef(FragValidationCases),
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "rev-code-when-to-use",
			Title:   "When to Use",
			Content: `Assigned to review code implementation after worker slices complete (Phase 10).`,
		},
		{
			Id:      "rev-code-severity-tree",
			Title:   "Severity Tree: EAGER Creation",
			Content: `First bind REVIEW_ID_URI using Step 4: Create Review Task below; then **ALWAYS create 3 severity group tasks per wave, per axis, per round**, even if some groups have no findings:`,
			Subsections: []ProseSection{
				{
					Id:    "rev-code-create-groups",
					Title: "Step 1: Create All 3 Severity Groups Immediately",
					Content: `` + "```" + `bash
# One severity trio per wave, per axis, per round — not one per slice.
# Wave 1 contains the first slice, the second slice, and the third slice.
# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_GROUP_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} BLOCKER" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
BLOCKER findings for this wave review round")
[[rec:label]] "$BLOCKER_GROUP_ID_URI" pasture:severity:blocker

IMPORTANT_GROUP_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} IMPORTANT" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
IMPORTANT findings for this wave review round")
[[rec:label]] "$IMPORTANT_GROUP_ID_URI" pasture:severity:important

MINOR_GROUP_ID_URI=$([[rec:create]] --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} MINOR" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
MINOR findings for this wave review round")
[[rec:label]] "$MINOR_GROUP_ID_URI" pasture:severity:minor

# Step 2: Wire the one severity trio to the wave's review task
[[rec:block]] "${REVIEW_ID_URI}" --by "${BLOCKER_GROUP_ID_URI}"
[[rec:block]] "${REVIEW_ID_URI}" --by "${IMPORTANT_GROUP_ID_URI}"
[[rec:block]] "${REVIEW_ID_URI}" --by "${MINOR_GROUP_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-add-findings",
					Title: "Adding Findings to Severity Groups",
					Content: `` + "```" + `bash
# Each finding carries slice: and wave: in references.
# BLOCKER finding — dual-parent relationship
BLOCKER_FINDING_ID_URI=$([[rec:create]] --kind task --title "BLOCKER: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_1_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details with file:line references>")
[[rec:label]] "${BLOCKER_FINDING_ID_URI}" pasture:finding
[[rec:block]] "${BLOCKER_GROUP_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
[[rec:block]] "${SLICE_1_1_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"

# IMPORTANT finding — single parent (severity group only)
IMPORTANT_FINDING_ID_URI=$([[rec:create]] --kind task --title "IMPORTANT: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_2_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details>")
[[rec:label]] "${IMPORTANT_FINDING_ID_URI}" pasture:finding
[[rec:block]] "${IMPORTANT_GROUP_ID_URI}" --by "${IMPORTANT_FINDING_ID_URI}"

# MINOR finding — single parent (severity group only)
MINOR_FINDING_ID_URI=$([[rec:create]] --kind task --title "MINOR: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_3_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details>")
[[rec:label]] "${MINOR_FINDING_ID_URI}" pasture:finding
[[rec:block]] "${MINOR_GROUP_ID_URI}" --by "${MINOR_FINDING_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-close-empty",
					Title: "Closing Empty Groups",
					Content: `Empty severity groups (no findings) are closed immediately:

` + "```" + `bash
# If no IMPORTANT findings were found:
[[rec:close]] "${IMPORTANT_GROUP_ID_URI}"

# If no MINOR findings were found:
[[rec:close]] "${MINOR_GROUP_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-dual-parent-rule",
					Title: "Dual-Parent BLOCKER Relationship",
					Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`pasture:severity:blocker`" + `) — for categorization
2. The slice they block — for dependency tracking

This ensures BLOCKERs both categorize under the severity tree AND block the slice they apply to.

IMPORTANT and MINOR findings do **NOT** block the slice via dual-parent (only BLOCKER does), and they are **not** routed to a follow-up epic: they stay open past the wave gate and converge at the end, where the convergence rule reads open pasture:finding tasks across all waves (R7/A1). The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity.`,
				},
			},
		},
		{
			Id:      "rev-code-steps",
			Title:   "Steps",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "rev-code-step1-read",
					Title: "Step 1: Read Code Changes and URD",
					Content: `` + "```" + `bash
[[rec:show]] "${SLICE_ID_URI}"
[[rec:show]] "${URD_ID_URI}"   # Read URD for requirements context
` + "```" + ``,
				},
				{
					Id:    "rev-code-step2-gates",
					Title: "Step 2: Run Quality Gates",
					Content: "```" + `bash` + "\n" +
						`# Run your project's type checking and test commands` + "\n" +
						"```",
				},
				{
					Id:      "rev-code-step3-criteria",
					Title:   "Step 3: Apply Review Criteria and Verify Production Code Paths",
					Content: `Apply end-user alignment criteria (see ` + "`pasture:reviewer`" + `) and verify production code paths (see Verify Production Code Paths section below).`,
				},
				{
					Id:    "rev-code-step4-create",
					Title: "Step 4: Create Review Task",
					Content: `` + "```" + `bash
REVIEW_ID_URI=$([[rec:create]] --kind task --title "Review: <feature>" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  urd: \"${URD_ID_URI}\"
---
VOTE: <ACCEPT|REVISE> - <justification>")
# One review covers the whole wave, so wire every slice in the wave to it.
[[rec:block]] "${SLICE_1_1_ID_URI}" --by "${REVIEW_ID_URI}"
[[rec:block]] "${SLICE_1_2_ID_URI}" --by "${REVIEW_ID_URI}"
[[rec:block]] "${SLICE_1_3_ID_URI}" --by "${REVIEW_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-step5-severity",
					Title: "Steps 5–8: Severity Tree and Vote",
					Content: `5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via ` + "`" + `[[rec:note]]` + "`" + ``,
				},
			},
		},
		{
			Id:      "rev-code-verify-production",
			Title:   "Verify Production Code Paths",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "rev-code-dual-export",
					Title: "Check for Dual-Export Anti-Pattern",
					Content: `**Anti-pattern example:**
` + "```" + `go` + "\n" +
						`// WRONG: Test-only export
func HandleCommand(argv []string, service Service) error { /* tested */ }

// WRONG: Production-only command (not tested)
var commandCmd = &cobra.Command{
    Use: "command",
    RunE: func(cmd *cobra.Command, args []string) error {
        // TODO: wire up service
        return nil
    },
}` + "\n" +
						"```" + `

**Correct example:**
` + "```" + `go` + "\n" +
						`// CORRECT: Single command, both tested and used in production
var commandCmd = &cobra.Command{
    Use: "command",
    RunE: func(cmd *cobra.Command, args []string) error {
        service := NewService(RealDeps{})
        result, err := service.DoThing(args)
        if err != nil {
            return err
        }
        fmt.Println(result)
        return nil
    },
}

// Tests import commandCmd directly
// import "myproject/cmd/thing"` + "\n" +
						"```",
				},
				{
					Id:    "rev-code-no-todos",
					Title: "Verify No TODO Placeholders",
					Content: "```" + `bash` + "\n" +
						`grep -r "TODO" src/  # Should not find any in delivered code` + "\n" +
						"```",
				},
				{
					Id:    "rev-code-test-imports",
					Title: "Check Tests Import Production Code",
					Content: `- Test file should import the actual CLI command or API endpoint
- Not a separate test harness function
- No TODOs in CLI/API actions
- Real dependencies wired (not mocks in production code)`,
				},
				{
					Id:    "rev-code-validation-cases",
					Title: "Verify Validation Cases (R6)",
					Content: `For **every** REQUEST (not only fix-intent ones), per [` + "frag--validation-cases" + `] verify the implementation:
- Carries **test fixtures** for the concrete validation cases captured in URE/UAT (the definition of done plus the correct/incorrect behaviours that must pass or must fail).
- Evaluates the implementation against each confirmed validation case.

An implementation that ships without validation-case fixtures is an IMPORTANT finding. There is **no** request-type axis/enum gating this — recognize what a request needs from the REQUEST/URD.`,
				},
			},
		},
		{
			Id:      "rev-code-clean-exit",
			Title:   "Two-Phase Review (within the chosen review-effort budget)",
			Content: `Per [` + "frag--review-clean-exit" + `], ` + "`C-finding-lifecycle`" + `, and ` + "`C-review-effort-budget`" + `, run the two-phase review. **During a wave**, only an open BLOCKER finding gates progress; IMPORTANT and MINOR findings stay open. **After the last wave**, convergence queries every open ` + "`pasture:finding`" + ` across all waves and targets **0/0/0** within the gate declaration's ` + "`maxRounds`" + `. Iterate **review → fix → re-review** up to the **review-effort budget chosen at Phase 8** (defaults: 3 rounds / 1 round / 0 rounds / unlimited / custom). The close rule is owned by ` + "`C-finding-lifecycle`" + `. On **budget exhaustion without convergence**, SURFACE the outstanding findings to the user at a gate for a decision — do not proceed dirty and do not loop forever. The budget is never hardcoded.`,
		},
		{
			Id:      "rev-code-followup-epic",
			Title:   "Follow-up Epic",
			Content: `The FOLLOWUP epic is **not** created from review findings. Open review findings converge at the end; their close rule is ` + "`C-finding-lifecycle`" + ` (R7/A1). The FOLLOWUP epic is fed ONLY by **user-DEFER'd UAT items** (Phase 11), and the Supervisor creates it from those (label ` + "`pasture:epic-followup`" + `).`,
		},
		{
			Id:    "rev-code-followup-slice",
			Title: "Reviewing a follow-up slice",
			Content: `When reviewing follow-up slices, use the same procedure:
- **Review task naming:** name the review for the follow-up slice, the axis, and the round
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per wave, per axis, per round)
- **Open findings converge at the end:** findings (BLOCKER/IMPORTANT/MINOR) in a FOLLOWUP_SLICE review also stay open past the wave gate and converge at the end — they are **never** re-routed to a follow-up epic (no followup-of-followup; the FOLLOWUP epic is fed only by user-DEFER'd UAT items)
- The worker's completion handoff (h4) reports which DEFER'd-item leaf tasks were resolved — verify these during review`,
		},
		{
			Id:    "rev-code-report",
			Title: "Report Results",
			Content: `` + "```" + `bash
# Add vote comment to the review task
[[rec:note]] "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
[[rec:note]] "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: REVISE - BLOCKERs found, see severity tree for details"
` + "```" + ``,
		},
	},
}

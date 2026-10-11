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
		// 3rd owner of frag--sup-blocker-dual-parent (SLICE-4): canonical Then
		// "add dual-parent: blocks BOTH the severity group AND the slice".
		behaviorRef(FragSupBlockerDualParent),
		// R7/A1: code review iterates review->fix->re-review up to the chosen
		// review-effort budget until a fix-free clean round confirms 0/0/0; on budget
		// exhaustion without clean, surface outstanding findings to the user. Resolves
		// to SharedFragmentSpecs[FragReviewCleanExit] (SLICE-1).
		behaviorRef(FragReviewCleanExit),
		// R6: verify EVERY request carries validation-case fixtures (generalized
		// from fix-intent-only at v2-2). Resolves to
		// SharedFragmentSpecs[FragValidationCases] (SLICE-1).
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
			Content: `First bind REVIEW_ID_URI using Step 4: Create Review Task below; then **ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:`,
			Subsections: []ProseSection{
				{
					Id:    "rev-code-create-groups",
					Title: "Step 1: Create All 3 Severity Groups Immediately",
					Content: `` + "```" + `bash
# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 BLOCKER" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
BLOCKER findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$BLOCKER_GROUP_ID_URI" pasture:severity:blocker
pasture task label add "$BLOCKER_GROUP_ID_URI" pasture:p10-impl:s10-review

IMPORTANT_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 IMPORTANT" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
IMPORTANT findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_GROUP_ID_URI" pasture:severity:important
pasture task label add "$IMPORTANT_GROUP_ID_URI" pasture:p10-impl:s10-review

MINOR_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 MINOR" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
MINOR findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$MINOR_GROUP_ID_URI" pasture:severity:minor
pasture task label add "$MINOR_GROUP_ID_URI" pasture:p10-impl:s10-review

# Step 2: Wire severity groups to review task
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${BLOCKER_GROUP_ID_URI}"
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${IMPORTANT_GROUP_ID_URI}"
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${MINOR_GROUP_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-add-findings",
					Title: "Adding Findings to Severity Groups",
					Content: `` + "```" + `bash
# BLOCKER finding — dual-parent relationship
BLOCKER_FINDING_ID_URI=$(pasture task create "BLOCKER: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details with file:line references>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${BLOCKER_GROUP_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"
pasture task dep add "${SLICE_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"

# IMPORTANT finding — single parent (severity group only)
IMPORTANT_FINDING_ID_URI=$(pasture task create "IMPORTANT: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${IMPORTANT_GROUP_ID_URI}" --blocked-by "${IMPORTANT_FINDING_ID_URI}"

# MINOR finding — single parent (severity group only)
MINOR_FINDING_ID_URI=$(pasture task create "MINOR: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${MINOR_GROUP_ID_URI}" --blocked-by "${MINOR_FINDING_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-close-empty",
					Title: "Closing Empty Groups",
					Content: `Empty severity groups (no findings) are closed immediately:

` + "```" + `bash
# If no IMPORTANT findings were found:
pasture task close "${IMPORTANT_GROUP_ID_URI}"

# If no MINOR findings were found:
pasture task close "${MINOR_GROUP_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-dual-parent-rule",
					Title: "Dual-Parent BLOCKER Relationship",
					Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`pasture:severity:blocker`" + `) — for categorization
2. The slice they block — for dependency tracking

This ensures BLOCKERs both categorize under the severity tree AND block the slice they apply to.

IMPORTANT and MINOR findings do **NOT** block the slice via dual-parent (only BLOCKER does), but they are **not** routed to a follow-up epic either: ALL severity groups (BLOCKER, IMPORTANT, MINOR) must reach 0 before the review wave closes (R7/A1). The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity.`,
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
pasture task show "${SLICE_ID_URI}"
pasture task show "${URD_ID_URI}"   # Read URD for requirements context
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
REVIEW_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1: <feature>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  urd: "${URD_ID_URI}"
---
VOTE: <ACCEPT|REVISE> - <justification>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REVIEW_ID_URI" pasture:p10-impl:s10-review
pasture task dep add "${SLICE_ID_URI}" --blocked-by "${REVIEW_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "rev-code-step5-severity",
					Title: "Steps 5–8: Severity Tree and Vote",
					Content: `5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via ` + "`" + `pasture task comment add` + "`" + ``,
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
			Title:   "Clean-Review Exit (within the chosen review-effort budget)",
			Content: `Per [` + "frag--review-clean-exit" + `] and ` + "`C-review-effort-budget`" + `, iterate **review → fix → re-review** up to the **review-effort budget chosen at Phase 8** (defaults: 3 rounds / 1 round / 0 rounds / unlimited / custom) until a fix-free clean round confirms **0 BLOCKER + 0 IMPORTANT + 0 MINOR** within budget. On **budget exhaustion without a clean round**, SURFACE the outstanding findings to the user at a gate for a decision — do not proceed dirty and do not loop forever. The budget is never hardcoded. A wave never closes on a fix-applying round, and never with any finding silently outstanding.`,
		},
		{
			Id:      "rev-code-followup-epic",
			Title:   "Follow-up Epic",
			Content: `The FOLLOWUP epic is **not** created from review findings. ALL review severities (BLOCKER/IMPORTANT/MINOR) must reach 0 before the wave closes (R7/A1). The FOLLOWUP epic is fed ONLY by **user-DEFER'd UAT items** (Phase 11), and the Supervisor creates it from those (label ` + "`pasture:epic-followup`" + `).`,
		},
		{
			Id:    "rev-code-followup-slice",
			Title: "Reviewing FOLLOWUP_SLICE-N (Follow-up Code Review)",
			Content: `When reviewing follow-up slices, use the same procedure:
- **Review task naming:** ` + "`FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`" + `
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per review round)
- **All severities reach 0:** ALL findings (BLOCKER/IMPORTANT/MINOR) in a FOLLOWUP_SLICE review must also reach 0 before the follow-up wave closes — they are **never** re-routed to a follow-up epic (no followup-of-followup; the FOLLOWUP epic is fed only by user-DEFER'd UAT items)
- The worker's completion handoff (h4) reports which DEFER'd-item leaf tasks were resolved — verify these during review`,
		},
		{
			Id:    "rev-code-report",
			Title: "Report Results",
			Content: `` + "```" + `bash
# Add vote comment to the review task
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: REVISE - BLOCKERs found, see severity tree for details"
` + "```" + ``,
		},
	},
}

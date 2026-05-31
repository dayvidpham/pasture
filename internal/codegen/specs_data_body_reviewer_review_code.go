// Body content for the reviewer-review-code skill SKILL.md.
// Ported from aura-plugins/skills/reviewer-review-code/SKILL.md.
package codegen

var reviewerReviewCodeBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10`,

	Behaviors: []BehaviorSpec{
		{
			ID:        "rev-code-quality-gates",
			Given:     "code assignment",
			When:      "reviewing",
			Then:      "apply end-user alignment criteria and verify production code paths",
			ShouldNot: "approve without running quality gates",
		},
		{
			ID:        "rev-code-verify-gates",
			Given:     "implementation",
			When:      "verifying",
			Then:      "run the project's quality gates",
			ShouldNot: "approve without passing checks",
		},
		{
			ID:        "rev-code-eager-severity",
			Given:     "issues found",
			When:      "categorizing",
			Then:      "use BLOCKER/IMPORTANT/MINOR severity with EAGER group creation",
			ShouldNot: "skip creating empty severity groups",
		},
		{
			ID:        "rev-code-blocker-dual-parent",
			Given:     "BLOCKER finding",
			When:      "wiring dependencies",
			Then:      "add dual-parent relationship (severity group + slice)",
			ShouldNot: "wire BLOCKER to only one parent",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "rev-code-when-to-use",
			Title:   "When to Use",
			Content: `Assigned to review code implementation after worker slices complete (Phase 10).`,
		},
		{
			ID:      "rev-code-severity-tree",
			Title:   "Severity Tree: EAGER Creation",
			Content: `**ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:`,
			Subsections: []ProseSection{
				{
					ID:    "rev-code-create-groups",
					Title: "Step 1: Create All 3 Severity Groups Immediately",
					Content: "```" + `bash` + "\n" +
						`# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
bd create --labels "aura:severity:blocker,aura:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 BLOCKER" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
BLOCKER findings for this review round"
# Result: <blocker-group-id>

bd create --labels "aura:severity:important,aura:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 IMPORTANT" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
IMPORTANT findings for this review round"
# Result: <important-group-id>

bd create --labels "aura:severity:minor,aura:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 MINOR" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
MINOR findings for this review round"
# Result: <minor-group-id>

# Step 2: Wire severity groups to review task
bd dep add <review-id> --blocked-by <blocker-group-id>
bd dep add <review-id> --blocked-by <important-group-id>
bd dep add <review-id> --blocked-by <minor-group-id>` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-add-findings",
					Title: "Adding Findings to Severity Groups",
					Content: "```" + `bash` + "\n" +
						`# BLOCKER finding — dual-parent relationship
bd create --title "BLOCKER: <finding title>" \
  --description "<finding details with file:line references>"
bd dep add <blocker-group-id> --blocked-by <blocker-finding-id>
bd dep add <slice-id> --blocked-by <blocker-finding-id>

# IMPORTANT finding — single parent (severity group only)
bd create --title "IMPORTANT: <finding title>" \
  --description "<finding details>"
bd dep add <important-group-id> --blocked-by <important-finding-id>

# MINOR finding — single parent (severity group only)
bd create --title "MINOR: <finding title>" \
  --description "<finding details>"
bd dep add <minor-group-id> --blocked-by <minor-finding-id>` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-close-empty",
					Title: "Closing Empty Groups",
					Content: `Empty severity groups (no findings) are closed immediately:

` + "```" + `bash` + "\n" +
						`# If no IMPORTANT findings were found:
bd close <important-group-id>

# If no MINOR findings were found:
bd close <minor-group-id>` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-dual-parent-rule",
					Title: "Dual-Parent BLOCKER Relationship",
					Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`aura:severity:blocker`" + `) — for categorization
2. The slice they block — for dependency tracking

This ensures BLOCKERs both categorize under the severity tree AND block the slice they apply to.

IMPORTANT and MINOR findings do **NOT** block the slice — they are tracked in the follow-up epic.`,
				},
			},
		},
		{
			ID:      "rev-code-steps",
			Title:   "Steps",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "rev-code-step1-read",
					Title: "Step 1: Read Code Changes and URD",
					Content: "```" + `bash` + "\n" +
						`bd show <slice-id>
bd show <urd-id>   # Read URD for requirements context` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-step2-gates",
					Title: "Step 2: Run Quality Gates",
					Content: "```" + `bash` + "\n" +
						`# Run your project's type checking and test commands` + "\n" +
						"```",
				},
				{
					ID:      "rev-code-step3-criteria",
					Title:   "Step 3: Apply Review Criteria and Verify Production Code Paths",
					Content: `Apply end-user alignment criteria (see ` + "`aura:reviewer`" + `) and verify production code paths (see Verify Production Code Paths section below).`,
				},
				{
					ID:    "rev-code-step4-create",
					Title: "Step 4: Create Review Task",
					Content: "```" + `bash` + "\n" +
						`bd create --labels "aura:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1: <feature>" \
  --description "---
references:
  slice: <slice-id>
  urd: <urd-id>
---
VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <slice-id> --blocked-by <review-id>` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-step5-severity",
					Title: "Steps 5–8: Severity Tree and Vote",
					Content: `5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via ` + "`bd comments add`",
				},
			},
		},
		{
			ID:      "rev-code-verify-production",
			Title:   "Verify Production Code Paths",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:    "rev-code-dual-export",
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
					ID:    "rev-code-no-todos",
					Title: "Verify No TODO Placeholders",
					Content: "```" + `bash` + "\n" +
						`grep -r "TODO" src/  # Should not find any in delivered code` + "\n" +
						"```",
				},
				{
					ID:    "rev-code-test-imports",
					Title: "Check Tests Import Production Code",
					Content: `- Test file should import the actual CLI command or API endpoint
- Not a separate test harness function
- No TODOs in CLI/API actions
- Real dependencies wired (not mocks in production code)`,
				},
			},
		},
		{
			ID:    "rev-code-followup-epic",
			Title: "Follow-up Epic",
			Content: `**Trigger:** Review completion + ANY IMPORTANT or MINOR findings exist.
**NOT gated on BLOCKER resolution.**
**Owner:** Supervisor creates the follow-up epic (label ` + "`aura:epic-followup`" + `).`,
		},
		{
			ID:    "rev-code-followup-slice",
			Title: "Reviewing FOLLOWUP_SLICE-N (Follow-up Code Review)",
			Content: `When reviewing follow-up slices, use the same procedure:
- **Review task naming:** ` + "`FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`" + `
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per review round)
- **No followup-of-followup:** New IMPORTANT/MINOR findings from FOLLOWUP_SLICE review are tracked on the existing follow-up epic, not a new nested follow-up
- The worker's completion handoff (h4) reports which original leaf tasks were resolved — verify these during review`,
		},
		{
			ID:    "rev-code-report",
			Title: "Report Results",
			Content: "```" + `bash` + "\n" +
				`# Add vote comment to the review task
bd comments add <review-id> "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
bd comments add <review-id> "VOTE: REVISE - BLOCKERs found, see severity tree for details"` + "\n" +
				"```",
		},
	},
}

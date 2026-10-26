// Body content for the afk-implement skill SKILL.md.
package codegen

var afkImplementBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-8-implementation-plan)** <- Phases 8-10`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "B-afk-no-merge",
			Given:     "an unattended session with green CI and a clean review wave",
			When:      "a pull request is ready",
			Then:      "leave it open, record the merge order and evidence, and stop",
			ShouldNot: "merge, self-approve on the human's behalf, or push to the default branch",
		},
		{
			Id:        "B-afk-smallest-iteration",
			Given:     "an approved direction",
			When:      "scoping work",
			Then:      "ship the smallest independently reviewable and revertable change per iteration",
			ShouldNot: "batch unrelated changes, add speculative abstraction, or refactor adjacent code",
		},
		{
			Id:        "B-afk-decide-dont-ask",
			Given:     "a reversible design choice",
			When:      "the human is away",
			Then:      "decide from repository conventions, record the rationale and reversal path, and continue",
			ShouldNot: "block or idle waiting for approval",
		},
		{
			Id:        "B-afk-clean-wave",
			Given:     "a completed iteration",
			When:      "running the review wave",
			Then:      "require three independent ACCEPT votes with zero blockers and zero importants",
			ShouldNot: "substitute self-review, ignore findings, or fabricate consensus",
		},
		{
			Id:        "B-afk-stop-at-clean",
			Given:     "every scoped iteration has an open PR and the final cross-stack wave is clean",
			When:      "the run is complete",
			Then:      "stop and write the morning handoff",
			ShouldNot: "start new scope or continue looking for work",
		},
		{
			Id:        "B-afk-evidence",
			Given:     "a claimed quality gate",
			When:      "reporting the result",
			Then:      "name the exact command, the commit it ran against, and the outcome",
			ShouldNot: "claim a gate that did not run or report green for a job that never executed",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "afk-when-to-use",
			Title: "When to Use",
			Content: "Use this skill when the human hands over a scoped direction and expects to review later: " +
				"they say they are going AFK or to bed, ask for overnight or unattended implementation, say " +
				"\"don't block on me\", ask for progressive adoption, or ask for open pull requests that must " +
				"not be merged. It also fits an in-flight branch that needs its gates run, a PR opened, and a " +
				"review wave driven to clean.\n\n" +
				"Do not use it for an attended session where the human expects the normal live URE/UAT gates, " +
				"and do not use it to authorize a release, deploy, or merge.",
		},
		{
			Id:    "afk-invocation-contract",
			Title: "Invocation Contract",
			Content: "Translate the human's words into explicit defaults, and state the mapping in the handoff.\n\n" +
				"| The human says | Default |\n" +
				"|---|---|\n" +
				"| Act as the supervisor | Own the implementation and review loop; spawn workers and reviewers instead of serializing every part. |\n" +
				"| Use a named model for all subagents | Resolve the exact provider/model ID with the harness before spawning, pass it on every subagent call, and record any substitution. |\n" +
				"| Scope smaller; minimal code; YAGNI | One smallest end-to-end change per iteration; no speculative flags, config, abstraction, or drive-by edits. |\n" +
				"| Progressive adoption | Each iteration is independently reviewable and revertable; prefer several small PRs to one large one. |\n" +
				"| Stack changes when needed | Base an iteration on its predecessor only when it depends on it; otherwise branch from the default branch. |\n" +
				"| Open PRs, do not merge | Push feature branches and open ready PRs; never merge or push to the default branch. |\n" +
				"| Stop on a clean review wave | Use the exit criteria below; do not keep burning the backlog after clean. |\n" +
				"| Handle design questions sensibly | Decide reversible questions from the repository, record the rationale, and continue. |\n" +
				"| Review in the morning | Write a five-minute handoff with merge order, decisions, risks, and first checks. |",
		},
		{
			Id:    "afk-operating-rules",
			Title: "Operating Rules",
			Content: "These are not negotiable, including when CI is green and every reviewer accepts:\n\n" +
				"- Never merge. The human merges after review.\n" +
				"- Never push to the default or protected branch; feature branches only.\n" +
				"- Rewrite only your own feature branches, with force-with-lease, and only to rebase a stack.\n" +
				"- No destructive commands, no shared-history rewrites, no branch or data deletion, and no Git " +
				"hook installation or hook-path changes.\n" +
				"- Preserve the primary checkout, including uncommitted work. Do all implementation in a worktree.\n" +
				"- Reviewers must be independent subagents. Never write their findings or votes yourself.\n" +
				"- Never claim a gate passed unless it ran on the commit you report.",
		},
		{
			Id:    "afk-iteration-loop",
			Title: "Iteration Loop",
			Content: "Work one iteration at a time, and open its PR before starting the next.\n\n" +
				"1. Recover. Read live tracker state, Git status, branches ahead of the base, worktrees, and open " +
				"PRs. If the human said to work on something specific, inventory the existing work instead of " +
				"restarting it.\n" +
				"2. Choose the next iteration. Follow the named scope first. Otherwise prefer a failing test or " +
				"reproduction, a correctness bug, the smallest missing piece of the approved feature, then " +
				"cleanup that unblocks it. Do not invent product scope.\n" +
				"3. Plan in one paragraph: change, files, test, acceptance case, risk, base branch. If it does " +
				"not fit, split it.\n" +
				"4. Create or reuse a worktree using the repository naming convention " +
				"(repository, issue number, commit type, short description); branch and directory share the name.\n" +
				"5. Reproduce or write the failing test first where practical, then make the smallest production " +
				"change that satisfies it.\n" +
				"6. Run the repository gates for the touched surface, then the full gate when cheap. Record the " +
				"exact commands and outcomes.\n" +
				"7. Commit atomically with the repository's agent-commit path. Stage only files this iteration " +
				"owns.\n" +
				"8. Push the branch and open the PR. Then run the review wave, fix findings, re-review, and only " +
				"then start the next iteration.",
		},
		{
			Id:    "afk-pr-contract",
			Title: "Pull Request Contract",
			Content: "Open a ready PR so CI runs, not a draft, unless the iteration is hard-stopped.\n\n" +
				"The body carries, in order:\n\n" +
				"1. What and why, in one paragraph.\n" +
				"2. Scope boundary: what this PR deliberately does not do.\n" +
				"3. Acceptance: the Given/When/Then case, or the observed before and after.\n" +
				"4. Verification: exact commands, results, and CI status.\n" +
				"5. Stack: position, base branch, and merge order, or standalone.\n" +
				"6. Decisions: choice, rationale, alternatives, and how to reverse it.\n" +
				"7. Known gaps: residual findings, flaky tests, unverified paths.\n\n" +
				"A hard-stopped iteration is a draft PR whose body names the exact blocker and next action. Never " +
				"present a broken PR as ready, and never merge or close it.",
		},
		{
			Id:    "afk-stacking",
			Title: "Stacking",
			Content: "Stack only when the change genuinely depends on its predecessor. Name the base branch in the " +
				"PR body and the handoff, and order the merge bottom-up. When a base moves, rebase only your own " +
				"branches and push with force-with-lease, then re-run the gates.\n\n" +
				"Prefer not stacking. Two iterations that touch the same files are usually one iteration.",
		},
		{
			Id:    "afk-review-wave",
			Title: "Review Wave",
			Content: "Spawn three independent reviewers in parallel with the model named in the invocation. Give each " +
				"the same inputs and a distinct axis.",
			Subsections: []ProseSection{
				{
					Id:    "afk-review-axes",
					Title: "Axes",
					Content: "| Axis | Ask |\n|---|---|\n" +
						"| Correctness | Does the diff do what the PR claims without breaking existing behavior? Check edge cases, error paths, data loss, and concurrency. |\n" +
						"| Tests | Do tests exercise the production path rather than a test-only export? Do they cover failure cases and the acceptance case? Would they catch a regression? Are they flaky? |\n" +
						"| Elegance and YAGNI | Is complexity proportional? Any speculative abstraction, unused flag, duplicated logic, or scope creep? Is there a materially smaller change? |\n\n" +
						"Require findings as severity (blocker, important, minor), file and line, problem, and a concrete " +
						"fix. Require a binary vote: ACCEPT or REVISE. Load the impl-review skill for the full " +
						"severity-group and dual-parent procedure when the run uses tracker severity tasks.",
				},
				{
					Id:    "afk-review-clean",
					Title: "Clean Wave",
					Content: "A clean wave is three ACCEPT votes with zero blockers and zero importants. Fix blockers " +
						"and importants in the same branch, commit atomically, push, and re-run the wave. Fix a minor " +
						"finding if trivial; otherwise record it as follow-up. Cap the loop at three cycles per PR.\n\n" +
						"If a blocker survives three cycles, mark the PR draft, write the exact blocker and attempted " +
						"fixes, and move to independent work. Do not merge and do not call the wave clean.\n\n" +
						"After the final iteration, run one cross-stack wave over all open PRs or the cumulative diff: " +
						"conflicting assumptions, duplicated helpers, and merge-order breakage. That is the wave that " +
						"must end clean before stopping.",
				},
			},
		},
		{
			Id:    "afk-decisions",
			Title: "Autonomous Decisions",
			Content: "Use this precedence, highest first:\n\n" +
				"1. The human's explicit request and the repository's documented invariants.\n" +
				"2. Existing repository conventions: naming, error style, test patterns, and gates.\n" +
				"3. The smallest, most reversible option.\n" +
				"4. A prototype behind no public API.\n\n" +
				"Record every non-obvious decision in the PR body: decision, why, alternatives, and reversal. If two " +
				"options are plausible and reversible, pick the smaller one and keep moving.\n\n" +
				"Reasonable autonomy includes naming, file placement, test shape, helper extraction, error wording, " +
				"choosing between equivalent libraries already present, and splitting or merging iterations.",
			Subsections: []ProseSection{
				{
					Id:    "afk-hard-stops",
					Title: "Hard Stops",
					Content: "Stop the current line of work, leave a clear note, and continue only with independent " +
						"iterations when one of these is true:\n\n" +
						"- Credentials, permissions, a paid resource, or a secret is required.\n" +
						"- The action is irreversible or destructive: rewriting shared history, deleting data or " +
						"branches, installing or altering Git hooks, or force-pushing a shared branch.\n" +
						"- It reaches production: deploy, release, publish, tag, or migrate live data.\n" +
						"- Product intent is genuinely ambiguous and both options change user-facing behavior " +
						"irreversibly.\n" +
						"- The change is security-sensitive and there is no safe default.\n\n" +
						"Not hard stops: failing tests, flaky tests, CI infrastructure outages, merge conflicts inside " +
						"your own stack, missing documentation, an unavailable model (fall back and record it), or an " +
						"unpleasant design tradeoff.",
				},
			},
		},
		{
			Id:    "afk-verification",
			Title: "Verification and Evidence",
			Content: "Discover the gates from the repository's agent instructions, Makefile, and CI. Run the narrow " +
				"gate while iterating and the full gate before calling a PR ready. Docs-only changes still run " +
				"docs, link, and frontmatter checks.\n\n" +
				"If CI cannot run, say so explicitly and report local results instead. Keep the tree quiescent " +
				"while verifying, and record the commit SHA the results apply to.",
		},
		{
			Id:    "afk-tracking",
			Title: "Tracking",
			Content: "Create one tracker task per iteration and link it to the parent scope. Add labels and " +
				"comments; never delete audit history. Comment the branch, commit SHA, PR URL, gate results, and " +
				"each review round with its votes.\n\n" +
				"Workers report evidence and never close their own tasks. Close only after the clean wave, and " +
				"only for work you supervised and verified. If the tracker is unavailable, report the store path, " +
				"the failed operation, and the repair needed, then continue with Git and PR evidence. A failed " +
				"query is not proof of no work.",
		},
		{
			Id:    "afk-morning-handoff",
			Title: "Morning Handoff",
			Content: "Write the handoff to the repository's untracked agent-handoff directory, for example " +
				"`" + ".agents.local/afk-<YYYY-MM-DD>.md" + "`" +
				", and repeat the essentials in the final message:\n\n" +
				"1. State: why the run stopped, what is ready, and what is not.\n" +
				"2. PR table: link, one-line summary, base, stack position, CI, review result, and recommended " +
				"merge order.\n" +
				"3. Decisions: choice and rationale for each non-obvious call.\n" +
				"4. Risks: residual findings, unverified paths, and flaky or skipped gates.\n" +
				"5. Scope boundary: what you deliberately did not do.\n" +
				"6. First three checks: the fastest way for the human to validate the work.\n\n" +
				"Never describe unmerged work as done or shipped. Say open, review clean, awaiting merge.",
		},
		{
			Id:    "afk-exit-criteria",
			Title: "Exit Criteria",
			Content: "Stop the session when all of these are true:\n\n" +
				"- Every scoped iteration has an open PR, or is a draft with a hard-stop reason.\n" +
				"- The final cross-stack review wave is clean: three ACCEPT, zero blockers, zero importants, or the " +
				"three-cycle budget is exhausted and residual findings are recorded.\n" +
				"- The morning handoff is written and names the merge order.\n" +
				"- No merge occurred, the default branch is untouched, and no unrelated work was modified.\n\n" +
				"Then stop and report. Do not start new scope, do not make one more small fix, and do not wait or " +
				"poll for the human.",
		},
		{
			Id:    "afk-anti-patterns",
			Title: "Anti-Patterns",
			Content: "- Merging because CI is green or the human seemed to want speed.\n" +
				"- One giant PR that cannot be adopted progressively.\n" +
				"- Silencing, skipping, or deleting a failing test to reach green.\n" +
				"- Refactoring adjacent code because it was already open.\n" +
				"- Asking a question mid-run and idling, or blocking forever on approval.\n" +
				"- Calling a wave clean without independent reviewers or without reading their findings.\n" +
				"- Expanding the backlog instead of stopping at the approved scope.\n" +
				"- Describing a red or draft PR as ready in the handoff.",
		},
	},
}

// Body content for the worker-implement skill SKILL.md.
// Ported from aura-plugins/skills/worker-implement/SKILL.md.
package codegen

var workerImplementBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "wimpl-plan-backwards",
			Given:     "vertical slice task",
			When:      "implementing",
			Then:      "plan backwards from production code path",
			ShouldNot: "start with types without knowing the end",
		},
		{
			Id:        "wimpl-vertical-ownership",
			Given:     "production code path",
			When:      "implementing",
			Then:      "own full vertical (types → tests → impl → wiring)",
			ShouldNot: "implement only horizontal layer",
		},
		{
			Id:        "wimpl-import-production",
			Given:     "tests",
			When:      "writing",
			Then:      "import actual production code",
			ShouldNot: "create test-only export or dual code paths",
		},
		{
			Id:        "wimpl-verify-production",
			Given:     "implementation complete",
			When:      "verifying",
			Then:      "confirm production code path is wired (via code inspection or safe testing)",
			ShouldNot: "rely only on unit tests passing",
		},
		{
			Id:        "wimpl-inject-deps",
			Given:     "dependencies",
			When:      "designing",
			Then:      "inject all deps for testability",
			ShouldNot: "hard-code `new`",
		},
		{
			Id:        "wimpl-validate-input",
			Given:     "external input",
			When:      "processing",
			Then:      "validate with schema/validation tooling",
			ShouldNot: "trust raw input",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "wimpl-when-to-use",
			Title:   "When to Use",
			Content: `You have a Beads task ID for a vertical slice and are ready to implement end-to-end.`,
		},
		{
			Id:      "wimpl-steps",
			Title:   "Steps",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "wimpl-step0-plan",
					Title: "Step 0: Plan backwards from production code path (before implementing)",
					Content: `**Given** Beads task **when** starting **then** identify production code path first

` + "```bash" + `
bd show <task-id>
# Look for: "productionCodePath": "cli-command subcommand" or "api-endpoint"
` + "```" + `

**Trace backwards through call stack:**
` + "```" + `
End: User runs production command
  ↓ Entry: CLI command.action(...) or API endpoint handler
  ↓ Service: createXService({ deps }).method(...)
  ↓ Types: InputType → OutputType
` + "```" + `

**Identify what you own in each layer:**
- **L1 Types:** Which types does YOUR slice need? (not other slices)
- **L2 Tests:** Import actual production code (CLI/API), not test-only export
- **L3 Implementation:** Service method + wiring with real dependencies (not TODO)`,
				},
				{
					Id:    "wimpl-step1-read",
					Title: "Step 1: Read Beads task for full context",
					Content: "```bash" + `
bd show <task-id>
` + "```",
				},
				{
					Id:    "wimpl-step2-status",
					Title: "Step 2: Update status",
					Content: "```bash" + `
bd update <task-id> --status=in_progress
` + "```",
				},
				{
					Id:    "wimpl-step3-layers",
					Title: "Step 3: Implement your vertical slice in layers",
					Content: `**Layer 1: Types (your slice only)**
- Create only types YOUR slice needs
- Don't add types for other slices

**Layer 2: Tests (import production code)**
- Import actual CLI/API package: ` + "`import \"myproject/cmd/feature\"`" + `
- NOT test-only handler: ~~` + "`import \"myproject/internal/testhelpers/feature\"`" + `~~
- Tests will FAIL - expected (no impl yet)

**Layer 3: Implementation + Wiring**
- Service method for your slice
- CLI/API wiring with real dependencies: ` + "`NewService(ServiceDeps{ FS: fs, Logger: logger })`" + `
- NOT TODO placeholders: ~~` + "`// TODO: Wire service`" + `~~

Follow:
- validation_checklist items
- acceptance_criteria (BDD Given/When/Then)
- tradeoffs from ratified plan`,
				},
				{
					Id:    "wimpl-step4-quality",
					Title: "Step 4: Verify quality gates",
					Content: `- Type checking passes
- Tests pass`,
				},
				{
					Id:    "wimpl-step5-commit",
					Title: "Step 5: Commit safely in a shared worktree",
					Content: `Stage **only** the files belonging to your slice, by name:
` + "```bash" + `
git add cmd/feature/list.go pkg/feature/service.go pkg/feature/types.go
git agent-commit -m "feat(feature): add list subcommand"
` + "```" + `

**Never** use ` + "`git add .`" + `, ` + "`git add -A`" + `, or ` + "`git commit -am ...`" + ` —
they sweep peer-worker WIP into your commit.

**Never** use destructive git operations (` + "`git reset --hard`" + `,
` + "`git checkout HEAD -- <path>`" + `, ` + "`git stash pop`" + `, ` + "`git stash apply`" + `,
` + "`git clean -fd`" + `, ` + "`git branch -D`" + `) on the shared worktree. A
PreToolUse hook blocks these for worker agents; if you find peer
work in your way, post ` + "`bd comments add`" + ` and wait for supervisor
coordination instead. See **Shared-Worktree Git Discipline** in
` + "`/pasture:worker`" + ` for the full rationale and the escape hatch.`,
				},
			},
		},
		{
			Id:    "wimpl-checklist",
			Title: "Checklist",
			Content: `- [ ] Planned backwards from production code path
- [ ] Read Beads task for validation_checklist
- [ ] Each validation_checklist item satisfied
- [ ] BDD acceptance_criteria met
- [ ] Tests import actual production code (not test-only export)
- [ ] No dual-export anti-pattern (one code path for tests and production)
- [ ] No TODO placeholders in production code
- [ ] Service wired with real dependencies (not mocks in production)
- [ ] Quality gates pass (type checking + tests)
- [ ] Production code path verified (via code inspection: no TODOs, real deps wired, tests import production code)
- [ ] Files staged individually by name (no ` + "`git add .`" + ` / ` + "`git add -A`" + `)
- [ ] No destructive git operations (` + "`reset --hard`" + `, ` + "`checkout HEAD -- <path>`" + `, ` + "`stash pop/apply`" + `, ` + "`clean -fd`" + `, ` + "`branch -D`" + `) used on the shared worktree`,
		},
		{
			Id:    "wimpl-followup-slices",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `If your Beads task is a ` + "`FOLLOWUP_SLICE-N`" + `, the implementation procedure is identical. Additionally:
- Check for an "Adopted Leaf Tasks" section in ` + "`bd show <task-id>`" + ` — these are IMPORTANT/MINOR findings you must resolve
- Your implementation must address each adopted leaf task's acceptance criteria
- On completion, report which leaf tasks were resolved`,
		},
		{
			Id:    "wimpl-next",
			Title: "Next",
			Content: `- Complete: ` + "`/pasture:worker-complete`" + `
- Blocked: ` + "`/pasture:worker-blocked`",
		},
	},

	Recipes: []RecipeBlock{},
}

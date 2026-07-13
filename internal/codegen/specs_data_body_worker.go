// Body content for the worker role SKILL.md.
package codegen

var workerBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,
	Sections: []ProseSection{
		{
			Id:    "wrk-what-you-own",
			Title: "Vertical Slice Ownership in Practice",
			Content: `**Example vertical slice: "CLI command with list subcommand"**
- **Production code path:** ` + "`" + `./bin/cli-tool command list` + "`" + ` (what end users run)
- **You own (within each file):**
  - Types: ` + "`" + `ListOptions` + "`" + `, ` + "`" + `ListEntry` + "`" + ` (in pkg/feature/types.go)
  - Tests: list_test.go (importing actual CLI command package)
  - Service: ` + "`" + `ListItems()` + "`" + ` method (in pkg/feature/service.go)
  - CLI wiring: ` + "`" + `listCmd` + "`" + ` cobra command RunE handler (in cmd/feature/list.go)

**Key insight:** You own the FEATURE end-to-end, not a layer or file.`,
		},
		{
			Id:    "wrk-planning-backwards",
			Title: "Planning Backwards from Production Code Path",
			Content: `**Start from the end, plan backwards:**

1. **Identify your production code path:**
   ` + "```bash" + `
   bd show <task-id>  # Look for "productionCodePath" field
   # Example: "cli-tool command list"
   # This is what end users will actually run
   ` + "```" + `

2. **Plan backwards from that end point:**
   ` + "```" + `
   End: User runs ./bin/cli-tool command list
     ↓ (what code handles this?)
   Entry: commandCli.command('list').action(async (options) => { ... })
     ↓ (what service does this call?)
   Service: createFeatureService({ fs, logger, parser, ... })
     ↓ (what method?)
   Method: await service.listItems(options)
     ↓ (what types does method need?)
   Types: ListOptions (input), ListEntry[] (output)
   ` + "```" + `

3. **Identify what you own in each layer:**
   - **L1 Types:** Which types does your slice need?
   - **L2 Tests:** How will you test the production code path?
   - **L3 Implementation + Wiring:** What service methods + CLI wiring needed?

4. **Verify no dual-export anti-pattern:**
   - Your tests must import the same code users run
   - Not a separate test-only function
   - When tests pass, production must work (same code path)`,
		},
		{
			Id:    "wrk-implementation-order",
			Title: "Implementation Order (Layers Within Your Slice)",
			Content: `You implement your vertical slice in layers (TDD approach):

**Layer 1: Types** (only what your slice needs)
` + "```go" + `
// pkg/feature/types.go
// Only add types for YOUR slice (e.g., list command)
type ListOptions struct { /* ... */ }
type ListEntry struct { /* ... */ }
// Don't add types for other slices (e.g., DetailView for other commands)
` + "```" + `

**Layer 2: Tests** (importing production code)
` + "```go" + `
// cmd/feature/list_test.go
package feature_test

import (
    "testing"
    "myproject/cmd/feature"
)

func TestFeatureList(t *testing.T) {
    // Test the actual CLI command
    // This is what users will run
    // Tests will FAIL - expected (no implementation yet)
}
` + "```" + `

Per [B-worker-test-production-code]:
` + "```go" + `
// ✅ CORRECT: Import actual CLI package
import "myproject/cmd/feature"

// ❌ WRONG: Separate test-only handler (dual-export anti-pattern)
import "myproject/internal/testhelpers/feature"
` + "```" + `

**Layer 3: Implementation + Wiring** (make tests pass)
` + "```go" + `
// pkg/feature/service.go
type FeatureServiceDeps struct {
    FS     afero.Fs
    Logger *slog.Logger
}

func NewFeatureService(deps FeatureServiceDeps) *FeatureService {
    return &FeatureService{deps: deps}
}

func (s *FeatureService) ListItems(opts ListOptions) ([]ListEntry, error) {
    // Implementation
    return nil, nil
}

// cmd/feature/list.go
var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List items",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Wire service with REAL dependencies (not mocks)
        service := feature.NewFeatureService(feature.FeatureServiceDeps{
            FS:     osFS{},
            Logger: slog.Default(),
        })

        limit, _ := cmd.Flags().GetInt("limit")
        format, _ := cmd.Flags().GetString("format")
        result, err := service.ListItems(feature.ListOptions{
            Limit:  limit,
            Format: format,
        })
        if err != nil {
            return err
        }

        fmt.Println(formatList(result, format))
        return nil
    },
}
` + "```" + `

Per [wrk-no-stubs], deliver fully wired production code.`,
		},
		{
			Id:    "wrk-tdd-layer-awareness",
			Title: "TDD Layer Awareness (Within Your Slice)",
			Content: `**Layer 2 (your tests):**
- Your tests WILL fail - implementation doesn't exist yet
- This is correct and expected
- Tests import actual production code (CLI command)
- Test failure is OK in Layer 2; typecheck must pass

**Layer 3 (your implementation + wiring):**
- Failing tests from Layer 2 are your specification
- Your job is to make those tests pass
- Wire production code with real dependencies
- Run tests - your tests should now PASS
- If tests fail for unrelated code (other workers' slices), that's OK

**Key insight:** A failing test for unimplemented code is NOT a blocker - it's the specification you're implementing against.`,
		},
		{
			Id:    "wrk-reading-from-beads",
			Title: "Reading from Beads",
			Content: `Get your task details:
` + "```bash" + `
bd show <task-id>
` + "```" + `

Look for:
- ` + "`productionCodePath`" + `: What end users will run (e.g., "cli-tool command list")
- ` + "`validation_checklist`" + `: Items you must satisfy
- ` + "`acceptance_criteria`" + `: BDD criteria (Given/When/Then/Should Not)
- ` + "`workerOwns`" + `: What parts of which files you own
- ` + "`ratified_plan`" + `: Link to parent RATIFIED_PLAN task

Update status on start:
` + "```bash" + `
bd update <task-id> --status=in_progress
` + "```",
		},
		{
			Id:    "wrk-vertical-slice-fields",
			Title: "Vertical Slice Fields (From Beads Task)",
			Content: `- ` + "`slice`" + `: Your slice identifier (e.g., "feature-list")
- ` + "`productionCodePath`" + `: What users run (e.g., "cli-tool command list")
- ` + "`workerOwns.types`" + `: Which types you create
- ` + "`workerOwns.tests`" + `: Which test files you write
- ` + "`workerOwns.implementation`" + `: Which methods/actions you implement
- ` + "`validation_checklist`" + `: Items you must verify (includes production code works)
- ` + "`acceptance_criteria`" + `: BDD criteria for your slice
- ` + "`ratified_plan`" + `: Link to parent plan`,
		},
		{
			Id:    "wrk-followup-slices",
			Title: "Follow-up Slices (FOLLOWUP_SLICE-N)",
			Content: `You may be assigned a ` + "`FOLLOWUP_SLICE-N`" + ` task instead of a ` + "`SLICE-N`" + ` task. The implementation procedure is identical, with these additions:

- **DEFER'd-item leaf tasks**: Your slice task will list specific user-DEFER'd UAT-item leaf tasks that you must resolve. Check ` + "`bd show <task-id>`" + ` for a "DEFER'd-Item Leaf Tasks" section.
- **Dual-parent resolution**: Each leaf task is a child of both the DEFER'd-items tracking group AND your FOLLOWUP_SLICE-N. Resolving the leaf task satisfies both parents.
- **Completion handoff (h4)**: When completing a follow-up slice, your handoff to the reviewer must list which DEFER'd-item leaf tasks were resolved.

` + "```bash" + `
# Completion comment for follow-up slices should include:
bd comments add <task-id> "Implementation complete. Resolved DEFER'd-item leaf tasks: <leaf-task-id-1>, <leaf-task-id-2>"
` + "```",
		},
		{
			Id:    "wrk-updating-beads-status",
			Title: "Updating Beads Status",
			Content: `On start:
` + "```bash" + `
bd update <task-id> --status=in_progress
` + "```" + `

On complete:
` + "```bash" + `
bd update <task-id> --status=done
bd update <task-id> --notes="Implementation complete. Production code verified working via code inspection."
` + "```" + `

On blocked:
` + "```bash" + `
bd update <task-id> --status=blocked
bd update <task-id> --notes="Blocked: <reason>. Need: <dependency or clarification>"
` + "```",
		},
	},
	Behaviors: []BehaviorSpec{
		{
			Id:        "wrk-no-stubs",
			Given:     "completing Layer 3 (implementation + wiring)",
			When:      "finishing a vertical slice",
			Then:      "deliver production code that is fully wired and working end-to-end",
			ShouldNot: "leave TODO placeholders, test-only exports, or unimplemented stubs",
		},
	},
}

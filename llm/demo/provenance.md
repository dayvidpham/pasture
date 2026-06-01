# Provenance Demo Plan

Demos and UATs for `github.com/dayvidpham/provenance` — the PROV-O task tracker replacing Beads.

**Module:** `/home/minttea/codebases/dayvidpham/provenance/`
**Run demos:** `cd ~/codebases/dayvidpham/provenance && go test ./... -count=1 -run <pattern>`

---

## Demo 1: Core Workflow (Beads Replacement)

**What it proves:** provenance can replace `bd create`, `bd show`, `bd update`, `bd close`.

```go
t, _ := provenance.OpenMemory()
defer t.Close()

// Create
task, _ := t.Create("aura-plugins", "REQUEST: Port codegen to Go", "User request verbatim", TaskTypeFeature, PriorityMedium, PhaseRequest)
// task.ID.String() == "aura-plugins--01968a3c-..."

// Show
found, _ := t.Show(task.ID)
// found.Title == "REQUEST: Port codegen to Go"
// found.Status == StatusOpen

// Update
updated, _ := t.Update(task.ID, UpdateFields{Status: ptr(StatusInProgress)})
// updated.Status == StatusInProgress

// Close
closed, _ := t.CloseTask(task.ID, "Implemented and pushed")
// closed.Status == StatusClosed
// closed.CloseReason == "Implemented and pushed"

// List
tasks, _ := t.List(ListFilter{Status: ptr(StatusOpen)})
// len(tasks) == 0 (the only task is closed)
```

**Acceptance criteria:**
- TaskID uses UUIDv7, formatted as `namespace--uuid`
- CRUD lifecycle works: open -> in_progress -> closed
- List filters by status, type, priority

---

## Demo 2: Dependency Graph

**What it proves:** typed edges with readiness detection replaces `bd dep add`, `bd ready`, `bd blocked`.

```go
t, _ := provenance.OpenMemory()

request, _ := t.Create("proj", "REQUEST", "", TaskTypeFeature, PriorityHigh, PhaseRequest)
proposal, _ := t.Create("proj", "PROPOSAL-1", "", TaskTypeTask, PriorityHigh, PhasePropose)
implPlan, _ := t.Create("proj", "IMPL_PLAN", "", TaskTypeTask, PriorityHigh, PhaseImplPlan)
slice1, _ := t.Create("proj", "SLICE-1", "", TaskTypeTask, PriorityMedium, PhaseWorkerSlices)

// Chain: REQUEST <- PROPOSAL <- IMPL_PLAN <- SLICE-1
t.AddEdge(request.ID, proposal.ID.String(), EdgeBlockedBy)
t.AddEdge(proposal.ID, implPlan.ID.String(), EdgeBlockedBy)
t.AddEdge(implPlan.ID, slice1.ID.String(), EdgeBlockedBy)

// Only SLICE-1 is ready (no blockers)
ready, _ := t.Ready()
// len(ready) == 1, ready[0].Title == "SLICE-1"

// REQUEST, PROPOSAL, IMPL_PLAN are blocked
blocked, _ := t.Blocked()
// len(blocked) == 3

// Close SLICE-1 -> IMPL_PLAN becomes ready
t.CloseTask(slice1.ID, "Done")
ready2, _ := t.Ready()
// ready2 includes IMPL_PLAN

// DepTree from REQUEST
edges, _ := t.DepTree(request.ID)
// Shows full chain: request->proposal->implPlan->slice1
```

**Acceptance criteria:**
- `Ready()` returns only unblocked tasks
- `Blocked()` returns tasks with open blockers
- Closing a blocker unblocks its parent
- `DepTree()` returns full transitive chain

---

## Demo 3: Cycle Detection

**What it proves:** EdgeBlockedBy enforces DAG — no circular dependencies.

```go
t, _ := provenance.OpenMemory()

a, _ := t.Create("proj", "Task A", "", TaskTypeTask, PriorityMedium, PhaseRequest)
b, _ := t.Create("proj", "Task B", "", TaskTypeTask, PriorityMedium, PhaseRequest)

t.AddEdge(a.ID, b.ID.String(), EdgeBlockedBy) // A blocked by B: OK

err := t.AddEdge(b.ID, a.ID.String(), EdgeBlockedBy) // B blocked by A: CYCLE
// err == ErrCycleDetected
```

**Acceptance criteria:**
- Direct cycles rejected
- Transitive cycles rejected (A->B->C->A)
- Non-blocking edge kinds (EdgeDerivedFrom, etc.) do NOT enforce cycles

---

## Demo 4: Provenance Edges (PROV-O Lineage)

**What it proves:** typed edges track derivation, supersession, and discovery — the lineage of work.

```go
t, _ := provenance.OpenMemory()

prop1, _ := t.Create("proj", "PROPOSAL-1", "Initial approach", TaskTypeTask, PriorityHigh, PhasePropose)
prop2, _ := t.Create("proj", "PROPOSAL-2", "Revised after review", TaskTypeTask, PriorityHigh, PhasePropose)
prop3, _ := t.Create("proj", "PROPOSAL-3", "Final version", TaskTypeTask, PriorityHigh, PhasePropose)

// Derivation chain
t.AddEdge(prop2.ID, prop1.ID.String(), EdgeDerivedFrom)
t.AddEdge(prop3.ID, prop2.ID.String(), EdgeDerivedFrom)

// Supersession
t.AddEdge(prop2.ID, prop1.ID.String(), EdgeSupersedes)
t.AddEdge(prop3.ID, prop2.ID.String(), EdgeSupersedes)

// Query lineage
derivations, _ := t.Edges(prop3.ID, ptr(EdgeDerivedFrom))
// derivations[0].TargetID == prop2.ID.String()

// Provenance edges do NOT affect readiness
ready, _ := t.Ready()
// All 3 proposals are ready (no EdgeBlockedBy edges)
```

**Acceptance criteria:**
- Multiple edge kinds coexist on the same task pair
- Non-blocking edges don't affect `Ready()`/`Blocked()`
- Lineage is queryable via `Edges(id, &kind)`

---

## Demo 5: PROV-O Agents

**What it proves:** three agent kinds (Human, ML, Software) with compile-time type safety.

```go
t, _ := provenance.OpenMemory()

// Human agent
human, _ := t.RegisterHumanAgent("aura", "David Pham", "dayvidpham@gmail.com")
// human.ID is AgentID (NOT TaskID — compile-time distinct)

// ML agent
supervisor, _ := t.RegisterMLAgent("aura", RoleSupervisor, ProviderAnthropic, "claude-opus-4")
worker, _ := t.RegisterMLAgent("aura", RoleWorker, ProviderAnthropic, "claude-sonnet-4")

// Software agent
bdCli, _ := t.RegisterSoftwareAgent("aura", "beads", "0.4.0", "github.com/dayvidpham/beads")

// Retrieve by kind
h, _ := t.HumanAgent(human.ID)    // works
_, err := t.MLAgent(human.ID)      // ErrAgentKindMismatch
```

**Acceptance criteria:**
- 3 agent subtypes register and retrieve correctly
- Kind mismatch returns `ErrAgentKindMismatch`
- `AgentID` is compile-time distinct from `TaskID`
- ML agents validate (provider, model) pair against seed data

---

## Demo 6: PROV-O Activities

**What it proves:** activities record who did what, when, linked to agents and tasks.

```go
t, _ := provenance.OpenMemory()

agent, _ := t.RegisterMLAgent("aura", RoleSupervisor, ProviderAnthropic, "claude-opus-4")

// Start an activity
activity, _ := t.StartActivity(agent.ID, PhaseImplPlan, StageImplPlan, "Decomposing into slices")
// activity.StartedAt is set, EndedAt is zero

// Create a task and link it to the activity
task, _ := t.Create("aura", "IMPL_PLAN: feature X", "", TaskTypeTask, PriorityHigh, PhaseImplPlan)
t.AddEdge(task.ID, activity.ID.String(), EdgeGeneratedBy)
t.AddEdge(task.ID, agent.ID.String(), EdgeAttributedTo)

// End the activity
ended, _ := t.EndActivity(activity.ID)
// ended.EndedAt is now set

// Query: what has this agent done?
activities, _ := t.Activities(&agent.ID)
// len(activities) == 1
```

**Acceptance criteria:**
- Activities have start/end timestamps
- Linked to tasks via `EdgeGeneratedBy`
- Linked to agents via `EdgeAttributedTo`
- Queryable by agent

---

## Demo 7: Labels + Comments

**What it proves:** organizational metadata with agent-attributed comments.

```go
t, _ := provenance.OpenMemory()

agent, _ := t.RegisterMLAgent("aura", RoleReviewer, ProviderAnthropic, "claude-opus-4")
task, _ := t.Create("proj", "SLICE-1", "", TaskTypeTask, PriorityMedium, PhaseWorkerSlices)

// Labels
t.AddLabel(task.ID, "aura:p9-impl:s9-slice")
t.AddLabel(task.ID, "aura:severity:blocker")
labels, _ := t.Labels(task.ID)
// labels == ["aura:p9-impl:s9-slice", "aura:severity:blocker"]

// Comments with agent attribution
comment, _ := t.AddComment(task.ID, agent.ID, "VOTE: ACCEPT — no blockers found")
// comment.AuthorID == agent.ID
// comment.CreatedAt is set

comments, _ := t.Comments(task.ID)
// len(comments) == 1, chronological order
```

**Acceptance criteria:**
- Labels are idempotent (add twice = one label)
- Comments track author agent and timestamp
- Comments return in chronological order

---

## Demo 8: Persistence

**What it proves:** SQLite persistence survives process restart.

```go
dbPath := "/tmp/provenance-demo.db"

// Session 1: create data
t1, _ := provenance.OpenSQLite(dbPath)
task, _ := t1.Create("proj", "Persistent task", "", TaskTypeFeature, PriorityHigh, PhaseRequest)
taskID := task.ID
t1.Close()

// Session 2: reopen, data survives
t2, _ := provenance.OpenSQLite(dbPath)
found, _ := t2.Show(taskID)
// found.Title == "Persistent task"
t2.Close()

// In-memory: data does NOT survive
mem, _ := provenance.OpenMemory()
memTask, _ := mem.Create("proj", "Ephemeral", "", TaskTypeTask, PriorityMedium, PhaseRequest)
mem.Close()
mem2, _ := provenance.OpenMemory()
_, err := mem2.Show(memTask.ID)
// err == ErrNotFound
```

**Acceptance criteria:**
- `OpenSQLite` persists across close/reopen
- `OpenMemory` is ephemeral
- Schema is applied idempotently on open

---

## Demo 9: Full Epoch Simulation

**What it proves:** the complete PROV-O model works as a Beads replacement with full lineage.

```go
t, _ := provenance.OpenMemory()

// --- Register agents ---
human, _ := t.RegisterHumanAgent("aura", "David Pham", "dayvidpham@gmail.com")
architect, _ := t.RegisterMLAgent("aura", RoleArchitect, ProviderAnthropic, "claude-opus-4")
supervisor, _ := t.RegisterMLAgent("aura", RoleSupervisor, ProviderAnthropic, "claude-opus-4")
workerAgent, _ := t.RegisterMLAgent("aura", RoleWorker, ProviderAnthropic, "claude-sonnet-4")
reviewerA, _ := t.RegisterMLAgent("aura", RoleReviewer, ProviderAnthropic, "claude-opus-4")

// --- Phase 1: REQUEST ---
reqActivity, _ := t.StartActivity(human.ID, PhaseRequest, StageClassify, "User submits request")
request, _ := t.Create("aura", "REQUEST: Port codegen to Go", "verbatim request", TaskTypeFeature, PriorityHigh, PhaseRequest)
t.AddEdge(request.ID, reqActivity.ID.String(), EdgeGeneratedBy)
t.AddEdge(request.ID, human.ID.String(), EdgeAttributedTo)
t.AddLabel(request.ID, "aura:p1-user:s1_1-classify")
t.EndActivity(reqActivity.ID)

// --- Phase 3: PROPOSAL ---
propActivity, _ := t.StartActivity(architect.ID, PhasePropose, StagePropose, "Writing proposal")
proposal, _ := t.Create("aura", "PROPOSAL-1: codegen port", "full plan", TaskTypeTask, PriorityHigh, PhasePropose)
t.AddEdge(proposal.ID, propActivity.ID.String(), EdgeGeneratedBy)
t.AddEdge(proposal.ID, architect.ID.String(), EdgeAttributedTo)
t.AddEdge(request.ID, proposal.ID.String(), EdgeBlockedBy) // REQUEST blocked by PROPOSAL
t.AddLabel(proposal.ID, "aura:p3-plan:s3-propose")
t.EndActivity(propActivity.ID)

// --- Phase 4: REVIEW ---
t.AddComment(proposal.ID, reviewerA.ID, "VOTE: ACCEPT — correctness verified")

// --- Phase 8: IMPL_PLAN ---
planActivity, _ := t.StartActivity(supervisor.ID, PhaseImplPlan, StageImplPlan, "Decomposing")
implPlan, _ := t.Create("aura", "IMPL_PLAN: 3 slices", "", TaskTypeTask, PriorityHigh, PhaseImplPlan)
t.AddEdge(implPlan.ID, planActivity.ID.String(), EdgeGeneratedBy)
t.AddEdge(proposal.ID, implPlan.ID.String(), EdgeBlockedBy)
slice1, _ := t.Create("aura", "SLICE-1: types", "", TaskTypeTask, PriorityMedium, PhaseWorkerSlices)
t.AddEdge(implPlan.ID, slice1.ID.String(), EdgeBlockedBy)
t.EndActivity(planActivity.ID)

// --- Readiness check ---
ready, _ := t.Ready()
// Only SLICE-1 is ready (leaf of the chain)

// --- Phase 9: Worker implements ---
implActivity, _ := t.StartActivity(workerAgent.ID, PhaseWorkerSlices, StageWorkerSlices, "Implementing SLICE-1")
t.AddEdge(slice1.ID, implActivity.ID.String(), EdgeGeneratedBy)
t.CloseTask(slice1.ID, "Tests pass, committed")
t.EndActivity(implActivity.ID)

// IMPL_PLAN is now ready
ready2, _ := t.Ready()
// ready2 includes IMPL_PLAN

// --- Full provenance query ---
ancestors, _ := t.Ancestors(request.ID)
// ancestors traces: proposal -> implPlan -> slice1

descendants, _ := t.Descendants(slice1.ID)
// descendants traces: implPlan -> proposal -> request
```

**Acceptance criteria:**
- Full epoch lifecycle works end-to-end
- Provenance edges track who/what/when at every step
- Readiness propagates correctly as work completes
- Ancestors/Descendants provide full lineage traversal
- Agent attribution on every task and activity

---

## Running Demos

These demos can be implemented as integration tests:

```bash
# Run all demos
cd ~/codebases/dayvidpham/provenance
go test ./... -count=1 -run TestDemo -v

# Run specific demo
go test ./... -count=1 -run TestDemo_FullEpochSimulation -v
```

Or as a CLI walkthrough once `pasture task` is wired.

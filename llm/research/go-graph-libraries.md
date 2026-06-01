# Go Graph Library Research

**Date:** 2026-03-23
**Purpose:** Evaluate pure-Go graph libraries for a task dependency tracker needing DAG operations: add/remove nodes and edges, cycle detection, topological sort, ancestor/descendant traversal, and "what blocks this?" / "what does this block?" queries.
**Node scale:** < 10 000 tasks (in-memory, single process)

---

## Comparison Table

| Library | Import path | Latest release | Stars | Zero-dep | CGo | Topo sort | Cycle detect | DFS/BFS traversal | Ancestors/Descendants | API maturity |
|---|---|---|---|---|---|---|---|---|---|---|
| **dominikbraun/graph** | `github.com/dominikbraun/graph` | v0.23.0 (Jul 2024) | ~2 100 | Yes (0 deps) | No | Yes (`TopologicalSort`, `StableTopologicalSort`) | Yes (`PreventCycles`, `CreatesCycle`) | Yes (`DFS`, `BFS`, `BFSWithDepth`) | Via `PredecessorMap`+`AdjacencyMap`+DFS | pre-v1 |
| **gonum/graph** | `gonum.org/v1/gonum/graph` | v0.15.x | ~7 500 (gonum) | No (18 deps) | No | Yes (`topo.Sort`, `topo.SortStabilized`) | Yes (`topo.DirectedCyclesIn`, `topo.TarjanSCC`) | Yes (`traverse` subpkg) | Via DFS/BFS on `Directed` interface | pre-v1 but very stable in practice |
| **yourbasic/graph** | `yourbasic.org/graph` | v1.0.5 (Sep 2017) | ~750 | Unknown | No | Yes | No explicit API | Yes (BFS/DFS) | Implicit via BFS/DFS | v1, abandoned since 2017 |
| **hmdsefi/gograph** | `github.com/hmdsefi/gograph` | v0.7.0 (Sep 2025) | ~108 | Yes (2 deps) | No | Yes (`traverse.TopologySort`) | Yes (`Acyclic()` option) | Yes (BFS, DFS, closest-first, random walk) | Via traversal | pre-v1, small community |
| **souleb/dag** | `github.com/souleb/dag` | v0.1.0 (Oct 2023) | ~0 | Yes (4 deps) | No | Yes (`TopologicalSort`) | Yes (`Cycles`, Tarjan) | No dedicated API | No | pre-v1, inactive |
| **go.arcalot.io/dgraph** | `go.arcalot.io/dgraph` | v1.7.0 (Sep 2024) | ~1 | Yes | No | Via `ListNodesWithoutInboundConnections` + `PopReadyNodes` | Yes (`HasCycles`) | No dedicated API | Via `ListInboundConnections` / `ListOutboundConnections` | v1, niche/internal |
| **gammazero/toposort** | `github.com/gammazero/toposort` | v0.2.0 (Feb 2026) | <10 | Yes (2 deps) | No | Yes (`Toposort`, `ToposortR`) | Yes (returns error) | No | No | pre-v1, topo-only |
| **Hand-rolled adjacency list** | (no import) | n/a | n/a | Yes (0 deps) | No | Kahn's (< 50 LOC) | DFS (< 30 LOC) | Trivial | Trivial | Exactly what you need |

---

## Detailed Notes per Library

### 1. dominikbraun/graph

**Import:** `github.com/dominikbraun/graph`
**Latest release:** v0.23.0 — 5 July 2024
**Stars:** ~2 100
**CGo:** No
**External dependencies:** Zero — `go.mod` contains only `module` + `go 1.18`
**License:** Apache-2.0

#### API coverage

```go
// Create a directed, cycle-preventing DAG keyed by string
g := graph.New(graph.StringHash, graph.Directed(), graph.PreventCycles())

g.AddVertex("task-a")
g.AddVertex("task-b")
g.AddEdge("task-a", "task-b")  // returns ErrEdgeCreatesCycle if would cycle

// Topological sort (non-deterministic)
order, err := graph.TopologicalSort(g)

// Stable/deterministic topological sort
order, err := graph.StableTopologicalSort(g, func(a, b string) bool { return a < b })

// Check if adding an edge would create a cycle without actually adding it
wouldCycle, err := graph.CreatesCycle(g, "task-b", "task-a")

// DFS from a root node
graph.DFS(g, "task-a", func(vertex string) bool {
    fmt.Println(vertex)
    return false // return true to stop early
})

// "What does task-a block?" — follow AdjacencyMap forward
adj, _ := g.AdjacencyMap()
// adj["task-a"] = map of direct successors

// "What blocks task-b?" — follow PredecessorMap backward
pred, _ := g.PredecessorMap()
// pred["task-b"] = map of direct predecessors
```

**Ancestors/descendants:** There is no single `Ancestors(node)` or `Descendants(node)` function. You must compose: call `DFS` starting from a node (using `AdjacencyMap` direction for descendants, or reverse-DFS via `PredecessorMap` for ancestors). This is ~10 lines of user code. The library's `AllPathsBetween` can enumerate all reachable paths but is expensive; DFS is the right primitive.

**Cycle detection strategy:** Two modes:
- Preventive: `graph.New(..., graph.PreventCycles())` — `AddEdge` returns `ErrEdgeCreatesCycle` immediately.
- Query: `graph.CreatesCycle(g, src, dst)` — check without modifying the graph.
- `StronglyConnectedComponents` can also identify cycles after the fact.

**Transitive reduction:** `graph.TransitiveReduction(g)` — O(V(V+E)), removes redundant edges while preserving reachability.

**Gotchas:**
- API is pre-v1; breaking changes are possible, though the project has been stable in practice across many releases.
- `TopologicalSort` is non-deterministic (map iteration order); use `StableTopologicalSort` with a comparator for reproducible output.
- No built-in `Ancestors(node)` — you compose it from DFS + PredecessorMap.
- The `Store` interface allows plugging in a persistent backend (e.g., SQLite), which is rare in this class of library.

**Assessment:** Best general-purpose graph library for this use case. Zero dependencies, generic, feature-rich, actively maintained through mid-2024, community of ~2100 stars. The absence of a direct `Ancestors()` function is a minor inconvenience easily papered over with a 10-line helper.

---

### 2. gonum/graph

**Import:** `gonum.org/v1/gonum/graph` (+ subpackages `graph/topo`, `graph/traverse`)
**Latest release:** v0.15.x (part of the gonum monorepo)
**Stars:** ~7 500 (entire gonum repo)
**CGo:** No
**External dependencies:** 18 total (3 direct, 15 indirect) — includes `golang.org/x/tools`, `gonum.org/v1/plot`, font and PDF libraries. Most are dev/test/visualization tools; runtime dependency is much smaller, but they are present in `go.mod` and affect `go mod tidy`.
**License:** BSD-3-Clause

#### API coverage

```go
// gonum uses int64 node IDs and interface-based design
import (
    "gonum.org/v1/gonum/graph"
    "gonum.org/v1/gonum/graph/simple"
    "gonum.org/v1/gonum/graph/topo"
    "gonum.org/v1/gonum/graph/traverse"
)

g := simple.NewDirectedGraph()
g.AddNode(simple.Node(1))
g.AddNode(simple.Node(2))
g.SetEdge(g.NewEdge(simple.Node(1), simple.Node(2)))

// Topological sort
sorted, err := topo.Sort(g)       // returns []graph.Node
// Stable sort
sorted, err = topo.SortStabilized(g, func(nodes []graph.Node) { /* sort by ID */ })

// Cycle detection
cycles := topo.DirectedCyclesIn(g)    // [][]graph.Node
sccs   := topo.TarjanSCC(g)           // [][]graph.Node

// DFS/BFS
var df traverse.DepthFirst
df.Walk(g, startNode, func(n graph.Node) bool { return false })
```

**Ancestors/descendants:** No dedicated function. Walk the graph with `traverse.DepthFirst` or `traverse.BreadthFirst`, following `g.From(id)` (successors) or `g.To(id)` (predecessors).

**Gotchas:**
- Interface-heavy design — you need concrete graph types from `graph/simple`, `graph/multi`, etc. Boilerplate is significant.
- Nodes must be `int64` IDs. Mapping string task IDs to `int64` requires a separate index.
- 18 dependencies is excessive for a task tracker. While most are indirect/test-only, they appear in `go.mod` and complicate auditing.
- The library is designed for scientific computing (network analysis, spectral graph theory, flow algorithms) — much of it is irrelevant here.
- `graph.Node` interface (`ID() int64`) forces a specific data model.

**Assessment:** Excellent library for numeric/scientific graph work. Over-engineered for a task dependency graph. The dependency footprint and the mandatory `int64` node IDs are friction points. Acceptable if you are already in the gonum ecosystem.

---

### 3. yourbasic/graph

**Import:** `yourbasic.org/graph`
**Latest release:** v1.0.5 — 21 September 2017
**Stars:** ~750
**CGo:** No
**External dependencies:** Unknown (likely zero, given the era)
**License:** BSD-2-Clause

#### API coverage

The library uses integer vertex labels (0 to n-1) in a fixed-size adjacency list. Vertices are fixed at construction time. Provides: BFS, DFS, topological sort, minimum spanning tree, shortest path. Cycle detection is implicit through topological sort failure.

**Gotchas:**
- **Abandoned since 2017.** No commits in 8+ years.
- **Fixed vertex count** — you must know n upfront. Incompatible with dynamic task addition.
- Integer-only labels require a separate string→int index.
- No generic support (pre-Go 1.18).

**Assessment:** Do not use. Abandoned, pre-generics, requires fixed vertex count.

---

### 4. hmdsefi/gograph

**Import:** `github.com/hmdsefi/gograph`
**Latest release:** v0.7.0 — September 2025 (active)
**Stars:** ~108
**CGo:** No
**External dependencies:** 2 direct imports (details not confirmed; library self-describes as minimal)
**License:** Apache-2.0

#### API coverage

```go
import (
    "github.com/hmdsefi/gograph"
    "github.com/hmdsefi/gograph/traverse"
)

g := gograph.New[string](gograph.Acyclic())  // Directed + cycle-preventing

vA := g.AddVertexByLabel("task-a")
vB := g.AddVertexByLabel("task-b")
g.AddEdge(vA, vB)  // returns ErrDAGCycle if would create cycle

// Topological sort
sorted, err := traverse.TopologySort(g)

// BFS/DFS via traverse package
// Vertex methods for neighbors
vA.Neighbors()   // []*Vertex — direct neighbors
vA.InDegree()    // number of incoming edges
vA.OutDegree()   // number of outgoing edges
```

**Ancestors/descendants:** Via `Vertex.Neighbors()` (outbound) and the graph's traversal. The `InDegree`/`OutDegree` methods indicate direction; full ancestor traversal requires DFS.

**Gotchas:**
- Small community (~108 stars). Higher bus-factor risk.
- Latest version is v0.7.0 — semantically close to dominikbraun/graph but less battle-tested.
- Dependency count not fully confirmed from public docs; needs `go mod download` to verify.

**Assessment:** Reasonable alternative to dominikbraun/graph, active as of September 2025, but significantly smaller community. Worth watching, not worth choosing over a more proven library for a new project.

---

### 5. souleb/dag

**Import:** `github.com/souleb/dag`
**Latest release:** v0.1.0 — October 2023
**Stars:** ~0
**CGo:** No
**External dependencies:** 4 imports
**License:** MIT

#### API coverage

Implements Kahn's algorithm for topological sort and Tarjan's algorithm for cycle detection. Clean, minimal API (`Add`, `AddEdge`, `TopologicalSort`, `Cycles`).

**Gotchas:**
- Effectively unmaintained (last commit December 2023, v0.1.0).
- Near-zero community.
- No traversal primitives for ancestor/descendant queries.

**Assessment:** Too immature and inactive. Not suitable.

---

### 6. go.arcalot.io/dgraph (arcalot/go-dgraph)

**Import:** `go.arcalot.io/dgraph`
**Latest release:** v1.7.0 — September 2024
**Stars:** ~1
**CGo:** No
**External dependencies:** Not enumerated in docs; likely minimal
**License:** Apache-2.0

#### API coverage

Unique among candidates: built around **execution-time dependency resolution** (AND/OR/optional/completion dependency types). Nodes are resolved at runtime via `PopReadyNodes()`, which returns nodes whose dependencies are all satisfied. This is the key distinguishing feature.

```go
g := dgraph.New[MyTask]()
nodeA, _ := g.AddNode("task-a", taskA)
nodeB, _ := g.AddNode("task-b", taskB)
nodeA.Connect("task-b")  // task-b depends on task-a

g.HasCycles()     // bool
g.PopReadyNodes() // map[string]ResolutionStatus — tasks with all deps satisfied
nodeA.ListInboundConnections()   // predecessors
nodeA.ListOutboundConnections()  // successors
```

**Gotchas:**
- v1 but niche — written for Arcaflow workflow engine. The `ResolveNode`/`PopReadyNodes` model is opinionated and may not map cleanly to task-tracker semantics.
- 1 GitHub star indicates near-zero external adoption.
- No generic topological sort output — "ready nodes" is the primary mechanism.

**Assessment:** Interesting design for workflow engines. The execution-model API (`PopReadyNodes`, `ResolveNode`) is well-suited for task runners, less so for a general-purpose tracker. Small community is a significant risk.

---

### 7. gammazero/toposort

**Import:** `github.com/gammazero/toposort`
**Latest release:** v0.2.0 — February 2026
**Stars:** <10
**CGo:** No
**External dependencies:** 2
**License:** MIT

#### API coverage

Extremely focused: given a list of `Edge[T]` pairs, return a topologically sorted slice. One function, one type.

```go
sorted, err := toposort.Toposort([]toposort.Edge[string]{
    {"a", "b"}, {"b", "c"},
})
```

**Gotchas:**
- Not a graph data structure — it operates on an edge list input.
- No persistent graph object; no add/remove node, no traversal, no ancestor queries.
- Suitable only as a utility function, not as the graph layer of a tracker.

**Assessment:** Wrong abstraction level for a task tracker. Useful if you just need topo sort over an ad-hoc list.

---

## Hand-Rolled Adjacency List

For a task tracker with < 10 000 nodes and in-process use, all required graph operations can be implemented in a single Go file of ~150 lines. Below is the complete operation map:

| Operation | Implementation | LOC estimate |
|---|---|---|
| Add node | `map[ID]struct{}` insert | 3 |
| Add edge | `map[ID][]ID` append | 5 |
| Remove node | Delete from node map + filter edge maps | 15 |
| Successors ("what does this block?") | Return `adj[id]` directly | 3 |
| Predecessors ("what blocks this?") | Return `pred[id]` directly | 3 |
| All descendants (transitive) | DFS over `adj` | 20 |
| All ancestors (transitive) | DFS over `pred` | 20 |
| Cycle detection | DFS with gray/black coloring | 25 |
| Topological sort | Kahn's algorithm | 30 |
| "Ready" tasks (no unmet deps) | Filter nodes where `len(pred[id]) == 0` or all predecessors resolved | 10 |

Total: ~130 lines for the full operation set, plus tests.

**Advantages:**
- Zero dependencies.
- Typed exactly to your domain (task IDs can be strings, UUIDs, or any comparable type).
- No translation layer between library types and your domain types.
- Trivially auditable.
- Deterministic behavior — no surprises from upstream changes.

**Disadvantages:**
- You own the implementation and bugs.
- Needs tests (which you should write anyway for a critical path component).
- No built-in visualization, transitive reduction, or path enumeration — add only what you need.

---

## Recommendation

### Primary recommendation: hand-rolled adjacency list

For a task tracker replacing Beads/bd, **write your own DAG package** (`internal/dag` or `pkg/dag`). The complete operation set is ~130 lines. This is the right choice because:

1. **Exact fit.** You control the node type (no mandatory `int64` IDs, no hash function boilerplate). Task IDs map directly.
2. **Zero dependencies.** Matches the `CGO_ENABLED=0` pure-Go constraint with no transitive risk.
3. **The problem is not hard.** Kahn's topological sort and gray-coloring cycle detection are textbook algorithms. At < 10 000 nodes there are no performance concerns — even a naive O(V+E) Kahn's runs in microseconds.
4. **You already need tests.** A hand-rolled implementation forces you to write tests that define exactly the semantics you need, which is valuable for a correctness-critical component.
5. **API ownership.** The beads-replacement likely needs domain-specific query shapes (e.g., "ready tasks with priority > 2"), which are awkward to express through a generic library's API.

**Suggested package layout:**

```
internal/dag/
    dag.go          # DAGGraph type + AddNode, AddEdge, RemoveNode, RemoveEdge
    query.go        # Successors, Predecessors, Descendants, Ancestors
    topo.go         # TopologicalSort (Kahn's), ReadyNodes
    cycles.go       # HasCycle, DetectCycle (returns the cycle path)
    dag_test.go     # Integration tests covering all operations
```

### Secondary recommendation: dominikbraun/graph (if you prefer a library)

If you want a library rather than a hand-rolled implementation, **dominikbraun/graph** is the clear choice:

- Zero external dependencies — fully compatible with `CGO_ENABLED=0` and the project's minimal-dependency policy.
- The most complete algorithm set in this class: `TopologicalSort`, `StableTopologicalSort`, `CreatesCycle`, `PreventCycles`, `DFS`, `BFS`, `AllPathsBetween`, `TransitiveReduction`, `StronglyConnectedComponents`.
- Active development through mid-2024 with 2 100+ stars — the most adopted library in this space.
- Generic (`Graph[K comparable, T any]`) — you use string task IDs as keys directly.
- The only missing pieces (`Ancestors(node)`, `Descendants(node)`) are trivially composed from `PredecessorMap` + `DFS` in ~10 lines.

The main risk is pre-v1 API stability, though the release cadence (20+ releases in 2024 alone) suggests active maintenance.

### Do not use

| Library | Reason |
|---|---|
| yourbasic/graph | Abandoned 2017, fixed vertex count, no generics |
| souleb/dag | Effectively inactive, near-zero community |
| gammazero/toposort | Topo-sort utility only, not a graph data structure |
| go.arcalot.io/dgraph | 1 star, niche execution-model API, not general-purpose |
| gonum/graph | 18 transitive deps, int64-centric API, designed for scientific computing |
| hmdsefi/gograph | Viable but ~108 stars vs dominikbraun's 2100; pick dominikbraun if using a library |

---

## References

- [dominikbraun/graph on GitHub](https://github.com/dominikbraun/graph)
- [dominikbraun/graph on pkg.go.dev](https://pkg.go.dev/github.com/dominikbraun/graph)
- [gonum/graph on pkg.go.dev](https://pkg.go.dev/gonum.org/v1/gonum/graph)
- [gonum/graph/topo on pkg.go.dev](https://pkg.go.dev/gonum.org/v1/gonum/graph/topo)
- [hmdsefi/gograph on GitHub](https://github.com/hmdsefi/gograph)
- [go.arcalot.io/dgraph on pkg.go.dev](https://pkg.go.dev/go.arcalot.io/dgraph)
- [gammazero/toposort on pkg.go.dev](https://pkg.go.dev/github.com/gammazero/toposort)
- [souleb/dag on pkg.go.dev](https://pkg.go.dev/github.com/souleb/dag)
- [gonum go.mod](https://github.com/gonum/gonum/blob/master/go.mod)

package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCLI_TaskWorkflow(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "workflow.db")
	tr, err := tasks.OpenTaskTracker(db)
	require.NoError(t, err)
	author, err := tr.RegisterHumanAgent("test", "Test author", "")
	require.NoError(t, err)
	require.NoError(t, tr.Close())

	run := func(args ...string) string {
		t.Helper()
		r := runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
		require.Equal(t, 0, r.exitCode, "%v: %s", args, r.stderr)
		require.Empty(t, r.stderr)
		return r.stdout
	}

	decode := func(s string) map[string]any {
		t.Helper()
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(s), &v))
		return v
	}

	ids := func(s string) []string {
		t.Helper()
		var rows []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(s), &rows))
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.ID
		}
		return out
	}

	a := decode(run(
		"create", "A",
		"--priority", "high",
		"--type", "feature",
		"--namespace", "alpha",
		"--phase", "request",
	))["id"].(string)
	b := decode(run("create", "B", "--namespace", "beta"))["id"].(string)

	require.Equal(t, []string{a, b}, ids(run("list")))
	require.Equal(t, []string{a, b}, ids(run("ready")))
	require.Equal(t, []string{a}, ids(run("list", "--namespace", "alpha", "--phase", "request")))
	require.Empty(t, ids(run("list", "--namespace", "beta", "--phase", "request")))

	edge := decode(run("dep", "add", a, "--blocked-by", b))
	require.Equal(t, a, edge["sourceId"])
	require.Equal(t, b, edge["targetId"])
	require.Equal(t, "blocked_by", edge["kind"])
	require.ElementsMatch(t, []string{a, b}, ids(run("list")))
	require.Equal(t, []string{b}, ids(run("ready")))
	require.Equal(t, []string{a}, ids(run("blocked")))

	for range 2 {
		require.Equal(t, []any{"work"}, decode(run("label", "add", a, "work"))["labels"])
	}
	require.Equal(t, []any{"work"}, decode(run("label", "list", a))["labels"])
	text := runCLI(t, "--db", db, "task", "label", "list", a)
	require.Zero(t, text.exitCode)
	require.Contains(t, text.stdout, "labels: work")
	require.Empty(t, ids(run("ready", "--label", "work")))
	require.Equal(t, []string{a}, ids(run("blocked", "--label", "work")))
	require.Equal(t, []string{a}, ids(run("list", "--label", "work", "--type", "feature", "--priority", "P1", "--status", "open")))
	require.Empty(t, ids(run("list", "--label", "work", "--type", "bug")))

	c := decode(run("comment", "add", a, "Checked blocker", "--author", author.ID.String()))
	require.Equal(t, author.ID.String(), c["authorId"])
	var cs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(run("comments", a)), &cs))
	require.Len(t, cs, 1)
	require.Equal(t, "Checked blocker", cs[0]["body"])
	require.Equal(t, c, cs[0])

	run("comment", "add", a, "Second check", "--author", author.ID.String())
	require.NoError(t, json.Unmarshal([]byte(run("comments", a)), &cs))
	require.Len(t, cs, 2)
	require.Equal(t, "Checked blocker", cs[0]["body"])
	require.Equal(t, "Second check", cs[1]["body"])
	text = runCLI(t, "--db", db, "task", "comments", a)
	require.Zero(t, text.exitCode)
	require.Less(t, strings.Index(text.stdout, "Checked blocker"), strings.Index(text.stdout, "Second check"))

	run("close", b)
	require.Equal(t, []string{a}, ids(run("ready")))
	require.Empty(t, ids(run("blocked")))
	require.Equal(t, []string{a}, ids(run("ready", "--label", "work")))
	require.Empty(t, ids(run("blocked", "--label", "work")))

	for range 2 {
		require.Equal(t, []any{}, decode(run("label", "remove", a, "work"))["labels"])
	}

	run("close", a)
	require.Empty(t, ids(run("ready")))
	require.Empty(t, ids(run("blocked")))
	require.Len(t, ids(run("list", "--status", "closed")), 2)
}

func TestCLI_TaskDependencyAliasesAndValidation(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	run := func(args ...string) runOutcome {
		return runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
	}

	a := run("create", "A")
	b := run("create", "B")
	require.Zero(t, a.exitCode)
	require.Zero(t, b.exitCode)
	var av, bv map[string]any
	require.NoError(t, json.Unmarshal([]byte(a.stdout), &av))
	require.NoError(t, json.Unmarshal([]byte(b.stdout), &bv))
	ai, bi := av["id"].(string), bv["id"].(string)

	for _, group := range []string{"dep", "relation"} {
		for _, kind := range []string{"blocked_by", "derived_from", "supersedes", "discovered_from"} {
			got := run(group, "add", ai, "--target", bi, "--kind", kind)
			require.Zero(t, got.exitCode, "%s", got.stderr)
			var e map[string]any
			require.NoError(t, json.Unmarshal([]byte(got.stdout), &e))
			require.Equal(t, kind, e["kind"])
		}
	}

	dep, alias := run("dep", "tree", ai), run("relation", "tree", ai)
	require.Zero(t, dep.exitCode)
	require.Zero(t, alias.exitCode)
	require.Equal(t, dep.stdout, alias.stdout)
	var tree struct {
		Edges []struct {
			Kind string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(dep.stdout), &tree))
	require.Len(t, tree.Edges, 1)
	require.Equal(t, "blocked_by", tree.Edges[0].Kind)

	for _, target := range []string{ai, bi} {
		got := run("dep", "add", bi, "--blocked-by", target)
		require.Equal(t, 3, got.exitCode)
		require.Empty(t, got.stdout)
	}

	for _, flag := range []string{"--target", "--kind"} {
		got := run("dep", "add", bi, "--blocked-by", ai, flag, ai)
		require.Equal(t, 1, got.exitCode)
		require.Contains(t, got.stderr, "cannot mix")
		require.Contains(t, got.stderr, "--target TARGET --kind KIND")
	}

	got := run("dep", "tree", bi)
	require.Zero(t, got.exitCode)
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &tree))
	require.Empty(t, tree.Edges)

	for _, flag := range []string{"--status", "--priority", "--type", "--phase"} {
		path := absentDB(t)
		got := runCLI(t, "--db", path, "task", "list", flag, "not-valid")
		require.Equal(t, 1, got.exitCode)
		require.Contains(t, got.stderr, flag)
		_, err := os.Stat(path)
		require.True(t, os.IsNotExist(err))
	}
}

func TestCLI_TaskAuthorValidationDoesNotWrite(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	run := func(args ...string) runOutcome {
		return runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
	}

	created := run("create", "Needs author")
	require.Zero(t, created.exitCode)
	var task map[string]any
	require.NoError(t, json.Unmarshal([]byte(created.stdout), &task))
	id := task["id"].(string)
	before := run("agents", "list")
	require.Zero(t, before.exitCode)

	for _, author := range []string{"", "malformed", "unknown--00000000-0000-0000-0000-000000000000"} {
		args := []string{"comment", "add", id, "No write"}
		if author != "" {
			args = append(args, "--author", author)
		}
		got := run(args...)
		require.Equal(t, 1, got.exitCode)
		require.Empty(t, got.stdout)
		require.Contains(t, got.stderr, "pasture task agents list")
		require.Contains(t, got.stderr, "--author")
	}

	after := run("agents", "list")
	require.Zero(t, after.exitCode)
	require.Equal(t, before.stdout, after.stdout)
	comments := run("comments", id)
	require.Zero(t, comments.exitCode)
	require.Equal(t, "[]\n", comments.stdout)
}

func TestCLI_TaskHelpAndDocumentationAgree(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join(moduleRoot(), "AGENTS.md"))
	require.NoError(t, err)
	start := strings.Index(string(doc), "### `pasture task` subcommands")
	end := strings.Index(string(doc)[start:], "## Dependencies") + start
	inventory := string(doc)[start:end]

	for _, name := range []string{"list", "ready", "blocked", "label", "comments", "agents", "dep", "events", "contexts"} {
		help := runCLI(t, "task", name, "--help")
		require.Zero(t, help.exitCode)
		require.Contains(t, inventory, "pasture task "+name)
	}

	for _, group := range []string{"dep", "relation"} {
		for _, verb := range []string{"add", "tree"} {
			help := runCLI(t, "task", group, verb, "--help")
			require.Zero(t, help.exitCode)
			require.Contains(t, help.stdout, "pasture task dep "+verb)
		}
	}
}

// depTreeCLIFixture creates A,B,C,D,X and the cross-kind cycle plus diamond
// used by the tree-kind tests, and returns the task IDs.
func depTreeCLIFixture(t *testing.T, db string) (a, b, c, d, x string) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		got := runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
		require.Zero(t, got.exitCode, "%v: %s", args, got.stderr)
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(got.stdout), &v))
		return v["id"].(string)
	}
	add := func(src, tgt, kind string) {
		t.Helper()
		got := runCLI(t, "--db", db, "task", "dep", "add", src, "--target", tgt, "--kind", kind)
		require.Zero(t, got.exitCode, "%s->%s %s: %s", src, tgt, kind, got.stderr)
	}

	a, b, c, d, x = run("create", "A"), run("create", "B"), run("create", "C"), run("create", "D"), run("create", "X")
	add(a, b, "supersedes")
	add(b, c, "derived_from")
	add(c, a, "supersedes")
	add(a, d, "derived_from")
	add(b, d, "supersedes")
	add(d, c, "discovered_from")
	add(x, a, "supersedes")
	return a, b, c, d, x
}

func TestCLI_TaskDepTreeKindSelection(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	a, b, c, d, x := depTreeCLIFixture(t, db)

	// All kinds: exact text, deterministic across runs, incoming absent.
	first := runCLI(t, "--db", db, "task", "dep", "tree", a, "--kind", "all")
	require.Zero(t, first.exitCode, "%s", first.stderr)
	second := runCLI(t, "--db", db, "task", "dep", "tree", a, "--kind", "all")
	require.Zero(t, second.exitCode)
	require.Equal(t, first.stdout, second.stdout)
	wantText := a + "\n" +
		"    ├── derived from " + d + "\n" +
		"    │   └── discovered from " + c + "\n" +
		"    │       └── supersedes " + a + " (already shown)\n" +
		"    └── supersedes " + b + "\n" +
		"        ├── derived from " + c + " (already shown)\n" +
		"        └── supersedes " + d + " (already shown)\n"
	require.Equal(t, wantText, first.stdout)
	require.NotContains(t, first.stdout, x)

	// The relation alias accepts the same selection.
	alias := runCLI(t, "--db", db, "task", "relation", "tree", a, "--kind", "all")
	require.Zero(t, alias.exitCode)
	require.Equal(t, first.stdout, alias.stdout)

	// JSON mirrors the text order and flags exactly the repeated edges.
	jsonOut := runCLI(t, "--db", db, "--format", "json", "task", "dep", "tree", a, "--kind", "all")
	require.Zero(t, jsonOut.exitCode, "%s", jsonOut.stderr)
	var tree struct {
		Root  string `json:"root"`
		Edges []struct {
			SourceID string `json:"sourceId"`
			TargetID string `json:"targetId"`
			Kind     string `json:"kind"`
			Repeated bool   `json:"repeated"`
		} `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOut.stdout), &tree))
	require.Equal(t, a, tree.Root)
	wantEdges := []struct {
		source, target, kind string
		repeated             bool
	}{
		{a, d, "derived_from", false},
		{d, c, "discovered_from", false},
		{c, a, "supersedes", true},
		{a, b, "supersedes", false},
		{b, c, "derived_from", true},
		{b, d, "supersedes", true},
	}
	require.Len(t, tree.Edges, len(wantEdges))
	for i, w := range wantEdges {
		require.Equal(t, w.source, tree.Edges[i].SourceID, "edge %d source", i)
		require.Equal(t, w.target, tree.Edges[i].TargetID, "edge %d target", i)
		require.Equal(t, w.kind, tree.Edges[i].Kind, "edge %d kind", i)
		require.Equal(t, w.repeated, tree.Edges[i].Repeated, "edge %d repeated", i)
	}

	// Single kind: only that kind is followed and labeled; no cross-kind
	// expansion into the derived_from edges.
	super := runCLI(t, "--db", db, "task", "dep", "tree", a, "--kind", "supersedes")
	require.Zero(t, super.exitCode, "%s", super.stderr)
	require.Equal(t, a+"\n    └── supersedes "+b+"\n        └── supersedes "+d+"\n", super.stdout)
	require.NotContains(t, super.stdout, "derived from")
	require.NotContains(t, super.stdout, "discovered from")

	derived := runCLI(t, "--db", db, "task", "dep", "tree", a, "--kind", "derived_from")
	require.Zero(t, derived.exitCode, "%s", derived.stderr)
	require.Equal(t, a+"\n    └── derived from "+d+"\n", derived.stdout)
	require.NotContains(t, derived.stdout, "supersedes")
}

func TestCLI_TaskDepTreeDefaultBlockedByUnchanged(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	run := func(args ...string) string {
		t.Helper()
		got := runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
		require.Zero(t, got.exitCode, "%v: %s", args, got.stderr)
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(got.stdout), &v))
		return v["id"].(string)
	}
	add := func(src, tgt string) {
		t.Helper()
		got := runCLI(t, "--db", db, "task", "dep", "add", src, "--blocked-by", tgt)
		require.Zero(t, got.exitCode, "%s", got.stderr)
	}

	e, f, g, h := run("create", "E"), run("create", "F"), run("create", "G"), run("create", "H")
	add(e, f)
	add(e, g)
	add(f, h)
	add(g, h)

	def := runCLI(t, "--db", db, "task", "dep", "tree", e)
	explicit := runCLI(t, "--db", db, "task", "dep", "tree", e, "--kind", "blocked_by")
	require.Zero(t, def.exitCode, "%s", def.stderr)
	require.Zero(t, explicit.exitCode, "%s", explicit.stderr)
	require.Equal(t, def.stdout, explicit.stdout)
	wantText := e + "\n" +
		"    ├── blocked by " + f + "\n" +
		"    │   └── blocked by " + h + "\n" +
		"    └── blocked by " + g + "\n" +
		"        └── blocked by " + h + "\n"
	require.Equal(t, wantText, def.stdout)

	// The pre-change JSON template, byte for byte: two-space indent, one
	// object per line, the edge order normalizeDepTree produces, and the
	// trailing newline fmt.Fprintln adds. A whitespace or key-order regression
	// in the legacy path would fail here.
	wantJSON := "{\n" +
		"  \"root\": \"" + e + "\",\n" +
		"  \"edges\": [\n" +
		"    {\n" +
		"      \"sourceId\": \"" + e + "\",\n" +
		"      \"targetId\": \"" + f + "\",\n" +
		"      \"kind\": \"blocked_by\"\n" +
		"    },\n" +
		"    {\n" +
		"      \"sourceId\": \"" + f + "\",\n" +
		"      \"targetId\": \"" + h + "\",\n" +
		"      \"kind\": \"blocked_by\"\n" +
		"    },\n" +
		"    {\n" +
		"      \"sourceId\": \"" + e + "\",\n" +
		"      \"targetId\": \"" + g + "\",\n" +
		"      \"kind\": \"blocked_by\"\n" +
		"    },\n" +
		"    {\n" +
		"      \"sourceId\": \"" + g + "\",\n" +
		"      \"targetId\": \"" + h + "\",\n" +
		"      \"kind\": \"blocked_by\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"

	defJSON := runCLI(t, "--db", db, "--format", "json", "task", "dep", "tree", e)
	explicitJSON := runCLI(t, "--db", db, "--format", "json", "task", "dep", "tree", e, "--kind", "blocked_by")
	require.Zero(t, defJSON.exitCode, "%s", defJSON.stderr)
	require.Zero(t, explicitJSON.exitCode, "%s", explicitJSON.stderr)
	require.Equal(t, wantJSON, defJSON.stdout)
	require.Equal(t, wantJSON, explicitJSON.stdout)
	require.Equal(t, defJSON.stdout, explicitJSON.stdout)
	require.NotContains(t, defJSON.stdout, "repeated")
}

func TestCLI_TaskDepTreeRejectsUnknownKindBeforeDBOpen(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"bogus", "generated_by", "attributed_to", "all,kinds", "Blocked_By"} {
		path := absentDB(t)
		got := runCLI(t, "--db", path, "task", "dep", "tree",
			"demo--00000000-0000-0000-0000-000000000001", "--kind", kind)
		require.Equal(t, 1, got.exitCode, "kind %q: %s", kind, got.stderr)
		require.Contains(t, got.stderr,
			"valid values are blocked_by, derived_from, supersedes, discovered_from, or all")
		require.Empty(t, got.stdout)
		_, err := os.Stat(path)
		require.True(t, os.IsNotExist(err), "unknown --kind %q must not create the database", kind)
	}
}

func TestCLI_TaskDepTreeHelpTruthfulAndNoDepthFlag(t *testing.T) {
	t.Parallel()

	for _, group := range []string{"dep", "relation"} {
		help := runCLI(t, "task", group, "tree", "--help")
		require.Zero(t, help.exitCode, "%s", help.stderr)
		for _, needle := range []string{"--kind", "blocked_by", "derived_from", "supersedes", "discovered_from", "all"} {
			require.Contains(t, help.stdout, needle, "%s tree --help must mention %q", group, needle)
		}
		require.NotContains(t, help.stdout, "--depth")
	}

	got := runCLI(t, "task", "dep", "tree", "demo--00000000-0000-0000-0000-000000000001", "--depth", "1")
	require.Equal(t, 1, got.exitCode)
	require.Contains(t, got.stderr, "unknown flag")
}

// Execute the emitted bytes, not a hand-maintained imitation of their argv.
func TestCLI_GeneratedTaskRecipes(t *testing.T) {
	t.Parallel()
	var fixture struct {
		Cases []struct {
			Name         string   `yaml:"name"`
			Skill        string   `yaml:"skill"`
			Anchor       string   `yaml:"anchor"`
			Titles       []string `yaml:"titles"`
			BlockedInput bool     `yaml:"blocked_input"`
		} `yaml:"cases"`
	}
	testutil.LoadFixtures(t, testutil.GeneratedTaskRecipes, &fixture)
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			db := newDB(t)
			namespace := "https://example.com/recipe"
			create := runCLI(t, "--db", db, "--format", "json", "--namespace", namespace, "task", "create", "Review input")
			require.Zero(t, create.exitCode, create.stderr)
			var input struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal([]byte(create.stdout), &input))
			data, err := os.ReadFile(filepath.Join("..", "..", "skills", tc.Skill, "SKILL.md"))
			require.NoError(t, err)
			text := string(data)
			start := strings.Index(text, tc.Anchor)
			require.NotEqual(t, -1, start)
			block := regexp.MustCompile("(?ms)^([ \\t]*)```bash\\n(.*?)^[ \\t]*```").FindStringSubmatch(text[start:])
			require.Len(t, block, 3)
			// Markdown list fences strip their indentation when copied as shell text.
			var lines []string
			for _, line := range strings.Split(block[2], "\n") {
				lines = append(lines, strings.TrimPrefix(line, block[1]))
			}
			script := "set -euo pipefail\npasture() { \"$PASTURE_BINARY\" --db \"$RECIPE_DB\" \"$@\"; }\n" + strings.Join(lines, "\n")
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PASTURE_BINARY="+binaryPath, "RECIPE_DB="+db,
				"PASTURE_NAMESPACE="+namespace, "SLICE_1_ID_URI="+input.ID, "REVIEW_ROUND_ID_URI="+input.ID,
				"SLICE_ID_URI="+input.ID, "REVIEW_ID_URI="+input.ID, "IMPL_PLAN_ID_URI="+input.ID,
				"REQUEST_ID_URI="+input.ID, "URD_ID_URI="+input.ID, "RATIFIED_PROPOSAL_ID_URI="+input.ID,
				"SLICE_2_ID_URI="+input.ID, "SLICE_3_ID_URI="+input.ID, "SLICE_4_ID_URI="+input.ID)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			require.NoError(t, cmd.Run(), "%s\n%s", script, output.String())
			listed := runCLI(t, "--db", db, "--format", "json", "task", "list", "--namespace", namespace)
			require.Zero(t, listed.exitCode, listed.stderr)
			var tasks []struct {
				ID     string   `json:"id"`
				Title  string   `json:"title"`
				Labels []string `json:"labels"`
			}
			require.NoError(t, json.Unmarshal([]byte(listed.stdout), &tasks))
			require.Len(t, tasks, len(tc.Titles)+1)
			var titles []string
			for _, task := range tasks {
				require.True(t, strings.HasPrefix(task.ID, namespace+"--"), task.ID)
				if task.ID != input.ID {
					titles = append(titles, task.Title)
					labels := runCLI(t, "--db", db, "--format", "json", "task", "label", "list", task.ID)
					require.Zero(t, labels.exitCode, labels.stderr)
					var labeled struct {
						Labels []string `json:"labels"`
					}
					require.NoError(t, json.Unmarshal([]byte(labels.stdout), &labeled))
					require.NotEmpty(t, labeled.Labels)
				}
			}
			require.ElementsMatch(t, tc.Titles, titles)
			blocked := runCLI(t, "--db", db, "--format", "json", "task", "blocked")
			require.Zero(t, blocked.exitCode, blocked.stderr)
			var blockers []struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal([]byte(blocked.stdout), &blockers))
			if tc.BlockedInput {
				require.Len(t, blockers, 1)
				require.Equal(t, input.ID, blockers[0].ID, "generated graph must leave the parent blocked by its finding group, never the inverse")
			} else {
				require.Empty(t, blockers)
			}
		})
	}
}

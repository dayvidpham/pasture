package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/stretchr/testify/require"
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

	for _, name := range []string{"list", "ready", "blocked", "label", "comments", "agents", "dep"} {
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

	require.NotContains(t, inventory, "| `pasture task events")
	require.NotContains(t, inventory, "| `pasture task contexts")
}

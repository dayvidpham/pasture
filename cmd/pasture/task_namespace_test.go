package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// SetHermeticEnv in TestMain unsets PASTURE_NAMESPACE for the whole test
// binary, so an operator's exported value cannot change what these CLI
// invocations see. Individual tests supply it per subprocess through
// runCLIEnv.

// runCLIEnv executes the compiled binary with extra environment entries, which
// take precedence over the inherited environment. It is the parallel-safe way
// to exercise PASTURE_NAMESPACE: no test mutates its own process environment.
func runCLIEnv(t *testing.T, env []string, args ...string) runOutcome {
	t.Helper()
	// #nosec G204 — binaryPath comes from TestMain's controlled go build.
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("unexpected exec error: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
	}
	return runOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

func decodeCLIIDs(t *testing.T, out runOutcome) []string {
	t.Helper()
	require.Zero(t, out.exitCode, "stderr=%s", out.stderr)
	var rows []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &rows), "body: %s", out.stdout)
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func TestCLI_TaskReadinessNamespaceScope(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "namespace.db")
	create := func(env []string, args ...string) string {
		t.Helper()
		full := append([]string{"--db", db, "--format", "json", "task", "create"}, args...)
		out := runCLIEnv(t, env, full...)
		require.Zero(t, out.exitCode, "create %v: %s", args, out.stderr)
		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(out.stdout), &created))
		return created.ID
	}

	// PASTURE_NAMESPACE supplies the create namespace when --namespace is absent.
	alphaReady := create([]string{"PASTURE_NAMESPACE=alpha"}, "alpha ready")
	require.True(t, strings.HasPrefix(alphaReady, "alpha/"), alphaReady)
	betaReady := create(nil, "beta ready", "--namespace", "beta")
	require.True(t, strings.HasPrefix(betaReady, "beta/"), betaReady)
	alphaBlocked := create(nil, "alpha blocked", "--namespace", "alpha")
	betaBlocker := create(nil, "beta blocker", "--namespace", "beta")

	dep := runCLIEnv(t, nil, "--db", db, "--format", "json", "task", "dep", "add", alphaBlocked, "--blocked-by", betaBlocker)
	require.Zero(t, dep.exitCode, dep.stderr)

	ready := func(env []string, extra ...string) []string {
		t.Helper()
		args := append([]string{"--db", db, "--format", "json", "task", "ready"}, extra...)
		return decodeCLIIDs(t, runCLIEnv(t, env, args...))
	}
	blocked := func(env []string, extra ...string) []string {
		t.Helper()
		args := append([]string{"--db", db, "--format", "json", "task", "blocked"}, extra...)
		return decodeCLIIDs(t, runCLIEnv(t, env, args...))
	}
	list := func(env []string, extra ...string) []string {
		t.Helper()
		args := append([]string{"--db", db, "--format", "json", "task", "list"}, extra...)
		return decodeCLIIDs(t, runCLIEnv(t, env, args...))
	}

	// env default scopes readiness
	require.ElementsMatch(t, []string{alphaReady}, ready([]string{"PASTURE_NAMESPACE=alpha"}))
	// explicit --namespace overrides the environment
	require.ElementsMatch(t, []string{betaReady, betaBlocker}, ready([]string{"PASTURE_NAMESPACE=alpha"}, "--namespace", "beta"))
	// --all-namespaces ignores both sources
	require.ElementsMatch(t, []string{alphaReady, betaReady, betaBlocker}, ready([]string{"PASTURE_NAMESPACE=alpha"}, "--all-namespaces"))

	require.ElementsMatch(t, []string{alphaBlocked}, blocked([]string{"PASTURE_NAMESPACE=alpha"}))
	require.Empty(t, blocked([]string{"PASTURE_NAMESPACE=beta"}, "--namespace", "beta"))
	require.ElementsMatch(t, []string{alphaBlocked}, blocked([]string{"PASTURE_NAMESPACE=beta"}, "--all-namespaces"))

	// task list uses the same effective namespace
	require.ElementsMatch(t, []string{alphaReady, alphaBlocked}, list([]string{"PASTURE_NAMESPACE=alpha"}))
	require.ElementsMatch(t, []string{betaReady, betaBlocker}, list([]string{"PASTURE_NAMESPACE=alpha"}, "--namespace", "beta"))
	require.Len(t, list([]string{"PASTURE_NAMESPACE=alpha"}, "--all-namespaces"), 4)
	// no namespace source leaves list unscoped
	require.Len(t, list(nil), 4)
}

func TestCLI_TaskNamespaceHelpMentionsSources(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"task", "ready", "--help"},
		{"task", "blocked", "--help"},
		{"task", "list", "--help"},
	} {
		out := runCLI(t, args...)
		require.Zero(t, out.exitCode, "%v: %s", args, out.stderr)
		for _, want := range []string{"PASTURE_NAMESPACE", "--all-namespaces"} {
			require.Contains(t, out.stdout, want, "%v help", args)
		}
	}

	root := runCLI(t, "--help")
	require.Zero(t, root.exitCode, root.stderr)
	require.Contains(t, root.stdout, "PASTURE_NAMESPACE")
	require.Contains(t, root.stdout, "Namespace URI")
}

func TestCLI_TaskListNoNamespaceStaysUnscoped(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "unscoped.db")
	for _, namespace := range []string{"alpha", "beta"} {
		out := runCLI(t, "--db", db, "--format", "json", "--namespace", namespace, "task", "create", namespace+" task")
		require.Zero(t, out.exitCode, out.stderr)
	}
	// This invocation explicitly clears the environment value inherited from the
	// test harness, proving an empty source keeps the legacy unscoped result.
	out := runCLIEnv(t, []string{"PASTURE_NAMESPACE="}, "--db", db, "--format", "json", "task", "list")
	require.Len(t, decodeCLIIDs(t, out), 2)
}

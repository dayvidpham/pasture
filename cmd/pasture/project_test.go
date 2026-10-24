package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func seedCLIProjectStore(t *testing.T) (dbPath, epochOne, epochTwo string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	epochOne = createCLIProjectEpoch(t, tracker, "Epoch one")
	epochTwo = createCLIProjectEpoch(t, tracker, "Epoch two")
	return dbPath, epochOne, epochTwo
}

func createCLIProjectEpoch(t *testing.T, tracker protocol.TaskTracker, title string) string {
	t.Helper()
	epoch, err := tracker.Create("file://project-cli", title, "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	return epoch.ID.String()
}

func cliProjectHash(seed string) string { return strings.Repeat(seed, 64/len(seed)) }

func decodeCLIProject(t *testing.T, raw string) sessionapi.ProjectView {
	t.Helper()
	var view sessionapi.ProjectView
	require.NoError(t, json.Unmarshal([]byte(raw), &view), "body: %s", raw)
	return view
}

func decodeCLIProjectPage(t *testing.T, raw string) sessionapi.ProjectPage {
	t.Helper()
	var page sessionapi.ProjectPage
	require.NoError(t, json.Unmarshal([]byte(raw), &page), "body: %s", raw)
	return page
}

func TestCLI_ProjectBindListShowUnbind(t *testing.T) {
	dbPath, epochOne, epochTwo := seedCLIProjectStore(t)
	hash := cliProjectHash("a")

	out := runCLI(t, "--db", dbPath, "--format", "json", "project", "bind", hash, "--epoch", epochOne)
	require.Zero(t, out.exitCode, out.stderr)
	view := decodeCLIProject(t, out.stdout)
	require.Equal(t, hash, view.ProjectHash)
	require.NotNil(t, view.Binding)
	require.Equal(t, "worker", view.Binding.Role)
	require.Equal(t, sessionapi.ProjectBindingSourceProject, view.Binding.Source)
	require.Equal(t, epochOne, view.Epoch.ID)

	out = runCLI(t, "--db", dbPath, "--format", "json", "project", "list")
	require.Zero(t, out.exitCode, out.stderr)
	page := decodeCLIProjectPage(t, out.stdout)
	require.Len(t, page.Items, 1)
	require.Equal(t, hash, page.Items[0].ProjectHash)

	out = runCLI(t, "--db", dbPath, "project", "show", hash)
	require.Zero(t, out.exitCode, out.stderr)
	require.Contains(t, out.stdout, hash)
	require.Contains(t, out.stdout, "Epoch one")
	require.Contains(t, out.stdout, "source=project")
	require.Contains(t, out.stdout, "Intervals:  1")

	out = runCLI(t, "--db", dbPath, "--format", "json", "project", "bind", hash, "--epoch", epochTwo, "--role", "reviewer")
	require.Zero(t, out.exitCode, out.stderr)
	view = decodeCLIProject(t, out.stdout)
	require.Equal(t, epochTwo, view.Epoch.ID)
	require.Len(t, view.Intervals, 2)
	require.Equal(t, "rebind", view.Intervals[0].EndReason)
	require.NotNil(t, view.Intervals[0].UnboundAt)

	out = runCLI(t, "--db", dbPath, "--format", "json", "project", "unbind", hash, "--expected-binding", view.Binding.BindingID)
	require.Zero(t, out.exitCode, out.stderr)
	view = decodeCLIProject(t, out.stdout)
	require.Nil(t, view.Binding)
	require.Len(t, view.Intervals, 2)
	require.Equal(t, "unbind", view.Intervals[1].EndReason)

	out = runCLI(t, "--db", dbPath, "project", "list")
	require.Zero(t, out.exitCode, out.stderr)
	require.Contains(t, out.stdout, "No projects have an active epoch mapping")
}

func TestCLI_ProjectErrors(t *testing.T) {
	dbPath, epochOne, _ := seedCLIProjectStore(t)
	hash := cliProjectHash("b")

	out := runCLI(t, "--db", dbPath, "project", "show", "not-a-hash")
	require.Equal(t, 1, out.exitCode, "a malformed project hash is a validation error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "project hash")

	out = runCLI(t, "--db", dbPath, "project", "bind", hash, "--epoch", "file://missing")
	require.Equal(t, 3, out.exitCode, "an unknown epoch is a workflow error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "epoch does not exist")

	out = runCLI(t, "--db", dbPath, "project", "bind", hash)
	require.Equal(t, 1, out.exitCode, "a missing epoch is a validation error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "epochId")

	out = runCLI(t, "--db", dbPath, "project", "bind", hash, "--epoch", epochOne, "--role", "implementer")
	require.Equal(t, 1, out.exitCode, "an unknown role is a validation error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "role must be one of")

	out = runCLI(t, "--db", dbPath, "project", "unbind", hash)
	require.Equal(t, 3, out.exitCode, "unbinding without an active mapping is a workflow error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "no active mapping")

	out = runCLI(t, "--db", dbPath, "project", "bind", hash, "--epoch", epochOne)
	require.Zero(t, out.exitCode, out.stderr)
	out = runCLI(t, "--db", dbPath, "project", "bind", hash, "--epoch", epochOne, "--expected-binding", "stale")
	require.Equal(t, 3, out.exitCode, "a stale expected binding is a conflict\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "changed since")

	unmapped := cliProjectHash("c")
	out = runCLI(t, "--db", dbPath, "project", "show", unmapped)
	require.Equal(t, 3, out.exitCode, "a valid but unmapped project is a workflow error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "no mapping history")

	missing := filepath.Join(t.TempDir(), "missing.db")
	out = runCLI(t, "--db", missing, "project", "list")
	require.Equal(t, 2, out.exitCode, "a missing store is a connection error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "project commands must not create a store")
}

func TestCLI_ProjectDatabasePathErrors(t *testing.T) {
	dir := t.TempDir()
	out := runCLI(t, "--db", dir, "project", "list")
	require.Equal(t, 2, out.exitCode, "a directory --db is a connection error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "is a directory")

	plain := filepath.Join(t.TempDir(), "plain-file")
	require.NoError(t, os.WriteFile(plain, []byte("not a database"), 0o600))
	out = runCLI(t, "--db", filepath.Join(plain, "nested.db"), "project", "list")
	require.Equal(t, 2, out.exitCode, "an unstattable --db path is a connection error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "Couldn't inspect")

	garbage := filepath.Join(t.TempDir(), "garbage.db")
	require.NoError(t, os.WriteFile(garbage, []byte("this is not sqlite at all\n"), 0o600))
	out = runCLI(t, "--db", garbage, "project", "list")
	require.Equal(t, 2, out.exitCode, "a non-SQLite --db file is a connection error\nstdout=%s\nstderr=%s", out.stdout, out.stderr)
	require.Contains(t, out.stderr, "not a SQLite database")
}

func TestCLI_ProjectHelpAndInventory(t *testing.T) {
	out := runCLI(t, "project", "--help")
	require.Zero(t, out.exitCode, out.stderr)
	require.Equal(t, []string{"bind", "list", "show", "unbind"}, commandNames(out.stdout))
	require.Contains(t, out.stdout, "Peasant")
	require.Contains(t, out.stdout, "never write")
}

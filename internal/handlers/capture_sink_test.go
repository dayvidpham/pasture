package handlers_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
)

// The sink's notice and refusals are operator text: sentences a person acts
// on. The load-bearing phrases are pinned here, and each pin is proved by
// mutation in the slice report: alter the phrase in capture_sink.go and the
// matching test turns RED.
const (
	pinNotice           = "capture mode is recording this session to"
	pinNotCaptured      = "so nothing is captured"
	pinRelativeReason   = "would land somewhere you did not choose"
	pinInsideRepoReason = "can reach a commit before it is cleared"
	pinMissingReason    = "a directory pasture creates is one you did not choose"
	pinHostUnaffected   = "the host is not affected"
	pinPayloadDropped   = "so this payload was not captured"
)

// repositoryWithCaptureDir builds a fake repository (a directory holding a
// .git entry) with a capture directory inside it.
func repositoryWithCaptureDir(t *testing.T, gitIsFile bool) (repo, inside string) {
	t.Helper()
	repo = t.TempDir()
	if gitIsFile {
		require.NoError(t, os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere\n"), 0o600))
	} else {
		require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o700))
	}
	inside = filepath.Join(repo, "captures")
	require.NoError(t, os.Mkdir(inside, 0o700))
	return repo, inside
}

func TestCaptureSinkRefusesARelativeDirectory(t *testing.T) {
	t.Parallel()
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink("captures", "", &warnings)
	require.Error(t, err)
	require.Nil(t, sink)
	assert.Contains(t, warnings.String(), `PASTURE_CAPTURE_DIR is "captures", which is not an absolute path`)
	assert.Contains(t, warnings.String(), pinNotCaptured)
	assert.Contains(t, warnings.String(), pinRelativeReason)
	assert.Contains(t, warnings.String(), pinHostUnaffected)
	assert.Equal(t, warnings.String(), err.Error()+"\n", "the warning the operator reads is the error the caller decides on")
}

func TestCaptureSinkRefusesADirectoryInsideTheRepository(t *testing.T) {
	t.Parallel()
	for _, gitIsFile := range []bool{false, true} {
		gitIsFile := gitIsFile
		t.Run(fmt.Sprintf("gitIsFile=%v", gitIsFile), func(t *testing.T) {
			t.Parallel()
			repo, inside := repositoryWithCaptureDir(t, gitIsFile)
			var warnings bytes.Buffer
			sink, err := handlers.NewDirectoryCaptureSink(inside, repo, &warnings)
			require.Error(t, err)
			require.Nil(t, sink)
			assert.Contains(t, warnings.String(), fmt.Sprintf("is inside the repository at %q", repo))
			assert.Contains(t, warnings.String(), pinNotCaptured)
			assert.Contains(t, warnings.String(), pinInsideRepoReason)

			// The repository root itself is inside the repository too.
			warnings.Reset()
			sink, err = handlers.NewDirectoryCaptureSink(repo, repo, &warnings)
			require.Error(t, err)
			require.Nil(t, sink)
			assert.Contains(t, warnings.String(), pinInsideRepoReason)

			// A sibling of the repository is outside it; a name that merely
			// shares the root's prefix is not inside.
			outside := t.TempDir()
			warnings.Reset()
			sink, err = handlers.NewDirectoryCaptureSink(outside, repo, &warnings)
			require.NoError(t, err)
			require.NotNil(t, sink)
			assert.Empty(t, warnings.String(), "an accepted directory produces no warning at construction")
		})
	}
}

func TestCaptureSinkRefusesASymlinkThatResolvesInsideTheRepository(t *testing.T) {
	t.Parallel()
	repo, inside := repositoryWithCaptureDir(t, false)
	link := filepath.Join(t.TempDir(), "link-to-inside")
	require.NoError(t, os.Symlink(inside, link))
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(link, repo, &warnings)
	require.Error(t, err)
	require.Nil(t, sink)
	assert.Contains(t, warnings.String(), "resolves through a symbolic link to a path inside the repository")
	assert.Contains(t, warnings.String(), pinInsideRepoReason)
}

func TestCaptureSinkDoesNotCreateAMissingDirectory(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "never-created")
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(missing, "", &warnings)
	require.Error(t, err)
	require.Nil(t, sink)
	_, statErr := os.Stat(missing)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the sink must not create the directory it refused")
	assert.Contains(t, warnings.String(), fmt.Sprintf("PASTURE_CAPTURE_DIR is %q, which does not exist", missing))
	assert.Contains(t, warnings.String(), pinNotCaptured)
	assert.Contains(t, warnings.String(), pinMissingReason)
	assert.Contains(t, warnings.String(), pinHostUnaffected)
}

func TestCaptureSinkRefusesAFileWhereADirectoryIsExpected(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(file, "", &warnings)
	require.Error(t, err)
	require.Nil(t, sink)
	assert.Contains(t, warnings.String(), "is not a directory")
	assert.Contains(t, warnings.String(), pinMissingReason)
}

func TestCaptureSinkRefusesAMissingWarningsStream(t *testing.T) {
	t.Parallel()
	sink, err := handlers.NewDirectoryCaptureSink(t.TempDir(), "", nil)
	require.Error(t, err)
	require.Nil(t, sink)
	assert.Contains(t, err.Error(), "no warnings stream was supplied")
}

func TestEnclosingRepositoryRootFindsTheNearestGitEntry(t *testing.T) {
	t.Parallel()
	repo, inside := repositoryWithCaptureDir(t, false)
	deeper := filepath.Join(inside, "a", "b")
	require.NoError(t, os.MkdirAll(deeper, 0o700))
	root, found := handlers.EnclosingRepositoryRoot(deeper)
	require.True(t, found)
	assert.Equal(t, repo, root)

	worktree, insideWorktree := repositoryWithCaptureDir(t, true)
	root, found = handlers.EnclosingRepositoryRoot(insideWorktree)
	require.True(t, found, "a linked worktree marks its root with a .git FILE, and that counts")
	assert.Equal(t, worktree, root)
}

// TestEnclosingRepositoryRootReportsNoneOutsideAnyRepository checks the
// negative answer. A plain temporary directory holds no .git entry of its own,
// so any root found for it is a STRICT ancestor: never the directory itself
// and never below it. Whether an ancestor exists depends on the machine, so
// the no-repository claim is checked only where the machine can arrange it,
// and the skip names the repository that prevents it.
func TestEnclosingRepositoryRootReportsNoneOutsideAnyRepository(t *testing.T) {
	t.Parallel()
	plain := t.TempDir()
	root, found := handlers.EnclosingRepositoryRoot(plain)
	if found {
		assert.NotEqual(t, plain, root)
		rel, err := filepath.Rel(root, plain)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(rel, ".."), "the root %q found for %q is not an ancestor of it", root, plain)
		t.Skipf("the no-repository case cannot be arranged here: %q lies under the repository at %q", plain, root)
	}
	assert.False(t, found)
}

func TestCaptureStemNamesHarnessEventAndVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		harness ir.HarnessID
		event   string
		version string
		want    string
	}{
		{ir.HarnessClaudeCode, "PreToolUse", "2.1.251", "claude-code_pre_tool_use_2_1_251"},
		{ir.HarnessClaudeCode, "SessionStart", "2.1.251", "claude-code_session_start_2_1_251"},
		{ir.HarnessCodex, "PostToolUse", "0.149.0", "codex_post_tool_use_0_149_0"},
		{ir.HarnessOpenCode, "tool.execute.before", "1.18.19", "opencode_tool_execute_before_1_18_19"},
		{ir.HarnessOpenCode, "lsp.client.diagnostics", "1.18.19", "opencode_lsp_client_diagnostics_1_18_19"},
	}
	for _, tc := range cases {
		got, ok := handlers.CaptureStem(tc.harness, tc.event, tc.version)
		require.True(t, ok, "%s/%s@%s", tc.harness, tc.event, tc.version)
		assert.Equal(t, tc.want, got)
	}
	for _, unsafe := range []struct{ event, version string }{{"../PreToolUse", "2.1.251"}, {"PreToolUse", "2.1.251/x"}, {"Pre Tool", "2.1.251"}, {"", "2.1.251"}, {"PreToolUse", ""}} {
		_, ok := handlers.CaptureStem(ir.HarnessClaudeCode, unsafe.event, unsafe.version)
		assert.False(t, ok, "event %q version %q must be refused as a file-name coordinate", unsafe.event, unsafe.version)
	}
	_, ok := handlers.CaptureStem("", "PreToolUse", "2.1.251")
	assert.False(t, ok, "an empty harness must be refused")
}

func TestCaptureSinkNumbersRecordsAndNeverOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(dir, "", &warnings)
	require.NoError(t, err)

	first, err := sink.Record(ir.HarnessClaudeCode, "PreToolUse", "2.1.251", []byte(`{"n":1}`))
	require.NoError(t, err)
	second, err := sink.Record(ir.HarnessClaudeCode, "PreToolUse", "2.1.251", []byte(`{"n":2}`))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "claude-code_pre_tool_use_2_1_251.1.json"), first)
	assert.Equal(t, filepath.Join(dir, "claude-code_pre_tool_use_2_1_251.2.json"), second)
	firstBytes, err := os.ReadFile(first)
	require.NoError(t, err)
	secondBytes, err := os.ReadFile(second)
	require.NoError(t, err)
	assert.Equal(t, `{"n":1}`, string(firstBytes), "the first capture keeps its exact bytes after the second is written")
	assert.Equal(t, `{"n":2}`, string(secondBytes))

	// A foreign file already holding a higher number moves the sequence past
	// it; nothing existing is ever rewritten.
	foreign := filepath.Join(dir, "claude-code_pre_tool_use_2_1_251.7.json")
	require.NoError(t, os.WriteFile(foreign, []byte("foreign"), 0o600))
	third, err := sink.Record(ir.HarnessClaudeCode, "PreToolUse", "2.1.251", []byte(`{"n":3}`))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "claude-code_pre_tool_use_2_1_251.8.json"), third)
	foreignBytes, err := os.ReadFile(foreign)
	require.NoError(t, err)
	assert.Equal(t, "foreign", string(foreignBytes))

	// A different event has its own sequence.
	other, err := sink.Record(ir.HarnessClaudeCode, "SessionStart", "2.1.251", []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "claude-code_session_start_2_1_251.1.json"), other)
}

func TestCaptureSinkPrintsTheNoticeExactlyOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(dir, "", &warnings)
	require.NoError(t, err)
	assert.Empty(t, warnings.String(), "nothing is recorded yet, so nothing may claim to be recording")
	for index := 0; index < 25; index++ {
		_, err := sink.Record(ir.HarnessOpenCode, "tool.execute.before", "1.18.19", []byte(fmt.Sprintf(`{"i":%d}`, index)))
		require.NoError(t, err)
	}
	lines := strings.Split(strings.TrimRight(warnings.String(), "\n"), "\n")
	require.Len(t, lines, 1, "exactly one line may be printed for twenty-five records, got %q", warnings.String())
	assert.Equal(t, "pasture: "+pinNotice+" "+dir, lines[0])
}

func TestCaptureSinkWarnsAndContinuesOnAnUnwritableDirectory(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so an unwritable directory cannot be arranged")
	}
	dir := t.TempDir()
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(dir, "", &warnings)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path, err := sink.Record(ir.HarnessCodex, "PreToolUse", "0.149.0", []byte(`{}`))
	require.Error(t, err)
	assert.Empty(t, path)
	assert.Contains(t, warnings.String(), "could not be created")
	assert.Contains(t, warnings.String(), pinPayloadDropped)
	assert.Contains(t, warnings.String(), pinHostUnaffected)
	assert.NotContains(t, warnings.String(), pinNotice, "a sink that recorded nothing must not claim to be recording")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCaptureSinkRefusesPathUnsafeCoordinatesAtRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warnings bytes.Buffer
	sink, err := handlers.NewDirectoryCaptureSink(dir, "", &warnings)
	require.NoError(t, err)
	path, err := sink.Record(ir.HarnessClaudeCode, "../escape", "2.1.251", []byte(`{}`))
	require.Error(t, err)
	assert.Empty(t, path)
	assert.Contains(t, warnings.String(), "carries a character that is not safe in a file name")
	assert.Contains(t, warnings.String(), pinPayloadDropped)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	_, err = os.Stat(filepath.Join(filepath.Dir(dir), "escape.1.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

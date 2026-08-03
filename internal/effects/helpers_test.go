package effects_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/require"
)

// oidA/oidB/treeA are fixed valid object ids reused across guarded-push tests.
const (
	oidA  = "1111111111111111111111111111111111111111"
	oidB  = "2222222222222222222222222222222222222222"
	treeA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func mustCommit(t testing.TB, value string) effects.CommitOID {
	t.Helper()
	commit, err := effects.NewCommitOID(value)
	require.NoError(t, err)
	return commit
}

func mustTree(t testing.TB, value string) effects.TreeDigest {
	t.Helper()
	tree, err := effects.NewTreeDigest(value)
	require.NoError(t, err)
	return tree
}

func mustRepository(t testing.TB, value string) effects.RepositoryID {
	t.Helper()
	repo, err := effects.NewRepositoryID(value)
	require.NoError(t, err)
	return repo
}

func mustRemoteRef(t testing.TB, value string) effects.RemoteRef {
	t.Helper()
	ref, err := effects.NewRemoteRef(value)
	require.NoError(t, err)
	return ref
}

func mustOwnedPath(t testing.TB, value string) effects.OwnedPath {
	t.Helper()
	path, err := effects.NewOwnedPath(value)
	require.NoError(t, err)
	return path
}

func mustGuardedInput(t testing.TB, expected effects.ExpectedOldOID) effects.GuardedPushInput {
	t.Helper()
	input, err := effects.NewGuardedPushInput(
		mustRepository(t, "/repo"),
		mustCommit(t, oidA),
		mustTree(t, treeA),
		mustRemoteRef(t, "refs/heads/main"),
		expected,
	)
	require.NoError(t, err)
	return input
}

// fakePusher is a deterministic RepositoryPusher used to drive every guarded-push
// scenario without a real repository or any sleeping. Each primitive's result is
// configured directly, and call counts are recorded so tests can prove ordering
// and short-circuiting. ReadRemote is called twice by the production algorithm —
// once before the push (returns remoteBefore, zero value = absent) and once
// after (returns remoteAfter) — so the two states can be configured
// independently to model the already-at-target-before-the-call scenario.
type fakePusher struct {
	localErr     error
	pushErr      error
	remoteBefore effects.RemoteState
	remoteAfter  effects.RemoteState
	readErr      error

	verifyCalls int
	pushCalls   int
	readCalls   int
}

func (f *fakePusher) VerifyLocalObject(effects.RepositoryID, effects.CommitOID, effects.TreeDigest) error {
	f.verifyCalls++
	return f.localErr
}

func (f *fakePusher) PushExact(effects.RepositoryID, effects.CommitOID, effects.RemoteRef, effects.ExpectedOldOID) error {
	f.pushCalls++
	return f.pushErr
}

func (f *fakePusher) ReadRemote(effects.RepositoryID, effects.RemoteRef) (effects.RemoteState, error) {
	f.readCalls++
	if f.readErr != nil {
		return effects.RemoteState{}, f.readErr
	}
	if f.readCalls == 1 {
		return f.remoteBefore, nil
	}
	return f.remoteAfter, nil
}

// memNode is one in-memory filesystem node.
type memNode struct {
	typ     effects.NodeType
	mode    fs.FileMode
	content []byte
}

// memFS is an in-memory PublicationFS with per-path failure injection and an
// ordered record of every mutating call, so tests can prove payload-first /
// sidecar-last ordering and that a failure leaves the sidecar unadvanced.
type memFS struct {
	nodes        map[string]memNode
	failWriteOn  map[string]error
	failRemoveOn map[string]error
	mutations    []string
}

func newMemFS() *memFS {
	return &memFS{
		nodes:        map[string]memNode{},
		failWriteOn:  map[string]error{},
		failRemoveOn: map[string]error{},
	}
}

func (m *memFS) seedFile(path string, mode fs.FileMode, content []byte) {
	m.nodes[path] = memNode{typ: effects.NodeFile, mode: mode, content: append([]byte(nil), content...)}
}

func (m *memFS) seedNode(path string, typ effects.NodeType, mode fs.FileMode) {
	m.nodes[path] = memNode{typ: typ, mode: mode}
}

func (m *memFS) Stat(path string) (effects.PublishedNode, error) {
	node, ok := m.nodes[path]
	if !ok {
		return effects.PublishedNode{Type: effects.NodeAbsent}, nil
	}
	return effects.PublishedNode{Type: node.typ, Mode: node.mode}, nil
}

func (m *memFS) ReadFile(path string) ([]byte, error) {
	node, ok := m.nodes[path]
	if !ok || node.typ != effects.NodeFile {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), node.content...), nil
}

func (m *memFS) WriteFile(path string, content []byte, mode fs.FileMode) error {
	if err := m.failWriteOn[path]; err != nil {
		return err
	}
	m.nodes[path] = memNode{typ: effects.NodeFile, mode: mode, content: append([]byte(nil), content...)}
	m.mutations = append(m.mutations, "write:"+path)
	return nil
}

func (m *memFS) MkdirAll(path string, mode fs.FileMode) error {
	if _, ok := m.nodes[path]; !ok {
		m.nodes[path] = memNode{typ: effects.NodeDir, mode: mode}
	}
	return nil
}

func (m *memFS) Remove(path string) error {
	if err := m.failRemoveOn[path]; err != nil {
		return err
	}
	delete(m.nodes, path)
	m.mutations = append(m.mutations, "remove:"+path)
	return nil
}

// mutationOf returns the ordered index of the first mutation matching substr,
// or -1 if none, so tests can assert relative write ordering.
func (m *memFS) mutationOf(substr string) int {
	for i, mutation := range m.mutations {
		if strings.Contains(mutation, substr) {
			return i
		}
	}
	return -1
}

// mustRenderedTree compiles a single-file RenderedTree at outputPath. A
// RenderedTree from ir.Compile always holds exactly one file, so publication
// multi-path scenarios are exercised by publishing successive single-file trees
// at different paths (see the stale-leaf tests).
func mustRenderedTree(t testing.TB, outputPath, content string) ir.RenderedTree {
	t.Helper()
	location, err := ir.NewLocation("worker-implement", "skills/worker/SKILL.md", "body", ir.SourceRange{Start: 0, Stop: len(content)})
	require.NoError(t, err)
	markdown, err := ir.Markdown([]byte(content), location)
	require.NoError(t, err)
	document, err := ir.NewDocument(markdown)
	require.NoError(t, err)
	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.210")
	require.NoError(t, err)
	target, err := ir.NewTarget(ir.HarnessClaudeCode, contract, outputPath, nil)
	require.NoError(t, err)
	tree, err := ir.Compile(document, target)
	require.NoError(t, err)
	return tree
}

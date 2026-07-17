package effects_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const payloadRoot = "/out/payload"

func sidecarPath() string { return "/out/.payload.pasture-manifest.json" }

// TestPublishCreatesPayloadThenSidecar proves a first publish creates the
// payload file, verifies it, and replaces the sidecar last.
func TestPublishCreatesPayloadThenSidecar(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()

	report, err := effects.Publish(tree, payloadRoot, filesystem)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathCreated, report.Results[0].Outcome)

	payloadWrite := filesystem.mutationOf("write:/out/payload/a/SKILL.md")
	sidecarWrite := filesystem.mutationOf("write:" + sidecarPath())
	require.GreaterOrEqual(t, payloadWrite, 0)
	require.GreaterOrEqual(t, sidecarWrite, 0)
	assert.Less(t, payloadWrite, sidecarWrite, "payload is written before the sidecar")
}

// TestPublishNeverRunsOnEmptyTree proves an empty tree (a compile-error stand-in)
// never publishes.
func TestPublishNeverRunsOnEmptyTree(t *testing.T) {
	t.Parallel()

	filesystem := newMemFS()
	_, err := effects.Publish(ir.RenderedTree{}, payloadRoot, filesystem)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assert.Empty(t, filesystem.mutations, "no mutation on a compile-error path")
}

// TestPublishIsIdempotentOnRetry proves publishing the same tree twice converges:
// the second publish verifies rather than rewrites, and no rollback is implied.
func TestPublishIsIdempotentOnRetry(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()

	_, err := effects.Publish(tree, payloadRoot, filesystem)
	require.NoError(t, err)

	report, err := effects.Publish(tree, payloadRoot, filesystem)
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathVerified, report.Results[0].Outcome, "already-desired path resumes as verified")
}

// TestPublishRemovesStaleLeaves proves publishing a new single-file tree removes
// the previously owned leaf recorded in the manifest.
func TestPublishRemovesStaleLeaves(t *testing.T) {
	t.Parallel()

	filesystem := newMemFS()
	first := mustRenderedTree(t, "old/SKILL.md", "# Old\n")
	_, err := effects.Publish(first, payloadRoot, filesystem)
	require.NoError(t, err)
	_, present := filesystem.nodes["/out/payload/old/SKILL.md"]
	require.True(t, present)

	second := mustRenderedTree(t, "new/SKILL.md", "# New\n")
	report, err := effects.Publish(second, payloadRoot, filesystem)
	require.NoError(t, err)

	_, stillThere := filesystem.nodes["/out/payload/old/SKILL.md"]
	assert.False(t, stillThere, "the stale leaf is removed")
	_, created := filesystem.nodes["/out/payload/new/SKILL.md"]
	assert.True(t, created)

	var removed bool
	for _, result := range report.Results {
		if result.Path == "/out/payload/old/SKILL.md" && result.Outcome == effects.PathRemoved {
			removed = true
		}
	}
	assert.True(t, removed, "stale removal is reported per-path")
}

// TestPublishRejectsSidecarUnsafeType proves an unsafe sidecar node fails before
// any mutation.
func TestPublishRejectsSidecarUnsafeType(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()
	filesystem.seedNode(sidecarPath(), effects.NodeDir, publishedDirModeForTest)

	_, err := effects.Publish(tree, payloadRoot, filesystem)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
	assert.Empty(t, filesystem.mutations, "no mutation before the unsafe sidecar is resolved")
}

// TestPublishRejectsUnrelatedDrift proves a managed path holding content that
// matches neither the desired tree nor the last manifest fails before mutation.
func TestPublishRejectsUnrelatedDrift(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()
	filesystem.seedFile("/out/payload/a/SKILL.md", 0o644, []byte("foreign edit"))

	_, err := effects.Publish(tree, payloadRoot, filesystem)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrelated content")
	assert.Empty(t, filesystem.mutations, "unrelated drift fails before any mutation")
}

// TestPublishRetainsManifestOnPayloadFailure proves a mid-payload write failure
// leaves the sidecar unadvanced (last confirmed manifest retained), reports the
// failed path, and claims no rollback.
func TestPublishRetainsManifestOnPayloadFailure(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()
	filesystem.failWriteOn["/out/payload/a/SKILL.md"] = errors.New("disk full")

	report, err := effects.Publish(tree, payloadRoot, filesystem)
	require.Error(t, err)
	assert.False(t, report.ManifestReplaced, "the last confirmed manifest is retained")
	require.NotEmpty(t, report.Results)
	assert.Equal(t, effects.PathFailed, report.Results[len(report.Results)-1].Outcome)
	_, sidecarWritten := filesystem.nodes[sidecarPath()]
	assert.False(t, sidecarWritten, "the sidecar is not advanced on failure")
}

// TestPublishRejectsBadPayloadRoot covers empty/degenerate roots.
func TestPublishRejectsBadPayloadRoot(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	for _, root := range []string{"", " /x", "/"} {
		_, err := effects.Publish(tree, root, newMemFS())
		require.Error(t, err, root)
	}
}

// TestPublishFailsStaleLeafRemoval proves a stale-leaf removal failure
// fails closed: the sidecar manifest is not advanced, the failing path is
// reported PathFailed, the already-succeeded payload results from the same
// call are still reported (partial per-path results, no rollback), and a
// retry after the injected failure clears converges.
func TestPublishFailsStaleLeafRemoval(t *testing.T) {
	t.Parallel()

	filesystem := newMemFS()
	first := mustRenderedTree(t, "old/SKILL.md", "# Old\n")
	_, err := effects.Publish(first, payloadRoot, filesystem)
	require.NoError(t, err)

	stalePath := "/out/payload/old/SKILL.md"
	filesystem.failRemoveOn[stalePath] = errors.New("permission denied")

	second := mustRenderedTree(t, "new/SKILL.md", "# New\n")
	report, err := effects.Publish(second, payloadRoot, filesystem)
	require.Error(t, err)
	assert.False(t, report.ManifestReplaced, "the last confirmed manifest is retained on a stale-removal failure")

	var sawCreated, sawFailedRemoval bool
	for _, result := range report.Results {
		if result.Path == "/out/payload/new/SKILL.md" && result.Outcome == effects.PathCreated {
			sawCreated = true
			assert.NoError(t, result.Err, "the already-succeeded payload write is still reported with no error")
		}
		if result.Path == stalePath && result.Outcome == effects.PathFailed {
			sawFailedRemoval = true
			assert.Error(t, result.Err)
		}
	}
	assert.True(t, sawCreated, "the already-succeeded create is reported even though a later step failed")
	assert.True(t, sawFailedRemoval, "the stale-leaf removal failure is reported per-path")

	// The stale leaf was never actually removed (Remove errored before deleting).
	_, stillThere := filesystem.nodes[stalePath]
	assert.True(t, stillThere, "no rollback is claimed and the failed removal leaves the node untouched")

	// Retry after clearing the injected failure converges.
	delete(filesystem.failRemoveOn, stalePath)
	report, err = effects.Publish(second, payloadRoot, filesystem)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)
	_, stillThereAfterRetry := filesystem.nodes[stalePath]
	assert.False(t, stillThereAfterRetry, "the retry completes the stale-leaf removal")
}

// TestPublishSidecarWriteFailureRetainsPayload proves a sidecar (manifest)
// write failure — a mid-sidecar crash after the payload has already verified
// — fails closed: the payload is retained on disk (not rolled back), the
// manifest is not advanced, and a retry after the injected failure clears
// converges to ManifestReplaced==true.
func TestPublishSidecarWriteFailureRetainsPayload(t *testing.T) {
	t.Parallel()

	tree := mustRenderedTree(t, "a/SKILL.md", "# Skill\n")
	filesystem := newMemFS()
	filesystem.failWriteOn[sidecarPath()] = errors.New("disk full")

	report, err := effects.Publish(tree, payloadRoot, filesystem)
	require.Error(t, err)
	assert.False(t, report.ManifestReplaced, "the manifest is not advanced when the sidecar write fails")

	require.NotEmpty(t, report.Results)
	payloadResult := report.Results[0]
	assert.Equal(t, "/out/payload/a/SKILL.md", payloadResult.Path)
	assert.Equal(t, effects.PathCreated, payloadResult.Outcome)
	assert.NoError(t, payloadResult.Err, "the payload write itself succeeded before the sidecar failure")

	payloadContent, readErr := filesystem.ReadFile("/out/payload/a/SKILL.md")
	require.NoError(t, readErr)
	assert.Equal(t, "# Skill\n", string(payloadContent), "the payload is retained on disk despite the sidecar failure")

	_, sidecarWritten := filesystem.nodes[sidecarPath()]
	assert.False(t, sidecarWritten, "the sidecar is not advanced")

	// Retry after clearing the injected failure converges: the payload is
	// already at desired (verified, not rewritten) and the sidecar is written.
	delete(filesystem.failWriteOn, sidecarPath())
	report, err = effects.Publish(tree, payloadRoot, filesystem)
	require.NoError(t, err)
	assert.True(t, report.ManifestReplaced)
	require.Len(t, report.Results, 1)
	assert.Equal(t, effects.PathVerified, report.Results[0].Outcome, "the retry finds the payload already at desired state")
}

// publishedDirModeForTest mirrors the publisher's directory mode for seeding.
const publishedDirModeForTest fs.FileMode = 0o755

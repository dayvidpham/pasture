package scan_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOwnerManifestValidatesEveryEntry(t *testing.T) {
	t.Parallel()

	t.Run("valid active and dead entries", func(t *testing.T) {
		t.Parallel()
		manifest, err := scan.NewOwnerManifest([]scan.OwnerEntry{
			{Path: "skills/a.md", Disposition: scan.OwnerActive},
			{Path: "skills/b.md", Disposition: scan.OwnerDead, Reason: "historical, superseded by skills/a.md"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, manifest.Len())
		assert.Equal(t, []string{"skills/a.md", "skills/b.md"}, manifest.Paths())

		entry, ok := manifest.Lookup("skills/a.md")
		require.True(t, ok)
		assert.Equal(t, scan.OwnerActive, entry.Disposition)

		_, ok = manifest.Lookup("skills/missing.md")
		assert.False(t, ok)
	})

	t.Run("empty path is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewOwnerManifest([]scan.OwnerEntry{{Path: "  ", Disposition: scan.OwnerActive}})
		assert.Error(t, err)
	})

	t.Run("unknown disposition is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewOwnerManifest([]scan.OwnerEntry{{Path: "skills/a.md", Disposition: "retired"}})
		assert.Error(t, err)
	})

	t.Run("dead owner without a reason is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewOwnerManifest([]scan.OwnerEntry{{Path: "skills/a.md", Disposition: scan.OwnerDead}})
		assert.ErrorContains(t, err, "no reason")
	})

	t.Run("duplicate path is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewOwnerManifest([]scan.OwnerEntry{
			{Path: "skills/a.md", Disposition: scan.OwnerActive},
			{Path: "skills/a.md", Disposition: scan.OwnerActive},
		})
		assert.ErrorContains(t, err, "duplicates path")
	})
}

func TestDecodeOwnerManifestStrictJSON(t *testing.T) {
	t.Parallel()

	t.Run("valid document decodes", func(t *testing.T) {
		t.Parallel()
		manifest, err := scan.DecodeOwnerManifest([]byte(`{
			"owners": [
				{"path": "skills/a.md", "disposition": "active"},
				{"path": "skills/b.md", "disposition": "dead", "reason": "superseded"}
			]
		}`))
		require.NoError(t, err)
		assert.Equal(t, 2, manifest.Len())
	})

	t.Run("missing owners field is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeOwnerManifest([]byte(`{}`))
		assert.Error(t, err)
	})

	t.Run("duplicate JSON member is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeOwnerManifest([]byte(`{
			"owners": [{"path": "skills/a.md", "path": "skills/a.md", "disposition": "active"}]
		}`))
		assert.Error(t, err)
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeOwnerManifest([]byte(`{
			"owners": [{"path": "skills/a.md", "disposition": "active", "unexpected": true}]
		}`))
		assert.Error(t, err)
	})

	t.Run("trailing content is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeOwnerManifest([]byte(`{"owners": []}{}`))
		assert.Error(t, err)
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeOwnerManifest([]byte(`not json`))
		assert.Error(t, err)
	})
}

func TestReconcileOwners(t *testing.T) {
	t.Parallel()

	manifest, err := scan.NewOwnerManifest([]scan.OwnerEntry{
		{Path: "skills/a.md", Disposition: scan.OwnerActive},
		{Path: "skills/b.md", Disposition: scan.OwnerActive},
	})
	require.NoError(t, err)

	t.Run("exact match is clean", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, scan.ReconcileOwners([]string{"skills/a.md", "skills/b.md"}, manifest))
	})

	t.Run("unlisted active file fails", func(t *testing.T) {
		t.Parallel()
		err := scan.ReconcileOwners([]string{"skills/a.md", "skills/b.md", "skills/c.md"}, manifest)
		require.Error(t, err)
		assert.ErrorContains(t, err, "skills/c.md")
		assert.ErrorContains(t, err, "unlisted active file")
	})

	t.Run("stale manifest entry fails", func(t *testing.T) {
		t.Parallel()
		err := scan.ReconcileOwners([]string{"skills/a.md"}, manifest)
		require.Error(t, err)
		assert.ErrorContains(t, err, "skills/b.md")
		assert.ErrorContains(t, err, "stale owner-manifest entry")
	})
}

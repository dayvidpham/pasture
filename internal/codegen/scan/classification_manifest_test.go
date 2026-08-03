package scan_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClassificationManifestValidatesEveryEntry(t *testing.T) {
	t.Parallel()

	t.Run("valid entries", func(t *testing.T) {
		t.Parallel()
		manifest, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) here.", Section: "body", Ordinal: 0, Classification: scan.ClassificationOrchestration},
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "Call Skill(/pasture:worker) here.", Section: "body", Ordinal: 1, Classification: scan.ClassificationNeutralFalsePositive},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, manifest.Len())
	})

	t.Run("empty owner is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "  ", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "body", Classification: scan.ClassificationOrchestration},
		})
		assert.Error(t, err)
	})

	t.Run("unknown pattern is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: "not_a_pattern", ContentWindow: "x", Section: "body", Classification: scan.ClassificationOrchestration},
		})
		assert.Error(t, err)
	})

	t.Run("empty content window is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "", Section: "body", Classification: scan.ClassificationOrchestration},
		})
		assert.Error(t, err)
	})

	t.Run("empty section is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "  ", Classification: scan.ClassificationOrchestration},
		})
		assert.Error(t, err)
	})

	t.Run("negative ordinal is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "body", Ordinal: -1, Classification: scan.ClassificationOrchestration},
		})
		assert.Error(t, err)
	})

	t.Run("unknown classification is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "body", Classification: "not_real"},
		})
		assert.Error(t, err)
	})

	t.Run("duplicate owner/pattern/content-window/section/ordinal is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.NewClassificationManifest([]scan.ClassificationEntry{
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "body", Ordinal: 0, Classification: scan.ClassificationOrchestration},
			{Owner: "skills/a.md", Pattern: scan.PatternSkillInvocation, ContentWindow: "x", Section: "body", Ordinal: 0, Classification: scan.ClassificationNeutralFalsePositive},
		})
		assert.ErrorContains(t, err, "duplicates")
	})
}

func TestDecodeClassificationManifestStrictJSON(t *testing.T) {
	t.Parallel()

	t.Run("valid document decodes, including an explicit ordinal 0", func(t *testing.T) {
		t.Parallel()
		manifest, err := scan.DecodeClassificationManifest([]byte(`{
			"entries": [
				{"owner": "skills/a.md", "pattern": "skill_invocation", "content_window": "Call Skill(/pasture:worker) here.", "section": "body", "ordinal": 0, "classification": "orchestration"}
			]
		}`))
		require.NoError(t, err)
		assert.Equal(t, 1, manifest.Len())
	})

	t.Run("omitted ordinal is rejected even though its zero value would otherwise decode silently", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{
			"entries": [
				{"owner": "skills/a.md", "pattern": "skill_invocation", "content_window": "Call Skill(/pasture:worker) here.", "section": "body", "classification": "orchestration"}
			]
		}`))
		require.Error(t, err)
		assert.ErrorContains(t, err, "ordinal")
	})

	t.Run("omitted section is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{
			"entries": [
				{"owner": "skills/a.md", "pattern": "skill_invocation", "content_window": "Call Skill(/pasture:worker) here.", "ordinal": 0, "classification": "orchestration"}
			]
		}`))
		require.Error(t, err)
		assert.ErrorContains(t, err, "section")
	})

	t.Run("missing entries field is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{}`))
		assert.Error(t, err)
	})

	t.Run("duplicate JSON member is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{
			"entries": [{"owner": "skills/a.md", "owner": "skills/a.md", "pattern": "skill_invocation", "content_window": "x", "section": "body", "ordinal": 0, "classification": "orchestration"}]
		}`))
		assert.Error(t, err)
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{
			"entries": [{"owner": "skills/a.md", "pattern": "skill_invocation", "content_window": "x", "section": "body", "ordinal": 0, "classification": "orchestration", "unexpected": 1}]
		}`))
		assert.Error(t, err)
	})

	t.Run("trailing content is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`{"entries": []}{}`))
		assert.Error(t, err)
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := scan.DecodeClassificationManifest([]byte(`not json`))
		assert.Error(t, err)
	})
}

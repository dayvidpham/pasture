package acp_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// ContentBlockInput mirrors the YAML structure of a single content block
// fixture entry (the "block" field inside each test case).
type ContentBlockInput struct {
	Type    string `yaml:"type"`
	Content string `yaml:"content"`
	Text    string `yaml:"text"`
}

// ContentBlockCase holds one row from content_block.yaml.
type ContentBlockCase struct {
	ID       string            `yaml:"id"`
	Block    ContentBlockInput `yaml:"block"`
	WantText string            `yaml:"want_text"`
}

// ContentBlockFixtures is the top-level YAML envelope for content_block.yaml.
type ContentBlockFixtures struct {
	Tests []ContentBlockCase `yaml:"tests"`
}

// TestContentBlock_DualFields verifies that Index resolves ContentBlock.Content
// and ContentBlock.Text according to the dual-field precedence rule:
//   - Content is the canonical field and wins when non-empty.
//   - Text is the live-wire alias and is used only when Content is empty.
//   - tool_use blocks are not text-like and produce no ContentPreview.
func TestContentBlock_DualFields(t *testing.T) {
	t.Parallel()

	var fixtures ContentBlockFixtures
	testutil.LoadFixtures(t, testutil.ContentBlock, &fixtures)

	for _, tc := range fixtures.Tests {
		tc := tc // capture range variable
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()

			block := acp.ContentBlock{
				Type:    tc.Block.Type,
				Content: tc.Block.Content,
				Text:    tc.Block.Text,
			}

			update := acp.SessionUpdate{
				SessionID: tc.ID,
				Role:      "assistant",
				Content:   []acp.ContentBlock{block},
			}

			idx := acp.NewSharedIndexer()
			entries, err := idx.Index([]acp.SessionUpdate{update})
			if err != nil {
				t.Fatalf("Index returned unexpected error: %v", err)
			}
			if len(entries) == 0 {
				t.Fatal("Index returned no entries")
			}

			e := entries[0]

			if tc.WantText == "" {
				if e.ContentPreview != nil {
					t.Errorf("ContentPreview: got %q, want nil (empty want_text)", *e.ContentPreview)
				}
				return
			}

			if e.ContentPreview == nil {
				t.Fatalf("ContentPreview: got nil, want %q", tc.WantText)
			}
			if *e.ContentPreview != tc.WantText {
				t.Errorf("ContentPreview: got %q, want %q", *e.ContentPreview, tc.WantText)
			}
		})
	}
}

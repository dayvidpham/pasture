package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// ContentBlockInput mirrors the YAML structure of a single content block
// fixture entry (the "block" field inside each test case).
type ContentBlockInput struct {
	Type string `yaml:"type"`
	Text string `yaml:"text"`
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

// TestContentBlock_TextExtraction verifies the field consolidation: a
// ContentBlock has a single canonical Text field (the ACP spec wire key
// "text"), text/thinking blocks surface their payload as a ContentPreview, and
// non-text blocks (tool_use) produce none.
//
// Each case is decoded from a JSON object (not a Go struct literal) so the test
// exercises the real spec wire shape end-to-end into the indexer.
func TestContentBlock_TextExtraction(t *testing.T) {
	t.Parallel()

	var fixtures ContentBlockFixtures
	testutil.LoadFixtures(t, testutil.ContentBlock, &fixtures)

	for _, tc := range fixtures.Tests {
		tc := tc // capture range variable
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()

			// Build the spec wire JSON object (the "text" key) and decode it,
			// mirroring how the live ACP client receives a content block.
			wire := map[string]string{"type": tc.Block.Type}
			if tc.Block.Text != "" {
				wire["text"] = tc.Block.Text
			}
			data, err := json.Marshal(wire)
			if err != nil {
				t.Fatalf("marshal fixture block: %v", err)
			}
			var block acp.ContentBlock
			if err := json.Unmarshal(data, &block); err != nil {
				t.Fatalf("decode ContentBlock(%s): %v", data, err)
			}
			if block.Text != tc.WantText {
				t.Errorf("block.Text: got %q, want %q (from %s)", block.Text, tc.WantText, data)
			}

			update := acp.SessionUpdate{
				SessionId: tc.ID,
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

// TestContentBlock_MarshalRoundTrip proves the spec key round-trips
// symmetrically with the standard library (no custom Marshal/UnmarshalJSON):
// marshaling a ContentBlock emits the ACP "text" key and unmarshaling that
// output reconstructs the same value. The negative check decodes to a key map
// so it cannot be fooled by substring collisions (e.g. the "rawContent" key).
func TestContentBlock_MarshalRoundTrip(t *testing.T) {
	t.Parallel()

	orig := acp.ContentBlock{Type: "text", Text: "round trip"}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Inspect the emitted JSON keys precisely: the spec "text" key must carry
	// the value, and no non-spec "content" key may appear.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decode marshaled bytes to key map: %v", err)
	}
	if _, ok := keys["content"]; ok {
		t.Errorf("marshaled JSON contains non-spec %q key: %s", "content", data)
	}
	rawText, ok := keys["text"]
	if !ok {
		t.Fatalf("marshaled JSON missing spec %q key: %s", "text", data)
	}
	var gotText string
	if err := json.Unmarshal(rawText, &gotText); err != nil {
		t.Fatalf("decode text value: %v", err)
	}
	if gotText != "round trip" {
		t.Errorf("marshaled text = %q, want %q", gotText, "round trip")
	}

	var back acp.ContentBlock
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Type != orig.Type || back.Text != orig.Text {
		t.Errorf("round trip: got {Type:%q Text:%q}, want {Type:%q Text:%q}",
			back.Type, back.Text, orig.Type, orig.Text)
	}
}

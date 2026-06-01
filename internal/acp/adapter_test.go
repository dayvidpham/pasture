package acp_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
)

// TestStaticRegistry_ClaudeAdapterPresent verifies that the compile-time
// registry contains the claude-jsonl adapter.
func TestStaticRegistry_ClaudeAdapterPresent(t *testing.T) {
	got, err := acp.GetAdapter("claude-jsonl")
	if err != nil {
		t.Fatalf("GetAdapter(\"claude-jsonl\"): unexpected error: %v", err)
	}
	if got.Format() != "claude-jsonl" {
		t.Errorf("Format: got %q, want %q", got.Format(), "claude-jsonl")
	}
}

// TestStaticRegistry_OpenCodeAdapterPresent verifies that the compile-time
// registry contains the opencode-json adapter.
func TestStaticRegistry_OpenCodeAdapterPresent(t *testing.T) {
	got, err := acp.GetAdapter("opencode-json")
	if err != nil {
		t.Fatalf("GetAdapter(\"opencode-json\"): unexpected error: %v", err)
	}
	if got.Format() != "opencode-json" {
		t.Errorf("Format: got %q, want %q", got.Format(), "opencode-json")
	}
}

// TestStaticRegistry_AllExpectedFormats verifies that RegisteredFormats
// contains all expected compile-time adapters.
func TestStaticRegistry_AllExpectedFormats(t *testing.T) {
	want := []string{"claude-jsonl", "opencode-json"}
	formats := acp.RegisteredFormats()

	formatSet := make(map[string]bool, len(formats))
	for _, f := range formats {
		formatSet[f] = true
	}

	for _, w := range want {
		if !formatSet[w] {
			t.Errorf("RegisteredFormats: missing expected format %q; got %v", w, formats)
		}
	}
}

// TestGetAdapter_UnknownFormat verifies that requesting an unregistered format
// returns a descriptive error listing available formats.
func TestGetAdapter_UnknownFormat(t *testing.T) {
	_, err := acp.GetAdapter("does-not-exist-xyz")
	if err == nil {
		t.Fatal("GetAdapter: expected error for unknown format, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does-not-exist-xyz") {
		t.Errorf("error message should contain the unknown format name: %q", msg)
	}
	// Error should mention how to fix it (adding to static registry).
	if !strings.Contains(msg, "adapter.go") {
		t.Errorf("error message should mention adapter.go for static registration: %q", msg)
	}
}

// TestStaticRegistry_AdaptersAreReadOnly verifies that calling GetAdapter
// multiple times returns a consistent result (registry is read-only / idempotent).
func TestStaticRegistry_AdaptersAreReadOnly(t *testing.T) {
	first, err := acp.GetAdapter("claude-jsonl")
	if err != nil {
		t.Fatalf("first GetAdapter: %v", err)
	}
	second, err := acp.GetAdapter("claude-jsonl")
	if err != nil {
		t.Fatalf("second GetAdapter: %v", err)
	}
	if first.Format() != second.Format() {
		t.Errorf("successive GetAdapter calls returned different formats: %q vs %q",
			first.Format(), second.Format())
	}
}

// TestStaticRegistry_AdaptersCanParse verifies that statically registered
// adapters can perform a basic parse without error.
func TestStaticRegistry_AdaptersCanParse(t *testing.T) {
	cases := []struct {
		format string
		record []byte
	}{
		{
			"claude-jsonl",
			[]byte(`{"type":"message","role":"assistant","sessionId":"s","content":[]}`),
		},
		{
			"opencode-json",
			[]byte(`{"id":"x","sessionId":"s","role":"assistant"}`),
		},
	}

	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			a, err := acp.GetAdapter(c.format)
			if err != nil {
				t.Fatalf("GetAdapter(%q): %v", c.format, err)
			}
			_, parseErr := a.Parse(c.record)
			if parseErr != nil {
				t.Errorf("Parse: unexpected error: %v", parseErr)
			}
		})
	}
}

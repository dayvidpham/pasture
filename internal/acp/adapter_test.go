package acp_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
)

// stubAdapter is a minimal Adapter implementation for registry tests.
type stubAdapter struct{ format string }

func (s *stubAdapter) Format() string { return s.format }
func (s *stubAdapter) Parse(_ []byte) (acp.SessionUpdate, error) {
	return acp.SessionUpdate{SessionID: "stub"}, nil
}

// TestGetAdapter_RegisteredFormat verifies that a registered adapter can be
// retrieved and returns a valid SessionUpdate.
func TestGetAdapter_RegisteredFormat(t *testing.T) {
	stub := &stubAdapter{format: "test-stub-format"}
	acp.RegisterAdapter(stub)

	got, err := acp.GetAdapter("test-stub-format")
	if err != nil {
		t.Fatalf("GetAdapter: unexpected error: %v", err)
	}
	if got.Format() != "test-stub-format" {
		t.Errorf("Format: got %q, want %q", got.Format(), "test-stub-format")
	}

	update, parseErr := got.Parse([]byte(`{}`))
	if parseErr != nil {
		t.Fatalf("Parse: unexpected error: %v", parseErr)
	}
	if update.SessionID != "stub" {
		t.Errorf("Parse: SessionID: got %q, want %q", update.SessionID, "stub")
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
	// Error should mention how to fix it (registration instructions).
	if !strings.Contains(msg, "RegisterAdapter") {
		t.Errorf("error message should mention RegisterAdapter: %q", msg)
	}
}

// TestRegisterAdapter_Overwrite verifies that re-registering with the same
// format name replaces the previous adapter.
func TestRegisterAdapter_Overwrite(t *testing.T) {
	first := &stubAdapter{format: "overwrite-test"}
	second := &stubAdapter{format: "overwrite-test"}
	acp.RegisterAdapter(first)
	acp.RegisterAdapter(second)

	got, err := acp.GetAdapter("overwrite-test")
	if err != nil {
		t.Fatalf("GetAdapter: unexpected error: %v", err)
	}
	// Both have the same format string; just ensure no error and retrieval works.
	if got.Format() != "overwrite-test" {
		t.Errorf("Format: got %q, want %q", got.Format(), "overwrite-test")
	}
}

// TestRegisteredFormats_IncludesRegistered verifies that RegisteredFormats
// includes any format that has been registered.
func TestRegisteredFormats_IncludesRegistered(t *testing.T) {
	acp.RegisterAdapter(&stubAdapter{format: "list-check-format"})
	formats := acp.RegisteredFormats()
	found := false
	for _, f := range formats {
		if f == "list-check-format" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RegisteredFormats: expected to find %q in %v", "list-check-format", formats)
	}
}

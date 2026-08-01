package model

import (
	"encoding/json"
	"testing"
)

func TestNativeBindingNativeNameJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := NativeBinding{Kind: BindingSession, NativeName: "session_id", Value: "session-é-<>&"}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got NativeBinding
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.NativeName != want.NativeName {
		t.Fatalf("NativeName = %q, want %q (JSON %s)", got.NativeName, want.NativeName, encoded)
	}
	if got != want {
		t.Fatalf("round-tripped binding = %#v, want %#v", got, want)
	}
}

package model

import (
	"encoding/base64"
	"testing"
)

func TestCursorCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	var fp QueryFingerprint
	fp[0] = 0xab
	encoded, err := EncodeCursor(Cursor{SnapshotJournalID: 10, LastJournalID: 4, QueryFingerprint: fp})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SnapshotJournalID != 10 || decoded.LastJournalID != 4 || decoded.QueryFingerprint != fp {
		t.Fatalf("decoded=%#v", decoded)
	}
	if len(encoded) > 0 && encoded[len(encoded)-1] == '=' {
		t.Fatalf("cursor is padded: %q", encoded)
	}
}
func TestCursorRejectsStrictJSONFailures(t *testing.T) {
	t.Parallel()
	cases := []string{`{"version":1,"version":1,"snapshotJournalId":2,"lastJournalId":1,"queryFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}`, `{"version":1,"snapshotJournalId":2,"lastJournalId":1,"queryFingerprint":"0000000000000000000000000000000000000000000000000000000000000000","unknown":1}`, `{"version":2,"snapshotJournalId":2,"lastJournalId":1,"queryFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}`, `{"version":1,"snapshotJournalId":2,"queryFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}`}
	for _, raw := range cases {
		if _, err := DecodeCursor(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatalf("accepted invalid cursor JSON %s", raw)
		}
	}
	if _, err := DecodeCursor(base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"snapshotJournalId":2,"lastJournalId":3,"queryFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}`))); err == nil {
		t.Fatal("accepted cursor beyond snapshot")
	}
}

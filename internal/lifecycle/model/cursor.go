package model

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"
)

const lifecycleCursorVersion = 1

type cursorWire struct {
	Version           int                  `json:"version"`
	SnapshotJournalID provenance.JournalID `json:"snapshotJournalId"`
	LastJournalID     provenance.JournalID `json:"lastJournalId"`
	QueryFingerprint  string               `json:"queryFingerprint"`
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.SnapshotJournalID <= 0 || cursor.LastJournalID <= 0 || cursor.LastJournalID > cursor.SnapshotJournalID {
		return "", fmt.Errorf("encode lifecycle cursor: invalid journal bounds")
	}
	w := cursorWire{Version: lifecycleCursorVersion, SnapshotJournalID: cursor.SnapshotJournalID, LastJournalID: cursor.LastJournalID, QueryFingerprint: hex.EncodeToString(cursor.QueryFingerprint[:])}
	raw, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("encode lifecycle cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeCursor(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode lifecycle cursor: expected unpadded URL-safe base64: %w", err)
	}
	if base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return nil, fmt.Errorf("decode lifecycle cursor: encoding is not canonical unpadded URL-safe base64")
	}
	if err := rejectCursorDuplicates(raw); err != nil {
		return nil, err
	}
	var w cursorWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode lifecycle cursor JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("decode lifecycle cursor JSON: trailing value")
	}
	if w.Version != lifecycleCursorVersion {
		return nil, fmt.Errorf("decode lifecycle cursor: unsupported version %d", w.Version)
	}
	if w.SnapshotJournalID <= 0 || w.LastJournalID <= 0 || w.LastJournalID > w.SnapshotJournalID {
		return nil, fmt.Errorf("decode lifecycle cursor: invalid journal bounds")
	}
	digest, err := hex.DecodeString(w.QueryFingerprint)
	if err != nil || len(digest) != len(QueryFingerprint{}) {
		return nil, fmt.Errorf("decode lifecycle cursor: queryFingerprint must be exactly 64 lowercase hexadecimal characters")
	}
	if hex.EncodeToString(digest) != w.QueryFingerprint {
		return nil, fmt.Errorf("decode lifecycle cursor: queryFingerprint is not canonical lowercase hexadecimal")
	}
	var fp QueryFingerprint
	copy(fp[:], digest)
	canonical, _ := json.Marshal(w)
	if !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("decode lifecycle cursor: JSON is not canonical field-ordered compact v1")
	}
	return &Cursor{SnapshotJournalID: w.SnapshotJournalID, LastJournalID: w.LastJournalID, QueryFingerprint: fp}, nil
}

func rejectCursorDuplicates(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("decode lifecycle cursor JSON: %w", err)
	}
	if token != json.Delim('{') {
		return fmt.Errorf("decode lifecycle cursor JSON: expected object")
	}
	seen := map[string]bool{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return err
		}
		key := keyToken.(string)
		if seen[key] {
			return fmt.Errorf("decode lifecycle cursor JSON: duplicate member %q", key)
		}
		seen[key] = true
		var value any
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	for _, required := range []string{"version", "snapshotJournalId", "lastJournalId", "queryFingerprint"} {
		if !seen[required] {
			return fmt.Errorf("decode lifecycle cursor JSON: missing member %q", required)
		}
	}
	return nil
}

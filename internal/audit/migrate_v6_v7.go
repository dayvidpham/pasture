package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type lifecycleV6Binding struct {
	Kind       uint8  `json:"Kind"`
	NativeName string `json:"NativeName"`
	Value      string `json:"Value"`
}

func migrateV6toV7Step(tx *sql.Tx, now int64) error {
	if _, err := tx.Exec(`CREATE TABLE lifecycle_occurrence_bindings (journal_id INTEGER NOT NULL REFERENCES lifecycle_occurrences(journal_id) ON DELETE CASCADE, binding_index INTEGER NOT NULL CHECK(binding_index BETWEEN 0 AND 15), binding_kind INTEGER NOT NULL CHECK(binding_kind BETWEEN 1 AND 8), native_name BLOB NOT NULL CHECK(length(native_name) BETWEEN 1 AND 512), binding_value BLOB NOT NULL CHECK(length(binding_value) BETWEEN 1 AND 512), PRIMARY KEY(journal_id,binding_index)) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("create lifecycle v7 bindings table: %w", err)
	}
	rows, err := tx.Query(`SELECT journal_id, bindings_json FROM lifecycle_occurrences ORDER BY journal_id`)
	if err != nil {
		return fmt.Errorf("read lifecycle v6 bindings: %w", err)
	}
	type source struct {
		id  int64
		raw []byte
	}
	var sources []source
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.id, &s.raw); err != nil {
			rows.Close()
			return err
		}
		sources = append(sources, s)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, s := range sources {
		var bindings []lifecycleV6Binding
		dec := json.NewDecoder(strings.NewReader(string(s.raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&bindings); err != nil || bindings == nil {
			return fmt.Errorf("decode lifecycle v6 bindings for journal %d: %w", s.id, err)
		}
		for i, b := range bindings {
			if !validBindingBytes(b.NativeName) || !validBindingBytes(b.Value) || b.Kind < 1 || b.Kind > 8 {
				return fmt.Errorf("invalid lifecycle v6 binding %d for journal %d", i, s.id)
			}
			if _, err := tx.Exec(`INSERT INTO lifecycle_occurrence_bindings(journal_id,binding_index,binding_kind,native_name,binding_value) VALUES(?,?,?,?,?)`, s.id, i, b.Kind, []byte(b.NativeName), []byte(b.Value)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`CREATE INDEX lifecycle_occurrence_bindings_lookup ON lifecycle_occurrence_bindings(binding_kind,native_name,binding_value,journal_id)`); err != nil {
		return err
	}
	return writeVersion(tx, 7, now)
}

func validBindingBytes(v string) bool {
	if !utf8.ValidString(v) || len(v) < 1 || len(v) > 512 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

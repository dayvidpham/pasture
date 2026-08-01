package audit

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

type lifecycleV6Binding struct {
	Kind       uint8  `json:"Kind"`
	NativeName string `json:"NativeName"`
	Value      string `json:"Value"`
}
type lifecycleV6Row struct {
	journalID int64
	bindings  []lifecycleV6Binding
}

func migrateV6toV7Step(tx *sql.Tx, now int64) error {
	rows, err := tx.Query(`SELECT journal_id,bindings_json FROM lifecycle_occurrences ORDER BY journal_id`)
	if err != nil {
		return fmt.Errorf("read lifecycle v6 bindings: %w", err)
	}
	var source []lifecycleV6Row
	expectedBindings := 0
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		bindings, err := decodeV6Bindings(raw)
		if err != nil {
			rows.Close()
			return fmt.Errorf("decode lifecycle v6 bindings for journal %d: %w", id, err)
		}
		expectedBindings += len(bindings)
		source = append(source, lifecycleV6Row{journalID: id, bindings: bindings})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	statements := []string{
		`ALTER TABLE lifecycle_occurrences RENAME TO lifecycle_occurrences_v6`,
		`DROP INDEX lifecycle_occurrences_contract_event_journal`,
		`CREATE TABLE lifecycle_occurrences (journal_id INTEGER PRIMARY KEY, contract TEXT NOT NULL, event_kind INTEGER NOT NULL, received_at INTEGER NOT NULL, actor_id TEXT NOT NULL, capture_disposition INTEGER NOT NULL, payload_digest TEXT NOT NULL REFERENCES lifecycle_payload_blobs(digest), envelope_json BLOB NOT NULL, snapshot_journal_id INTEGER NOT NULL, CHECK(journal_id>0), CHECK(snapshot_journal_id>=journal_id))`,
		`INSERT INTO lifecycle_occurrences(journal_id,contract,event_kind,received_at,actor_id,capture_disposition,payload_digest,envelope_json,snapshot_journal_id) SELECT journal_id,contract,event_kind,received_at,actor_id,capture_disposition,payload_digest,envelope_json,snapshot_journal_id FROM lifecycle_occurrences_v6 ORDER BY journal_id`,
		`CREATE INDEX lifecycle_occurrences_contract_event_journal ON lifecycle_occurrences(contract,event_kind,journal_id)`,
		`CREATE TABLE lifecycle_occurrence_bindings (journal_id INTEGER NOT NULL REFERENCES lifecycle_occurrences(journal_id) ON DELETE CASCADE, binding_index INTEGER NOT NULL CHECK(binding_index BETWEEN 0 AND 15), binding_kind INTEGER NOT NULL CHECK(binding_kind BETWEEN 1 AND 8), native_name BLOB NOT NULL CHECK(length(native_name) BETWEEN 1 AND 512), binding_value BLOB NOT NULL CHECK(length(binding_value) BETWEEN 1 AND 512), PRIMARY KEY(journal_id,binding_index)) STRICT, WITHOUT ROWID`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("build lifecycle v7 schema: %w", err)
		}
	}
	for _, row := range source {
		for index, binding := range row.bindings {
			if _, err := tx.Exec(`INSERT INTO lifecycle_occurrence_bindings(journal_id,binding_index,binding_kind,native_name,binding_value) VALUES(?,?,?,?,?)`, row.journalID, index, binding.Kind, []byte(binding.NativeName), []byte(binding.Value)); err != nil {
				return fmt.Errorf("backfill lifecycle binding %d for journal %d: %w", index, row.journalID, err)
			}
		}
	}
	if _, err := tx.Exec(`CREATE INDEX lifecycle_occurrence_bindings_lookup ON lifecycle_occurrence_bindings(binding_kind,native_name,binding_value,journal_id)`); err != nil {
		return err
	}
	var parents, children int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM lifecycle_occurrences`).Scan(&parents); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM lifecycle_occurrence_bindings`).Scan(&children); err != nil {
		return err
	}
	if parents != len(source) || children != expectedBindings {
		return fmt.Errorf("verify lifecycle v7 counts: parents=%d want=%d bindings=%d want=%d", parents, len(source), children, expectedBindings)
	}
	foreign, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer foreign.Close()
	if foreign.Next() {
		return fmt.Errorf("verify lifecycle v7 foreign keys: violation remains")
	}
	if err := foreign.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE lifecycle_occurrences_v6`); err != nil {
		return fmt.Errorf("remove lifecycle v6 table after verified copy: %w", err)
	}
	return writeVersion(tx, 7, now)
}

func decodeV6Bindings(raw []byte) ([]lifecycleV6Binding, error) {
	if err := rejectDuplicateMembers(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var bindings []lifecycleV6Binding
	if err := dec.Decode(&bindings); err != nil {
		return nil, err
	}
	if bindings == nil {
		return nil, fmt.Errorf("bindings must be one non-null array")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON value")
	}
	if len(bindings) > 16 {
		return nil, fmt.Errorf("bindings exceed 16-item bound")
	}
	for i, b := range bindings {
		if model.ValidateNativeBinding(model.NativeBinding{Kind: model.NativeBindingKind(b.Kind), NativeName: b.NativeName, Value: b.Value}) != nil {
			return nil, fmt.Errorf("invalid binding at index %d", i)
		}
	}
	return bindings, nil
}
func rejectDuplicateMembers(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key := keyToken.(string)
				if seen[key] {
					return fmt.Errorf("duplicate object member %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter")
		}
	}
	return walk()
}

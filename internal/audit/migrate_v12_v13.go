package audit

import (
	"database/sql"
	"fmt"
)

func migrateV12toV13Step(tx *sql.Tx, now int64) error {
	statements := []string{
		`CREATE TABLE pasture_session_execution (journal_id INTEGER PRIMARY KEY, harness TEXT NOT NULL, session_id TEXT NOT NULL, kind TEXT NOT NULL, native_agent_id TEXT NOT NULL, turn_id TEXT NOT NULL, request_id TEXT NOT NULL, observed_at INTEGER NOT NULL, execution_json BLOB NOT NULL)`,
		`CREATE INDEX pasture_session_execution_session ON pasture_session_execution(harness, session_id, journal_id)`,
		`CREATE INDEX pasture_session_execution_scope ON pasture_session_execution(harness, session_id, native_agent_id, turn_id, request_id, kind, journal_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("upgrade execution cache from schema 12 to 13: %w; repair the store and retry pasture migrate", err)
		}
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='pasture_session_api_state')`).Scan(&exists); err != nil {
		return fmt.Errorf("check session cache during schema upgrade: %w; repair the store and retry pasture migrate", err)
	}
	if exists {
		if _, err := tx.Exec(`UPDATE pasture_session_api_state SET cache_version=?, dirty=1, completed_through_jid=0, revision=revision+1 WHERE singleton_id=0 AND cache_version < ?`, SessionCacheVersion, SessionCacheVersion); err != nil {
			return fmt.Errorf("invalidate session cache during schema upgrade: %w; repair the store and retry pasture migrate", err)
		}
	}
	return writeVersion(tx, 13, now)
}

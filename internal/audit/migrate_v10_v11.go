package audit

import (
	"database/sql"
	"fmt"
)

// SessionCacheVersion identifies the disposable prompt/end/resume cache layout.
// Fresh stores use the same version as upgraded stores; opening a current store
// must not invalidate a successfully rebuilt cache again.
const SessionCacheVersion = 2

func migrateV10toV11Step(tx *sql.Tx, now int64) error {
	for _, table := range []string{"pasture_session_state", "pasture_session_api_state"} {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return sessionCacheMigrationError(table, err)
		}
		// Fresh audit-only stores have no session cache yet. The unified-store
		// initializer creates the current layout when they are first opened.
		if !exists {
			continue
		}
		columns := []string{"cache_version"}
		if table == "pasture_session_state" {
			columns = []string{"last_prompt_at", "last_prompt_journal_id", "host_end_at", "host_end_journal_id", "last_resume_at", "last_resume_journal_id"}
		}
		for _, column := range columns {
			var present bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, column).Scan(&present); err != nil {
				return sessionCacheMigrationError(table+"."+column, err)
			}
			if present {
				continue
			}
			if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return sessionCacheMigrationError(table+"."+column, err)
			}
		}
		if table == "pasture_session_api_state" {
			if _, err := tx.Exec(`UPDATE pasture_session_api_state SET cache_version=1, dirty=1, completed_through_jid=0, revision=revision+1 WHERE singleton_id=0 AND cache_version < 1`); err != nil {
				return sessionCacheMigrationError(table, err)
			}
		}
	}
	return writeVersion(tx, 11, now)
}

func sessionCacheMigrationError(object string, err error) error {
	return fmt.Errorf("upgrade session cache %s from schema 10 to 11: %w; the migration rolls back without partial cache changes; confirm the store is writable and retry pasture migrate", object, err)
}

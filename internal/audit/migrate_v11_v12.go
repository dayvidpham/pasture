package audit

import (
	"database/sql"
	"fmt"
)

// The marker is published by the unified store only after the fixed actor's
// registration has settled successfully, never by the schema migration.
func migrateV11toV12Step(tx *sql.Tx, now int64) error {
	if _, err := tx.Exec(`CREATE TABLE pasture_default_llm_initialization (singleton_id INTEGER PRIMARY KEY CHECK(singleton_id = 0), version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create default LLM initialization marker during schema migration: %w; repair the store and retry pasture migrate", err)
	}
	return writeVersion(tx, 12, now)
}

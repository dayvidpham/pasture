package audit

import (
	"context"
	"testing"
)

func TestExecutionCacheUpgradeInvalidatesOnce(t *testing.T) {
	t.Parallel()
	db := legacySessionCache(t)
	if err := MigrateContextToCeiling(context.Background(), db, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE pasture_session_api_state SET dirty=0, completed_through_jid=42`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var revision, dirty, cursor, version int
	if err := db.QueryRow(`SELECT revision,dirty,completed_through_jid,cache_version FROM pasture_session_api_state`).Scan(&revision, &dirty, &cursor, &version); err != nil {
		t.Fatal(err)
	}
	if revision != 9 || dirty != 1 || cursor != 0 || version != 2 {
		t.Fatalf("upgrade state = %d/%d/%d/%d", revision, dirty, cursor, version)
	}
	if _, err := db.Exec(`INSERT INTO pasture_session_execution(journal_id,harness,session_id,kind,native_agent_id,turn_id,request_id,observed_at,execution_json) VALUES(1,'claude-code','s','selection','','','',1,'{}')`); err != nil {
		t.Fatal(err)
	}
}

package peasantproject

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/peasant-labs/schema"
	_ "modernc.org/sqlite" // pure-Go driver; CGO_ENABLED=0 compatible

	"github.com/dayvidpham/pasture/internal/dbconn"
)

// dbSchemaTimeout bounds the constructor's probe and shape guard. The source is
// optional integration state: a slow or missing Peasant database must not delay
// serve startup indefinitely.
const dbSchemaTimeout = 2 * time.Second

// maxDBLookupKeys is a backstop on the VALUES list. The session API requests at
// most one page (100 sessions) or one detail, so production never approaches
// it; a larger direct caller is refused instead of building an unbounded query.
const maxDBLookupKeys = 1000

// The Peasant database contract this integration reads. Peasant owns these
// tables and columns; Pasture only reads them and never migrates or writes.
var dbTableContract = []struct {
	table   string
	columns []string
}{
	{table: "projects", columns: []string{"project_hash", "canonical_cwd", "canonical_remote"}},
	{table: "sessions", columns: []string{"session_id", "model_harness", "project_hash"}},
}

// dbSource reads project mappings from a Peasant SQLite database over a
// read-only handle. It never writes, migrates, or creates the file.
type dbSource struct {
	db *sql.DB

	mu     sync.Mutex
	status Status
}

// NewDBSource opens path read-only and verifies the Peasant schema. A missing
// file, an unreadable file, or an incompatible schema produces an unavailable
// source, never an error: serve must still start and sessions must still read.
func NewDBSource(path string) Source {
	source := &dbSource{status: Status{Enabled: true, Source: SourceDB, State: StateOK}}
	if strings.TrimSpace(path) == "" {
		source.markUnavailable("the Peasant database path is empty")
		return source
	}
	// mode=ro refuses to create the file; query_only is a second fence against
	// any statement that would write to an existing one.
	db, err := sql.Open("sqlite", dbconn.ReadOnlyDSN(path)+"&_pragma=query_only(1)")
	if err != nil {
		source.markUnavailable("the Peasant database could not be opened: " + err.Error())
		return source
	}
	// One connection keeps the per-connection query_only pragma and the
	// read-only handle coherent; the source is a low-rate optional reader.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), dbSchemaTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		source.markUnavailable("the Peasant database is unavailable: " + err.Error())
		return source
	}
	if problem, ok := dbSchemaProblem(ctx, db); !ok {
		_ = db.Close()
		source.markUnavailable(problem)
		return source
	}
	source.db = db
	return source
}

// Status reports the integration state. The source starts ok once the file and
// schema are readable, and flips to unavailable only when a lookup fails.
func (s *dbSource) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *dbSource) markUnavailable(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = StateUnavailable
	s.status.Message = message
}

func (s *dbSource) markOK() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = StateOK
	s.status.Message = ""
}

// Lookup resolves every requested session in ONE query joining the requested
// (session_id, harness) pairs to Peer's sessions and projects rows. It returns
// only verified mappings; a missing session, a harness mismatch, or a malformed
// project hash simply produces no project for that key.
func (s *dbSource) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	out := make(map[Key]Project, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	s.mu.Lock()
	db := s.db
	unavailableMessage := ""
	if s.status.State != StateOK {
		unavailableMessage = s.status.Message
	}
	s.mu.Unlock()
	if db == nil || unavailableMessage != "" {
		return nil, fmt.Errorf("the Peasant project source is unavailable: %s", unavailableMessage)
	}

	unique := make([]Key, 0, len(keys))
	seen := make(map[Key]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	if len(unique) > maxDBLookupKeys {
		return nil, fmt.Errorf("the Peasant project lookup requested %d sessions, above the %d-session bound; request one page at a time", len(unique), maxDBLookupKeys)
	}

	requested := make(map[string]Key, len(unique))
	args := make([]any, 0, len(unique)*2)
	var values strings.Builder
	for i, key := range unique {
		if i > 0 {
			values.WriteString(", ")
		}
		values.WriteString("(?, ?)")
		args = append(args, key.Session, key.Harness)
		requested[key.Session] = key
	}
	statement := `WITH keys(session_id, model_harness) AS (VALUES ` + values.String() + `)
		SELECT k.session_id, s.model_harness, p.project_hash, p.canonical_remote, p.canonical_cwd
		FROM keys k
		LEFT JOIN sessions s ON s.session_id = k.session_id
		LEFT JOIN projects p ON p.project_hash = s.project_hash`

	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		s.markUnavailable("the Peasant database could not be read: " + err.Error())
		return nil, fmt.Errorf("reading the Peasant project mapping: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var storedHarness, projectHash, canonicalRemote, canonicalCWD sql.NullString
		if err := rows.Scan(&sessionID, &storedHarness, &projectHash, &canonicalRemote, &canonicalCWD); err != nil {
			s.markUnavailable("a Peasant project mapping row could not be decoded: " + err.Error())
			return nil, fmt.Errorf("decoding a Peasant project mapping row: %w", err)
		}
		key, ok := requested[sessionID]
		if !ok || !storedHarness.Valid || storedHarness.String != key.Harness {
			// The session join found no row, or Peasant recorded a different
			// harness for that session ID. Neither is this Pasture session.
			continue
		}
		if !projectHash.Valid || projectHash.String == "" {
			continue
		}
		hash, err := schema.NewProjectHash(projectHash.String)
		if err != nil {
			// A hash Peasant's own wire contract rejects cannot be presented as
			// a project identity; leave the session without project fields.
			continue
		}
		out[key] = Project{Name: firstNonEmpty(canonicalRemote.String, canonicalCWD.String, hash.String()), Hash: hash.String()}
	}
	if err := rows.Err(); err != nil {
		s.markUnavailable("iterating Peasant project mappings failed: " + err.Error())
		return nil, fmt.Errorf("iterating Peasant project mappings: %w", err)
	}
	s.markOK()
	return out, nil
}

// Close releases the read-only handle. It is safe to call more than once.
func (s *dbSource) Close() error {
	s.mu.Lock()
	db := s.db
	s.db = nil
	s.mu.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// dbSchemaProblem checks the Peasant table/column contract. It returns the
// operator-facing problem and false when the schema is incompatible. A missing
// table or column is a shape mismatch, not a read fault.
func dbSchemaProblem(ctx context.Context, db *sql.DB) (string, bool) {
	for _, contract := range dbTableContract {
		columns, err := dbTableColumns(ctx, db, contract.table)
		if err != nil {
			return "the Peasant database schema could not be read: " + err.Error(), false
		}
		if columns == nil {
			return fmt.Sprintf("the Peasant database has no %q table", contract.table), false
		}
		for _, required := range contract.columns {
			if _, ok := columns[required]; !ok {
				return fmt.Sprintf("the Peasant %q table is missing the %q column", contract.table, required), false
			}
		}
	}
	return "", true
}

// dbTableColumns reads one table's column names. A nil map with no error means
// the table does not exist.
func dbTableColumns(ctx context.Context, db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{}, 8)
	for rows.Next() {
		var cid, notNull, pk int
		var name, declaredType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &declaredType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, nil
	}
	return columns, nil
}

package idmigrate_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// schemaColumn is one column of the store's own schema.
type schemaColumn struct{ table, column, declaredType string }

// storeSchema walks every user table in the store and returns its columns. The
// walk is the gate's authority: it reads the schema the pinned dependency
// actually creates, not a copy of any declaration.
func storeSchema(t *testing.T, path string) []schemaColumn {
	t.Helper()
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		if strings.HasPrefix(name, "sqlite_") {
			continue
		}
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	var cols []schemaColumn
	for _, table := range tables {
		info, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
		require.NoError(t, err)
		for info.Next() {
			var cid, notNull, pk int
			var name, declaredType string
			var dflt any
			require.NoError(t, info.Scan(&cid, &name, &declaredType, &notNull, &dflt, &pk))
			cols = append(cols, schemaColumn{table, name, strings.ToUpper(declaredType)})
		}
		require.NoError(t, info.Err())
		require.NoError(t, info.Close())
	}
	return cols
}

// classifiedStore builds a current production store: the combined Provenance
// and pasture schema with every table the conversion or the dry run can meet.
// The inference cache is part of the open-time pasture schema (created beside
// pasture_session_state), so it is visible here without running a rebuild.
func classifiedStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "classified.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())
	return path
}

// identityCandidate reports whether a column is one the classification rule
// demands be declared on purpose: a TEXT column whose name is identity-shaped.
// The predicate is deliberately shape-based and red-by-default, mirroring the
// dependency's own declaration gate: a new column is unclassified until it is
// declared an owned identity or recorded as preserved-opaque.
func identityCandidate(col schemaColumn) bool {
	if col.declaredType != "TEXT" {
		return false
	}
	lower := strings.ToLower(col.column)
	return strings.HasSuffix(lower, "_id") || lower == "context_identity"
}

// TestConversionMirrorsClassifyEveryIdentityShapedColumn is the cross-module
// parity pin. It walks the schema the pinned dependency creates and fails when
// any identity-shaped column is in no declared mirror (a column the conversion
// or the dry run would silently ignore) or when a mirror names a column the
// schema does not carry (a stale mirror entry). The provenance-owned, the
// dependency-preserved and the pasture-owned mirrors are all checked, so the
// plan and the conversion cannot diverge again in either direction.
func TestConversionMirrorsClassifyEveryIdentityShapedColumn(t *testing.T) {
	t.Parallel()
	path := classifiedStore(t)
	cols := storeSchema(t, path)

	mirrors := idmigrate.DeclaredConversionMirrors()
	classified := map[string]string{} // "table.column" -> which mirror
	record := func(which string, list []idmigrate.IdentityColumn) {
		for _, col := range list {
			classified[col.Table+"."+col.Column] = which
		}
	}
	record("provenance-owned", mirrors.ProvenanceOwned)
	record("provenance-preserved", mirrors.ProvenancePreserved)
	record("provenance-owned-context", mirrors.ProvenanceOwnedContext)
	record("pasture-owned", mirrors.PastureOwned)
	record("pasture-owned-array", mirrors.PastureOwnedArray)
	record("pasture-preserved", mirrors.PasturePreserved)
	record("pasture-special", mirrors.PastureSpecial)

	// Anti-vacuity: both halves must be visibly populated before the walk means
	// anything.
	require.GreaterOrEqual(t, len(classified), 20, "classification mirrors too small to be meaningful")

	inSchema := map[string]schemaColumn{}
	for _, col := range cols {
		inSchema[col.table+"."+col.column] = col
	}

	var unclassified []string
	candidates := 0
	for _, col := range cols {
		if !identityCandidate(col) {
			continue
		}
		candidates++
		if _, ok := classified[col.table+"."+col.column]; !ok {
			unclassified = append(unclassified, col.table+"."+col.column)
		}
	}
	require.Greater(t, candidates, 0, "the schema walk found no identity-shaped columns")
	sort.Strings(unclassified)
	require.Empty(t, unclassified,
		"identity-shaped columns in no mirror (declare each an owned identity or record it preserved): %s",
		strings.Join(unclassified, ", "))

	// Reverse direction: a mirror entry with no column is a stale declaration.
	var missing []string
	for key := range classified {
		if _, ok := inSchema[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "mirror entries with no column in the schema: %s", strings.Join(missing, ", "))

	// The three context identity columns are a separate declaration the shape
	// walk alone cannot catch (context_identity does not end in "_id"); pin them
	// explicitly so a dropped context column is red, not silently ignored.
	for _, col := range mirrors.ProvenanceOwnedContext {
		require.Contains(t, inSchema, col.Table+"."+col.Column,
			"the context identity mirror names a column the schema does not carry")
	}
	require.Len(t, mirrors.ProvenanceOwnedContext, 3, "the dependency declares exactly three context identity columns")
}

// TestProvenanceMirrorsMatchConvertIDsInTxBucketForBucket is the behavioral half
// of the parity pin. It builds a synthetic schema from the provenance mirrors,
// seeds a retired identity into every owned column (and every kind-routed context
// column), and seeds a retired-looking value into every preserved column. It then
// calls the dependency's exported ConvertIDsInTx and asserts the set of columns
// that actually changed equals the owned-plus-context mirror exactly. A mirror
// column the dependency no longer rewrites, a column it rewrites but the mirror
// omits, or a preserved column it wrongly rewrites all fail here.
//
// The governed-allocation and journal-operation layers are excluded from the
// synthetic schema because their allocation receipts and canonical mutations
// need a full valid owner payload a synthetic row cannot supply; their columns
// are covered by the schema walk in
// TestConversionMirrorsClassifyEveryIdentityShapedColumn.
func TestProvenanceMirrorsMatchConvertIDsInTxBucketForBucket(t *testing.T) {
	t.Parallel()
	mirrors := idmigrate.DeclaredConversionMirrors()

	// The exclusions are named, not silent: a new owned or preserved column in
	// these layers must be added to the assertions below or this test fails.
	excluded := map[string]bool{"governed_allocation_operations": true, "journal_operations": true}
	owned := make([]idmigrate.IdentityColumn, 0, len(mirrors.ProvenanceOwned))
	for _, col := range mirrors.ProvenanceOwned {
		if excluded[col.Table] {
			continue
		}
		owned = append(owned, col)
	}
	preserved := make([]idmigrate.IdentityColumn, 0, len(mirrors.ProvenancePreserved))
	for _, col := range mirrors.ProvenancePreserved {
		if excluded[col.Table] {
			continue
		}
		preserved = append(preserved, col)
	}

	path := filepath.Join(t.TempDir(), "synthetic.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	ctx := context.Background()

	// Build the synthetic schema from every mirror column of every table, so the
	// conversion's SELECTs resolve. Tables are plain TEXT columns with no
	// constraints, so every column is seedable.
	tables := map[string]map[string]bool{}
	addColumn := func(table, column string) {
		if tables[table] == nil {
			tables[table] = map[string]bool{}
		}
		tables[table][column] = true
	}
	for _, col := range owned {
		addColumn(col.Table, col.Column)
	}
	for _, col := range preserved {
		addColumn(col.Table, col.Column)
	}
	for _, col := range mirrors.ProvenanceOwnedContext {
		addColumn(col.Table, "context_kind")
		addColumn(col.Table, col.Column)
	}
	for table, columns := range tables {
		var defs []string
		for column := range columns {
			defs = append(defs, fmt.Sprintf("%q TEXT", column))
		}
		sort.Strings(defs)
		_, err := db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %q (%s)", table, strings.Join(defs, ", ")))
		require.NoError(t, err)
	}

	retired := func(n int) string { return fmt.Sprintf("seed-ns--00000000-0000-0000-0000-%012d", n) }
	seeded := map[string]string{}
	index := 0
	for _, col := range owned {
		index++
		value := retired(index)
		_, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %q (%q) VALUES (?)", col.Table, col.Column), value)
		require.NoError(t, err)
		seeded[col.Table+"."+col.Column] = value
	}
	for _, col := range mirrors.ProvenanceOwnedContext {
		index++
		value := retired(index)
		_, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %q (context_kind, %q) VALUES (?, ?)", col.Table, col.Column), "task", value)
		require.NoError(t, err)
		seeded[col.Table+"."+col.Column] = value
	}
	// A preserved column seeded with a retired-looking value: the conversion must
	// leave it byte-exact, so a preserved column the dependency actually owns is
	// red rather than silently rewritten.
	preservedValue := retired(index + 1000)
	for _, col := range preserved {
		_, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %q (%q) VALUES (?)", col.Table, col.Column), preservedValue)
		require.NoError(t, err)
		seeded[col.Table+"."+col.Column] = preservedValue
	}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = provenance.ConvertIDsInTx(ctx, tx, provenance.IDConversionOptions{})
	require.NoError(t, err)

	// Observe which seeded columns the conversion rewrote, inside the
	// uncommitted transaction so nothing is persisted.
	changed := map[string]bool{}
	for key := range seeded {
		table, column, ok := strings.Cut(key, ".")
		require.True(t, ok)
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, fmt.Sprintf("SELECT %q FROM %q WHERE %q IS NOT NULL LIMIT 1", column, table, column)).Scan(&value))
		if value != seeded[key] {
			changed[key] = true
		}
	}
	require.NoError(t, tx.Rollback())

	want := map[string]bool{}
	for _, col := range owned {
		want[col.Table+"."+col.Column] = true
	}
	for _, col := range mirrors.ProvenanceOwnedContext {
		want[col.Table+"."+col.Column] = true
	}
	require.Equal(t, want, changed,
		"the provenance mirrors and ConvertIDsInTx disagree on which columns convert")
}
